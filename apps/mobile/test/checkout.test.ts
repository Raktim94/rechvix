import assert from "node:assert/strict";
import { test } from "node:test";
import { createItem, createParty, getItem, getSale, paymentsForSale, setProfile, DEFAULT_PROFILE } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { minorToString } from "../src/lib/decimal";
import { salePaidMinor, saleTotalMinor, unitPriceFor } from "../src/lib/sale";
import { cartActions, getCart, emptyCart } from "../src/state/cart";
import { checkout } from "../src/state/checkout";
import { memoryDb } from "./nodedb";

async function setup() {
  const db = memoryDb(); await migrate(db);
  await setProfile(db, { ...DEFAULT_PROFILE, name: "Shop", gstState: "27" });
  await createParty(db, "c1", { name: "Gupta", phone: "", gstin: "", address: "" }, 1);
  await createItem(db, "i1", { name: "Bolt", sku: "B", barcode: "", hsn: "7318", price: "105", gstRate: "5", stock: "100", unitLabel: "pcs", priceBasis: "INCL" }, 1);
  await createItem(db, "i2", { name: "Rice", sku: "R", barcode: "", hsn: "1006", price: "100", gstRate: "5", stock: "50", unitLabel: "kg", priceBasis: "EXCL" }, 1);
  return db;
}
const ids = () => { let n = 0; return { uid: () => `id${++n}`, now: 1000 }; };

test("price shown to the seller follows the billing mode", async () => {
  const db = await setup();
  const bolt = (await getItem(db, "i1"))!, rice = (await getItem(db, "i2"))!;
  assert.equal(unitPriceFor(bolt, "INCLUSIVE"), "105");
  assert.equal(unitPriceFor(rice, "INCLUSIVE"), "105");   // ₹100 + 5% GST
  assert.equal(unitPriceFor(bolt, "EXCLUSIVE"), "100");
});

test("a cart of fractional quantity is saved with exact figures and the money received", async () => {
  const db = await setup();
  const rice = (await getItem(db, "i2"))!;
  cartActions.clear("INCLUSIVE");
  cartActions.patch({ customerId: "c1", customerName: "Gupta" });
  cartActions.addProduct(rice, "INCLUSIVE", unitPriceFor(rice, "INCLUSIVE"));
  cartActions.setQtyText(getCart().lines[0]!.key, "2.5");
  assert.equal(getCart().lines[0]!.qtyError, null);
  cartActions.patch({ payMethod: "UPI", receivedText: "100" });
  const r = await checkout(db, getCart(), ids());
  assert.equal(minorToString(r.totalMinor), "262.50");
  const s = (await getSale(db, r.localId))!;
  assert.equal(minorToString(saleTotalMinor(s)), "262.50");
  const ps = await paymentsForSale(db, r.localId);
  assert.equal(minorToString(salePaidMinor(s, ps)), "100.00");
  // saving the same bill again makes no second invoice
  const again = await checkout(db, getCart(), ids());
  assert.equal(again.localId, r.localId);
  assert.equal((await db.first<{ n: number }>("SELECT COUNT(*) n FROM sales"))!.n, 1);
});

test("whole-unit items refuse a fraction; an empty or zero quantity is rejected", async () => {
  const db = await setup();
  const bolt = (await getItem(db, "i1"))!;
  cartActions.clear("INCLUSIVE");
  cartActions.addProduct(bolt, "INCLUSIVE", "105");
  const k = getCart().lines[0]!.key;
  cartActions.setQtyText(k, "1.5"); assert.ok(getCart().lines[0]!.qtyError);
  cartActions.setQtyText(k, ""); assert.ok(getCart().lines[0]!.qtyError);
  cartActions.setQtyText(k, "0"); assert.ok(getCart().lines[0]!.qtyError);
  cartActions.setQtyText(k, "12"); assert.equal(getCart().lines[0]!.qtyError, null);
  assert.equal(getCart().lines[0]!.qty, "12");
});
void emptyCart;

test("editing an unsynced invoice keeps its payments and reports them", async () => {
  const db = await setup();
  const bolt = (await getItem(db, "i1"))!;
  cartActions.clear("INCLUSIVE");
  cartActions.patch({ customerId: "c1", customerName: "Gupta", payMethod: "CASH", receivedText: "40" });
  cartActions.addProduct(bolt, "INCLUSIVE", "105");
  const r = await checkout(db, getCart(), ids());
  const s = (await getSale(db, r.localId))!;
  cartActions.load(s.payload, { editing: { localId: r.localId, number: r.number } });
  cartActions.step(getCart().lines[0]!.key, 1);
  const e = await checkout(db, getCart(), ids());
  assert.equal(minorToString(e.totalMinor), "210.00");
  assert.equal(minorToString(e.receivedMinor), "40.00");
  assert.equal((await getSale(db, r.localId))!.revision, 1);
});

test("bill 6000, customer pays 3000: 3000 received, 3000 due, and the invoice says so", async () => {
  const db = await setup();
  const rice = (await getItem(db, "i2"))!;
  cartActions.clear("INCLUSIVE");
  cartActions.patch({ customerId: "c1", customerName: "Gupta" });
  cartActions.addProduct(rice, "INCLUSIVE", "6000");
  cartActions.patch({ payMethod: "CASH", receivedText: "3000" });
  const r = await checkout(db, getCart(), ids());
  const s = (await getSale(db, r.localId))!;
  const paid = salePaidMinor(s, await paymentsForSale(db, r.localId));
  assert.equal(minorToString(r.totalMinor), "6000.00");
  assert.equal(minorToString(paid), "3000.00");
  const { paymentSummary } = await import("../src/lib/invoice");
  const sum = paymentSummary(r.totalMinor, paid);
  assert.equal(minorToString(sum.dueMinor), "3000.00"); assert.equal(sum.status, "PARTIAL");
  // clearing the box means nothing received, never "everything"
  cartActions.clear("INCLUSIVE"); cartActions.patch({ customerId: "c1", customerName: "Gupta" }); cartActions.addProduct(rice, "INCLUSIVE", "6000");
  cartActions.patch({ payMethod: "CASH", receivedText: "" });
  assert.equal((await checkout(db, getCart(), ids())).receivedMinor, 0n);
});
