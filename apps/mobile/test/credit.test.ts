import assert from "node:assert/strict";
import { test } from "node:test";
import type { Api } from "../src/api/client";
import { createItem, createParty, creditNotesForSale, getSale, insertSale, setOrg, type Org, type SalePayload } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { SyncEngine } from "../src/sync/engine";
import { issueCreditNote, CreditNoteError } from "../src/sync/creditNote";
import { calcInvoice, snapshot } from "../src/lib/tax";
import { buildLedgers } from "../src/lib/receivables";
import { creditsBySale } from "../src/lib/sale";
import { paymentSummary } from "../src/lib/invoice";
import { FakeServer } from "./fakeServer";
import { memoryDb } from "./nodedb";

const org: Org = { organisationId: "o", legalEntityId: "le", legalName: "Demo", country: "IN", currency: "INR", gstStateCode: "20", branchId: "b", warehouseId: "w", priceListId: "p", walkInPartyId: null };

async function setup(discount = "0") {
  const db = memoryDb(); await migrate(db); await setOrg(db, org);
  const srv = new FakeServer();
  const eng = new SyncEngine(srv as unknown as Api, db, () => 1);
  (eng as unknown as { pull: () => Promise<boolean> }).pull = async () => true;
  await createParty(db, "c-local", { name: "Gupta", phone: "9", gstin: "", address: "" }, 1);
  await createItem(db, "i-local", { name: "Bolt", sku: "B", barcode: "", hsn: "7318", price: "100", gstRate: "18", stock: "100", unitLabel: "pcs", priceBasis: "EXCL" }, 1);
  const lines = [{ variantId: "i-local", name: "Bolt", unitId: "", qty: "4", price: "100", discount, gstRate: "18", hsn: "7318" }];
  const payload: SalePayload = { customerId: "c-local", customerName: "Gupta", lines, paymentMethod: null, pricingMode: "EXCLUSIVE", sellerState: "20", customerState: "20", calc: snapshot(calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), "EXCLUSIVE", false)) };
  await insertSale(db, "s1", payload, 10, "INV-0001");
  await eng.syncNow();
  return { db, srv, eng };
}
const stock = async (db: Awaited<ReturnType<typeof setup>>["db"]) => (await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='i-local'"))!.stock;

test("a part return goes to the server as a numbered return, lowers what is owed, and puts the stock back once", async () => {
  const { db, srv } = await setup();
  assert.equal(await stock(db), "96");
  const r = await issueCreditNote(srv as unknown as Api, db, "s1", [{ index: 0, qty: "1" }], true, "one damaged", "cn1", 20);
  assert.equal(srv.converts.length, 1);
  assert.equal(srv.converts[0]!.type, "SALES_RETURN");
  assert.deepEqual(Object.values(srv.converts[0]!.quantities), ["1"]);
  assert.ok(r.number.startsWith("INV/"), "the number is the server's");
  assert.equal(await stock(db), "97");
  const notes = await creditNotesForSale(db, "s1");
  assert.equal(notes.length, 1); assert.equal(notes[0]!.state, "issued");
  // Asking again with the same id changes nothing.
  await issueCreditNote(srv as unknown as Api, db, "s1", [{ index: 0, qty: "1" }], true, "one damaged", "cn1", 21);
  assert.equal(srv.converts.length, 1); assert.equal(await stock(db), "97");
});

test("money-only credit uses a credit note and leaves stock alone", async () => {
  const { db, srv } = await setup();
  await issueCreditNote(srv as unknown as Api, db, "s1", [{ index: 0, qty: "2" }], false, "price adjustment", "cn2", 20);
  assert.equal(srv.converts[0]!.type, "CREDIT_NOTE");
  assert.equal(await stock(db), "96");
});

test("connection lost after the server finalized: the retry finds the same credit note and books nothing twice", async () => {
  const { db, srv } = await setup();
  srv.crashAt = "after-finalize"; // the real invoice finalized earlier; this arms the next finalize: the credit note's
  await assert.rejects(issueCreditNote(srv as unknown as Api, db, "s1", [{ index: 0, qty: "1" }], true, "x", "cn3", 20));
  assert.equal((await creditNotesForSale(db, "s1"))[0]!.state, "pending");
  assert.equal(await stock(db), "96", "stock is not touched until the server confirms");
  await issueCreditNote(srv as unknown as Api, db, "s1", [{ index: 0, qty: "1" }], true, "x", "cn3", 21);
  assert.equal(srv.converts.length, 1, "one draft, not two");
  assert.equal((await creditNotesForSale(db, "s1"))[0]!.state, "issued");
  assert.equal(await stock(db), "97");
});

test("cannot credit more than was sold, in total, or an empty selection, or without a reason", async () => {
  const { db, srv } = await setup();
  const api = srv as unknown as Api;
  await assert.rejects(issueCreditNote(api, db, "s1", [{ index: 0, qty: "5" }], true, "x", "a", 20), CreditNoteError);
  await assert.rejects(issueCreditNote(api, db, "s1", [{ index: 0, qty: "0" }], true, "x", "b", 20), CreditNoteError);
  await assert.rejects(issueCreditNote(api, db, "s1", [{ index: 0, qty: "1" }], true, "  ", "c", 20), CreditNoteError);
  await issueCreditNote(api, db, "s1", [{ index: 0, qty: "3" }], true, "x", "d", 20);
  await assert.rejects(issueCreditNote(api, db, "s1", [{ index: 0, qty: "2" }], true, "x", "e", 21), /Only 1/);
  assert.equal(srv.converts.length, 1);
});

test("a part return of a discounted line is refused rather than credited at the wrong amount", async () => {
  const { db, srv } = await setup("20");
  await assert.rejects(issueCreditNote(srv as unknown as Api, db, "s1", [{ index: 0, qty: "1" }], true, "x", "a", 20), /discount/);
  await issueCreditNote(srv as unknown as Api, db, "s1", [{ index: 0, qty: "4" }], true, "whole line", "b", 20);
  assert.equal(srv.converts.length, 1);
});

test("an invoice the server does not hold yet cannot be credited, and the sale is untouched", async () => {
  const { db, srv } = await setup();
  await insertSale(db, "s2", (await getSale(db, "s1"))!.payload, 30, "INV-0002");
  await assert.rejects(issueCreditNote(srv as unknown as Api, db, "s2", [{ index: 0, qty: "1" }], true, "x", "a", 20), /hasn't reached the server/);
});

test("credit notes lower the net bill; money received beyond it shows as owed back, never as a payment", () => {
  const sale = (total: string) => ({ local_id: "s1", state: "synced", payload: { customerId: "c", customerName: "Gupta", lines: [], paymentMethod: null }, doc_number: "INV-1", local_number: null, created_at: 1, total, due_date: null, cancelled: 0 }) as never;
  const pay = (amount: string) => ({ id: "p", sale_local_id: "s1", party_id: "c", amount, state: "synced", reversed_at: null, paid_at: 2, server_doc_id: null }) as never;
  const credits = creditsBySale([{ sale_local_id: "s1", total: "200.00", state: "issued" }, { sale_local_id: "s1", total: "50.00", state: "pending" }]);
  assert.equal(credits.get("s1"), 20000n, "a note the server has not issued is not counted");
  const owing = buildLedgers([sale("1000.00")], [], 5, credits)[0]!;
  assert.equal(owing.balance.owedMinor, 80000n);
  const over = buildLedgers([sale("1000.00")], [pay("900.00")], 5, new Map([["s1", 20000n]]))[0]!;
  assert.equal(over.balance.owedMinor, 0n);
  assert.equal(over.balance.creditMinor, 10000n - 0n, "paid 900.00 against a net bill of 800.00: 100.00 owed back");
});

test("payment summary with a credit note", () => {
  const s = paymentSummary(100000n, 30000n, 20000n);
  assert.equal(s.dueMinor, 50000n); assert.equal(s.refundMinor, 0n); assert.equal(s.status, "PARTIAL");
  const r = paymentSummary(100000n, 100000n, 20000n);
  assert.equal(r.dueMinor, 0n); assert.equal(r.refundMinor, 20000n);
  assert.deepEqual(paymentSummary(100000n, 0n), { paidMinor: 0n, dueMinor: 100000n, status: "UNPAID" });
});
