import assert from "node:assert/strict";
import { test } from "node:test";
import {
  canEditSale, createItem, getSale, groupPaymentsBySale, insertPayments, insertSale, markPaymentPosted, paymentsForSale, pendingPaymentWork,
  reversePayment, saleRevisions, unallocatedPayments, updateSaleContent, type SalePayload,
} from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { minorToString } from "../src/lib/decimal";
import { dashboardStats } from "../src/lib/stats";
import { payStatus, saleTotalMinor, salePaidMinor } from "../src/lib/sale";
import { calcInvoice, snapshot } from "../src/lib/tax";
import { memoryDb } from "./nodedb";

const payload = (price = "105", qty = "2", pay: string | null = null): SalePayload => {
  const lines = [{ variantId: "i1", name: "Bolt", unitId: "", qty, price, discount: "0", gstRate: "5" }];
  const calc = calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), "INCLUSIVE");
  return { customerId: "c1", customerName: "Gupta", lines, paymentMethod: pay, pricingMode: "INCLUSIVE", calc: snapshot(calc) };
};
async function setup() {
  const db = memoryDb(); await migrate(db);
  await createItem(db, "i1", { name: "Bolt", sku: "B", barcode: "", hsn: "7318", price: "105", gstRate: "5", stock: "100", unitLabel: "pcs" }, 1);
  return db;
}
const pay = (id: string, sale: string | null, amount: string) => ({ id, saleLocalId: sale, amount });

test("sale total is the exact inclusive total; a payment brings it to partial, then paid", async () => {
  const db = await setup();
  await insertSale(db, "s1", payload(), 10, "INV-0001");
  const s = (await getSale(db, "s1"))!;
  assert.equal(minorToString(saleTotalMinor(s)), "210.00");
  await insertPayments(db, { groupId: "g1", partyId: "c1", method: "CASH", reference: "", note: "", paidAt: 20, parts: [pay("p1", "s1", "100.00")] }, 20);
  let ps = await paymentsForSale(db, "s1");
  assert.equal(payStatus(saleTotalMinor(s), salePaidMinor(s, ps)), "PARTIAL");
  await insertPayments(db, { groupId: "g2", partyId: "c1", method: "UPI", reference: "UTR9", note: "rest", paidAt: 30, parts: [pay("p2", "s1", "110.00")] }, 30);
  ps = await paymentsForSale(db, "s1");
  assert.equal(payStatus(saleTotalMinor(s), salePaidMinor(s, ps)), "PAID");
});
test("a payment split over two invoices and a credit row is stored as separate rows sharing a group", async () => {
  const db = await setup();
  await insertSale(db, "s1", payload(), 10); await insertSale(db, "s2", payload("50", "1"), 11);
  await insertPayments(db, { groupId: "g", partyId: "c1", method: "CASH", reference: "", note: "", paidAt: 50, parts: [pay("a", "s1", "210.00"), pay("b", "s2", "50.00"), pay("c", null, "40.00")] }, 50);
  assert.equal((await unallocatedPayments(db, "c1")).length, 1, "the leftover is customer credit");
  assert.equal(groupPaymentsBySale(await paymentsForSale(db, "s1")).size, 1);
});
test("reversing a payment keeps it (history), stops counting it, and a never-posted payment is voided locally", async () => {
  const db = await setup();
  await insertSale(db, "s1", payload(), 10);
  await insertPayments(db, { groupId: "g", partyId: "c1", method: "CASH", reference: "", note: "", paidAt: 20, parts: [pay("p1", "s1", "210.00"), pay("p2", "s1", "0.01")] }, 20);
  await markPaymentPosted(db, "p1", "srv-receipt-1");               // p1 reached the server, p2 never did
  await assert.rejects(reversePayment(db, "p1", "  ", 30), /why/);
  await reversePayment(db, "p1", "Typed twice", 30);
  await reversePayment(db, "p2", "Mistake", 31);
  const ps = await paymentsForSale(db, "s1"); const s = (await getSale(db, "s1"))!;
  assert.equal(ps.length, 2, "rows are never deleted");
  assert.equal(ps.find((p) => p.id === "p1")!.reversal_state, "pending", "a booked payment needs a server reversal");
  assert.equal(ps.find((p) => p.id === "p2")!.state, "void", "a payment the server never saw is just voided");
  assert.equal(minorToString(salePaidMinor(s, ps)), "0.00", "reversed money no longer counts as paid");
  await assert.rejects(reversePayment(db, "p1", "again", 40), /already/);
  const work = await pendingPaymentWork(db);
  assert.deepEqual(work.reverse.map((p) => p.id), ["p1"]); assert.equal(work.post.length, 0, "a reversed payment is never posted");
});
test("editing an unsynced invoice keeps the old version, fixes stock, and refuses to drop below what was paid", async () => {
  const db = await setup();
  await insertSale(db, "s1", payload("105", "2"), 10);                                                  // stock 100 -> 98
  assert.equal((await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='i1'"))!.stock, "98");
  assert.ok(canEditSale((await getSale(db, "s1"))!));
  const edited = payload("105", "5");
  await updateSaleContent(db, "s1", edited, "Customer wanted 5", 20, 525_00n);                        // stock back to 100, then 95
  assert.equal((await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='i1'"))!.stock, "95");
  const s = (await getSale(db, "s1"))!;
  assert.equal(s.revision, 1); assert.equal(s.payload.lines[0]!.qty, "5"); assert.equal(minorToString(saleTotalMinor(s)), "525.00");
  const revs = await saleRevisions(db, "s1");
  assert.equal(revs.length, 1); assert.equal(JSON.parse(revs[0]!.snapshot).lines[0].qty, "2", "the original is preserved");
  await insertPayments(db, { groupId: "g", partyId: "c1", method: "CASH", reference: "", note: "", paidAt: 30, parts: [pay("p", "s1", "400.00")] }, 30);
  await assert.rejects(updateSaleContent(db, "s1", payload("105", "1"), "shrink", 40, 105_00n), /More money is recorded/);
});
test("a sale the server has seen cannot be edited in place", async () => {
  const db = await setup();
  await insertSale(db, "s1", payload(), 10);
  await db.run("UPDATE sales SET progress='{\"docId\":\"d1\"}', state='failed' WHERE local_id='s1'");
  assert.equal(canEditSale((await getSale(db, "s1"))!), false);
  await assert.rejects(updateSaleContent(db, "s1", payload(), "x", 20, 210_00n), /reached the server/);
  await db.run("UPDATE sales SET progress='{}', state='synced' WHERE local_id='s1'");
  assert.equal(canEditSale((await getSale(db, "s1"))!), false);
});
test("dashboard keeps billed sales, money collected and money still owed apart", async () => {
  const db = await setup();
  const now = Date.now();
  await insertSale(db, "s1", payload("105", "2"), now - 1000);       // billed 210
  await insertSale(db, "s2", payload("100", "1"), now - 500);        // billed 100
  await insertPayments(db, { groupId: "g", partyId: "c1", method: "CASH", reference: "", note: "", paidAt: now - 100, parts: [pay("p1", "s1", "210.00"), pay("p2", "s2", "30.00")] }, now);
  const sales = (await db.all<never>("SELECT * FROM sales")).length;
  assert.equal(sales, 2);
  const { listSales, allPayments } = await import("../src/db/repo");
  const all = await listSales(db); const ps = await allPayments(db);
  const bySale = groupPaymentsBySale(ps);
  const st = dashboardStats(all, bySale, [], "today", now);
  assert.equal(minorToString(st.grossSalesMinor), "310.00", "billed");
  assert.equal(minorToString(st.collectedMinor), "240.00", "collected is money received, not money billed");
  assert.equal(minorToString(st.dueMinor), "70.00", "still owed");
  assert.equal(st.invoiceCount, 2); assert.equal(minorToString(st.averageBillMinor), "155.00");
  assert.equal(st.unsynced, 2);
  assert.equal(st.top[0]!.name, "Bolt");
  // reversing a payment moves it from collected back to owed
  await reversePayment(db, "p1", "wrong amount", now);
  const st2 = dashboardStats(await listSales(db), groupPaymentsBySale(await allPayments(db)), [], "today", now);
  assert.equal(minorToString(st2.collectedMinor), "30.00"); assert.equal(minorToString(st2.dueMinor), "280.00");
});
