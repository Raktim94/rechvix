import assert from "node:assert/strict";
import { test } from "node:test";
import type { Api } from "../src/api/client";
import { createItem, createParty, getPayment, getSale, insertPayments, insertSale, reversePayment, setOrg, type Org, type SalePayload } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { SyncEngine } from "../src/sync/engine";
import { placeOfSupply } from "../src/sync/saga";
import { calcInvoice, snapshot } from "../src/lib/tax";
import { totalMismatch } from "../src/lib/sale";
import { FakeServer } from "./fakeServer";
import { memoryDb } from "./nodedb";
import { cancelSyncedInvoice } from "../src/sync/correct";
import { cancelLocalSale, paymentsForSale } from "../src/db/repo";
import { minorToString } from "../src/lib/decimal";

const org: Org = { organisationId: "o", legalEntityId: "le", legalName: "Demo", country: "IN", currency: "INR", gstStateCode: "20", branchId: "b", warehouseId: "w", priceListId: "p", walkInPartyId: null };

async function setup() {
  const db = memoryDb(); await migrate(db); await setOrg(db, org);
  const srv = new FakeServer();
  const eng = new SyncEngine(srv as unknown as Api, db, () => 1);
  (eng as unknown as { pull: () => Promise<boolean> }).pull = async () => true;
  await createParty(db, "c-local", { name: "Gupta", phone: "9", gstin: "", address: "" }, 1);
  await createItem(db, "i-local", { name: "Bolt", sku: "B", barcode: "", hsn: "7318", price: "105", gstRate: "5", stock: "100", unitLabel: "pcs", priceBasis: "INCL" }, 1);
  return { db, srv, eng };
}
const inclusive = (extra: Partial<SalePayload> = {}): SalePayload => {
  const lines = [{ variantId: "i-local", name: "Bolt", unitId: "", qty: "2", price: "105", discount: "0", gstRate: "5", hsn: "7318" }];
  return { customerId: "c-local", customerName: "Gupta", lines, paymentMethod: null, pricingMode: "INCLUSIVE", sellerState: "20", calc: snapshot(calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), "INCLUSIVE")), ...extra };
};
const part = (id: string, sale: string | null, amount: string) => ({ id, saleLocalId: sale, amount });
const pay = (db: Awaited<ReturnType<typeof setup>>["db"], parts: ReturnType<typeof part>[], paidAt = 5) =>
  insertPayments(db, { groupId: "g" + parts[0]!.id, partyId: "c-local", method: "UPI", reference: "UTR", note: "", paidAt, parts }, paidAt);

test("an inclusive sale is created as INCLUSIVE with the prices exactly as entered (the server takes the tax out)", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s1", inclusive(), 10, "INV-0001");
  await eng.syncNow();
  const body = srv.createBodies[0]!;
  assert.equal(body.pricing_mode, "INCLUSIVE");
  assert.equal([...srv.docs.values()][0]!.lines[0]!.price, "105", "unit price sent as entered (GST included), not converted or taxed again");
  assert.equal(body.place_of_supply_state_code, "20");
});
test("a sale queued before pricing modes existed is still created as EXCLUSIVE", async () => {
  const { db, srv, eng } = await setup();
  const legacy: SalePayload = { customerId: "c-local", customerName: "Gupta", paymentMethod: null, lines: [{ variantId: "i-local", name: "Bolt", unitId: "", qty: "1", price: "100", discount: "0", gstRate: "5" }] };
  await insertSale(db, "old", legacy, 10, "INV-0001");
  await eng.syncNow();
  assert.equal(srv.createBodies[0]!.pricing_mode, "EXCLUSIVE");
});
test("place of supply: the customer's state when known and different (IGST), the seller's otherwise, never guessed", () => {
  assert.equal(placeOfSupply("20", { customerState: "27", sellerState: "20" }), "27");
  assert.equal(placeOfSupply("20", { customerState: "20", sellerState: "20" }), "20");
  assert.equal(placeOfSupply("20", { customerState: null, sellerState: "20" }), "20");
  assert.equal(placeOfSupply("20", {}), "20");
});
test("payments post the exact amounts entered (not 'whatever is outstanding'), each once, after the invoice", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s1", inclusive(), 10, "INV-0001");
  await pay(db, [part("p1", "s1", "100.00")], 5); await pay(db, [part("p2", "s1", "50.00")], 6);
  const r = await eng.syncNow();
  assert.equal(r.paymentsPosted, 2, JSON.stringify(r));
  assert.deepEqual(srv.receipts.map((x) => [x.amount, x.method, x.clientRef]), [["100.00", "UPI", "mobile:pay:p1"], ["50.00", "UPI", "mobile:pay:p2"]]);
  assert.equal(srv.receipts[0]!.doc, "doc-1");
  assert.equal((await getPayment(db, "p1"))!.state, "synced");
  const again = await eng.syncNow();
  assert.equal(again.paymentsPosted, 0); assert.equal(srv.receipts.length, 2, "re-syncing books nothing twice");
});
for (const crash of ["after-receipt"]) {
  test(`connection lost ${crash}: the retry returns the same receipt, so the money is booked exactly once`, async () => {
    const { db, srv, eng } = await setup();
    await insertSale(db, "s1", inclusive(), 10);
    await pay(db, [part("p1", "s1", "210.00")]);
    srv.crashAt = crash;
    for (let i = 0; i < 3; i++) await eng.syncNow();
    assert.equal(srv.receipts.length, 1, "one receipt, not two");
    assert.equal((await getPayment(db, "p1"))!.state, "synced");
  });
}
test("a payment for an invoice that hasn't reached the server waits instead of failing", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s1", inclusive(), 10);
  await pay(db, [part("p1", "s1", "50.00")]);
  srv.reject = { path: /\/sales\/documents$/, status: 500, code: "X", message: "down" };
  await eng.syncNow();
  assert.equal(srv.receipts.length, 0); assert.equal((await getPayment(db, "p1"))!.state, "pending");
  srv.reject = null;
  await eng.syncNow();
  assert.equal(srv.receipts.length, 1);
});
test("reversing a payment the server already booked sends an audited reversal; one it never saw is simply never sent", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s1", inclusive(), 10);
  await pay(db, [part("p1", "s1", "100.00")]); await eng.syncNow();
  await pay(db, [part("p2", "s1", "10.00")]);
  await reversePayment(db, "p1", "Wrong amount", 50);     // booked -> needs a server reversal
  await reversePayment(db, "p2", "Typo", 51);             // never sent -> voided
  const r = await eng.syncNow();
  assert.equal(srv.receipts.length, 1, "the voided payment never reached the server");
  assert.equal(srv.receipts[0]!.reversed, true); assert.equal(srv.receipts[0]!.reason, "Wrong amount");
  assert.equal((await getPayment(db, "p1"))!.reversal_state, "synced");
  assert.equal(r.paymentsFailed, 0);
  await eng.syncNow(); assert.equal(srv.receipts.length, 1);
});
test("a reversal retried after the server already reversed it is treated as done", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s1", inclusive(), 10);
  await pay(db, [part("p1", "s1", "100.00")]); await eng.syncNow();
  srv.receipts[0]!.reversed = true;                       // reversed on the server by someone else / a lost answer
  await reversePayment(db, "p1", "dup", 60); await eng.syncNow();
  assert.equal((await getPayment(db, "p1"))!.reversal_state, "synced");
});
test("legacy: a sale queued with a payment method still settles in full once, at the server's total", async () => {
  const { db, srv, eng } = await setup();
  const legacy: SalePayload = { customerId: "c-local", customerName: "Gupta", paymentMethod: "CASH", lines: [{ variantId: "i-local", name: "Bolt", unitId: "", qty: "1", price: "100", discount: "0", gstRate: "5" }] };
  await insertSale(db, "old", legacy, 10, "INV-0001");
  await eng.syncNow();
  assert.equal(srv.receipts.length, 1); assert.equal(srv.receipts[0]!.method, "CASH");
});
test("server total differing from the issued invoice is detected, not hidden", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s1", inclusive(), 10);
  await eng.syncNow();
  assert.equal(totalMismatch((await getSale(db, "s1"))!), true, "the fake server taxes on top (its own rule), so its total differs from the inclusive invoice: flagged");
  void srv;
});


test("correcting a synced invoice reverses its payments, cancels it on the server once, and survives a lost answer", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s1", inclusive(), 10, "INV-0001");
  await pay(db, [part("p1", "s1", "100.00")]);
  await eng.syncNow();
  assert.equal(srv.receipts.length, 1);
  srv.crashAt = "after-cancel";
  await assert.rejects(() => cancelSyncedInvoice(srv as unknown as Api, db, "s1", "wrong rate", 50));
  const again = await cancelSyncedInvoice(srv as unknown as Api, db, "s1", "wrong rate", 60);
  assert.equal(minorToString(again.receivedMinor), "100.00", "a retry still knows what had been received");
  assert.equal(srv.receipts[0]!.reversed, true);
  assert.equal([...srv.docs.values()][0]!.Status, "CANCELLED");
  assert.equal((await getSale(db, "s1"))!.cancelled, 1);
  const stock = (await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='i-local'"))!.stock;
  assert.equal(stock, "100");
  assert.equal(srv.cancelCount, 1);
  void paymentsForSale;
});

test("an invoice that never left the phone is cancelled locally and never sent", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "s2", inclusive(), 10, "INV-0002");
  await pay(db, [part("p9", "s2", "50.00")]);
  await cancelLocalSale(db, "s2", "customer left", 20);
  await eng.syncNow();
  assert.equal(srv.docs.size, 0);
  assert.equal((await db.first<{ state: string }>("SELECT state FROM payments WHERE id='p9'"))!.state, "void");
});
