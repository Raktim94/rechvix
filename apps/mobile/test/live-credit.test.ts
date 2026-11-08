/** Opt-in end-to-end test of credit notes and returns against a real Rechvix server, using the phone's own code.
 *    RECHVIX_URL=http://localhost:8099 RECHVIX_EMAIL=... RECHVIX_PASSWORD=... npx tsx --test test/live-credit.test.ts  */
import assert from "node:assert/strict";
import { test } from "node:test";
import { Api } from "../src/api/client";
import { createItem, createParty, creditNotesForSale, getSale, insertSale, insertPayments } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { SyncEngine } from "../src/sync/engine";
import { issueCreditNote } from "../src/sync/creditNote";
import { calcInvoice, snapshot } from "../src/lib/tax";
import { memoryDb } from "./nodedb";

const URL = process.env.RECHVIX_URL, EMAIL = process.env.RECHVIX_EMAIL, PASSWORD = process.env.RECHVIX_PASSWORD;

test("live: a part return restores stock, a money-only credit does not, a whole discounted line is credited in full, and the server balance matches the phone", { skip: !URL }, async () => {
  let cookie: string | null = null;
  const api = new Api({ baseUrl: () => URL!, cookie: () => cookie, onCookie: (c) => (cookie = c), onUnauthorized: () => {} });
  await api.post("/auth/login", { email: EMAIL, password: PASSWORD, mfa_code: "" });
  const db = memoryDb(); await migrate(db);
  const tag = String(Date.now()).slice(-6);
  await createParty(db, "w-local", { name: "Walk-in Customer", phone: "", gstin: "", address: "" }, 1, true);
  await createParty(db, "c-local", { name: `Credit Customer ${tag}`, phone: `92${tag}00`, gstin: "", address: "" }, 2);
  await createItem(db, "i-local", { name: `Credit Item ${tag}`, sku: `CR-${tag}`, barcode: `88${tag}`, hsn: "7318", price: "100", gstRate: "18", stock: "50", unitLabel: "pcs", priceBasis: "EXCL" }, 3);
  const mk = (discount: string) => {
    const lines = [{ variantId: "i-local", name: `Credit Item ${tag}`, unitId: "", qty: "4", price: "100", discount, gstRate: "18", hsn: "7318" }];
    return { customerId: "c-local", customerName: `Credit Customer ${tag}`, paymentMethod: null, pricingMode: "EXCLUSIVE" as const, sellerState: "20", customerState: "20", lines,
      calc: snapshot(calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), "EXCLUSIVE", false)) };
  };
  await insertSale(db, "plain", mk("0"), 10, "INV-0001");
  await insertSale(db, "disc", mk("40"), 11, "INV-0002");
  await insertPayments(db, { groupId: "g" + tag, partyId: "c-local", method: "UPI", reference: "R1", note: "", paidAt: Date.now(), parts: [{ id: "p" + tag, saleLocalId: "plain", amount: "100.00" }] }, Date.now());
  const eng = new SyncEngine(api, db);
  const r = await eng.syncNow();
  const bad = await db.all<{ error: string }>("SELECT error FROM payments WHERE state='failed'");
  assert.equal(r.failed ?? 0, 0, JSON.stringify(r) + JSON.stringify(bad));
  const plain = (await getSale(db, "plain"))!, disc = (await getSale(db, "disc"))!;
  assert.equal(plain.state, "synced"); assert.equal(disc.state, "synced");
  const stockOf = async () => Number((await db.first<{ stock: string }>("SELECT stock FROM products WHERE sku=?", [`CR-${tag}`]))!.stock);

  // 1) Return 1 of 4, goods back: a SALES_RETURN for exactly the line's price share, numbered by the server.
  const a = await issueCreditNote(api, db, "plain", [{ index: 0, qty: "1" }], true, "one damaged", "cn-a", 20);
  const docA = await api.get<{ document: { DocumentType: string; Status: string; GrandTotalAmount: { amount: string } } }>(`/sales/documents/${(await creditNotesForSale(db, "plain"))[0]!.server_doc_id}`);
  assert.equal(docA.document.DocumentType, "SALES_RETURN"); assert.equal(docA.document.Status, "FINALIZED");
  assert.equal(Number(docA.document.GrandTotalAmount.amount), 118, "1 x 100 + 18% GST");
  assert.equal(a.totalMinor, 11800n);
  assert.equal(await stockOf(), 50 - 4 - 4 + 1);

  // Asking again with the same id books nothing twice.
  await issueCreditNote(api, db, "plain", [{ index: 0, qty: "1" }], true, "one damaged", "cn-a", 21);
  assert.equal((await creditNotesForSale(db, "plain")).length, 1);

  // 2) Money-only credit: CREDIT_NOTE, stock untouched.
  const before = await stockOf();
  await issueCreditNote(api, db, "plain", [{ index: 0, qty: "1" }], false, "price adjustment", "cn-b", 22);
  const notes = await creditNotesForSale(db, "plain");
  const docB = await api.get<{ document: { DocumentType: string } }>(`/sales/documents/${notes[1]!.server_doc_id}`);
  assert.equal(docB.document.DocumentType, "CREDIT_NOTE"); assert.equal(await stockOf(), before);

  // 3) A part-return of a discounted line is credited at its share of the discount (40 over 4 = 10 each): (100 - 10) + 18% = 106.20.
  const part = await issueCreditNote(api, db, "disc", [{ index: 0, qty: "1" }], true, "one back", "cn-c", 23);
  assert.equal(part.totalMinor, 10620n);
  const rest = await issueCreditNote(api, db, "disc", [{ index: 0, qty: "3" }], true, "the rest", "cn-d", 24);
  assert.equal(rest.totalMinor, 31860n, "3 x 100 - 30 = 270, + 18% = 318.60; together 424.80, the whole invoice");

  // The server's own receivable for this customer agrees with the phone: invoices 472.00 + 424.80, credits 118 + 118 + 424.80, paid 100.
  const rec = await api.get<{ parties: { PartyName: string; Total: { amount: string } }[] | null }>("/reports/accounting/receivables/detailed");
  const mine = (rec.parties ?? []).find((p) => p.PartyName === `Credit Customer ${tag}`);
  assert.ok(mine, "customer appears with a balance");
  assert.equal(Number(mine!.Total.amount), 136, "472 - 236 credited - 100 paid; the discounted invoice is fully credited");
});
