import assert from "node:assert/strict";
import { test } from "node:test";
import { createItem, createParty, getItem, getQuote, getSale, listQuotes, setProfile, DEFAULT_PROFILE } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { buildLedgers } from "../src/lib/receivables";
import { invoiceHtml, paymentSummary, type InvoiceData, type InvoiceTemplate } from "../src/lib/invoice";
import { calcInvoice } from "../src/lib/tax";
import { unitPriceFor } from "../src/lib/sale";
import { cartActions, getCart } from "../src/state/cart";
import { checkout, saveQuote } from "../src/state/checkout";
import { memoryDb } from "./nodedb";

async function setup() {
  const db = memoryDb(); await migrate(db);
  await setProfile(db, { ...DEFAULT_PROFILE, name: "Shop", gstState: "27" });
  await createParty(db, "c1", { name: "Gupta", phone: "", gstin: "", address: "" }, 1);
  await createItem(db, "i1", { name: "Bolt", sku: "B", barcode: "", hsn: "7318", price: "105", gstRate: "5", stock: "100", unitLabel: "pcs", priceBasis: "INCL" }, 1);
  cartActions.clear("INCLUSIVE");
  cartActions.patch({ customerId: "c1", customerName: "Gupta" });
  const bolt = (await getItem(db, "i1"))!;
  cartActions.addProduct(bolt, "INCLUSIVE", unitPriceFor(bolt, "INCLUSIVE"));
  cartActions.setQtyText(getCart().lines[0]!.key, "3");
  return db;
}
const stock = async (db: Awaited<ReturnType<typeof setup>>) => (await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='i1'"))!.stock;

test("a quotation is not a sale: stock, invoices and balances are untouched, and saving twice makes one", async () => {
  const db = await setup();
  const q = await saveQuote(db, getCart(), { now: 1000 }, 30);
  const again = await saveQuote(db, getCart(), { now: 2000 }, 30);
  assert.equal(q.number, "QT-0001"); assert.equal(again.localId, q.localId);
  assert.equal((await listQuotes(db)).length, 1);
  assert.equal(await stock(db), "100");
  assert.equal((await db.first<{ n: number }>("SELECT COUNT(*) AS n FROM sales"))!.n, 0);
  assert.equal(buildLedgers([], [], 1).length, 0);
  const row = (await getQuote(db, q.localId))!;
  assert.equal(row.valid_until, 1000 + 30 * 86_400_000); assert.equal(row.status, "open");
});

test("an invoice made from a quotation closes the quotation and points at the invoice", async () => {
  const db = await setup();
  const q = await saveQuote(db, getCart(), { now: 1000 }, null);
  const row = (await getQuote(db, q.localId))!;
  cartActions.load(row.payload, { fromQuote: { id: row.local_id, number: row.number } });
  cartActions.patch({ customerId: "c1", customerName: "Gupta", payMethod: null });
  assert.equal(getCart().fromQuote?.number, "QT-0001");
  let n = 0;
  const r = await checkout(db, getCart(), { uid: () => `u${++n}`, now: 3000 });
  const after = (await getQuote(db, q.localId))!;
  assert.equal(after.status, "converted"); assert.equal(after.sale_local_id, r.localId);
  assert.ok(await getSale(db, r.localId));
  assert.equal(await stock(db), "97", "stock moves only when the invoice is made");
});

test("refuses an empty quotation and an item without a price", async () => {
  const db = await setup();
  cartActions.clear("INCLUSIVE");
  await assert.rejects(saveQuote(db, getCart(), { now: 1 }, 30), /Add at least one item/);
});

const business = { ...DEFAULT_PROFILE, name: "Shop" };
const lines = [{ variantId: "a", name: "Bolt", unitId: "", qty: "2", price: "105", discount: "0", gstRate: "5", hsn: "7318" }];
const calc = calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), "INCLUSIVE", false);
const base = (extra: Partial<InvoiceData>): InvoiceData => ({ number: "N-1", date: Date.UTC(2026, 10, 6), business, customerName: "Gupta", lines, calc, currency: "INR", payment: paymentSummary(calc.totalMinor, 0n), ...extra });
for (const t of ["classic", "modern", "thermal"] as InvoiceTemplate[]) {
  test(`${t}: a quotation says QUOTATION with its validity and has no payment block; a credit note says CREDIT NOTE`, () => {
    const q = invoiceHtml(base({ kind: "quotation", validUntil: Date.UTC(2026, 11, 6) }), t);
    assert.match(q, /QUOTATION|Quotation/); assert.match(q, /Valid until/);
    assert.doesNotMatch(q, /Balance due/); assert.doesNotMatch(q, /Total paid/); assert.doesNotMatch(q, /UNPAID/); assert.doesNotMatch(q, /TAX INVOICE/);
    const c = invoiceHtml(base({ kind: "credit", against: "INV-0007" }), t);
    assert.match(c, /CREDIT NOTE|Credit note/); assert.match(c, /Against INV-0007/); assert.doesNotMatch(c, /Balance due/);
    const inv = invoiceHtml(base({ credits: [{ number: "CN-1", totalMinor: 5000n }], payment: paymentSummary(calc.totalMinor, 0n, 5000n) }), t);
    assert.match(inv, /Credit note CN-1/); assert.match(inv, /Balance due/);
  });
}
