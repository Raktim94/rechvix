/** Opt-in end-to-end test against a real Rechvix server (the same sync code the phone runs).
 *    RECHVIX_URL=http://localhost:8099 RECHVIX_EMAIL=... RECHVIX_PASSWORD=... npm run test:live  */
import assert from "node:assert/strict";
import { test } from "node:test";
import { Api } from "../src/api/client";
import { countByState, getOrg, getSale, insertSale, searchParties, searchProducts } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { SyncEngine } from "../src/sync/engine";
import { pullMaster } from "../src/sync/pull";
import { memoryDb } from "./nodedb";

const URL = process.env.RECHVIX_URL, EMAIL = process.env.RECHVIX_EMAIL, PASSWORD = process.env.RECHVIX_PASSWORD;

test("live: pull -> sell offline -> sync -> exactly one finalised, paid invoice", { skip: !URL }, async () => {
  let cookie: string | null = null;
  const api = new Api({ baseUrl: () => URL!, cookie: () => cookie, onCookie: (c) => (cookie = c), onUnauthorized: () => {} });
  await api.post("/auth/login", { email: EMAIL, password: PASSWORD, mfa_code: "" });
  const db = memoryDb(); await migrate(db);

  const pulled = await pullMaster(api, db);
  assert.ok(pulled.products > 0, "catalogue downloaded");
  const org = (await getOrg(db))!;
  assert.ok(org.walkInPartyId);
  const prods = await searchProducts(db, "");
  assert.ok(prods.every((p) => p.unit_id), "every product has a unit");
  const customer = (await searchParties(db, "")).find((p) => !p.is_walk_in) ?? (await searchParties(db, ""))[0]!;
  const p = prods.find((x) => x.price && Number(x.price) > 0)!;

  await insertSale(db, "live-" + Date.now(), { customerId: customer.id, customerName: customer.name, paymentMethod: "UPI", lines: [{ variantId: p.variant_id, name: p.name, unitId: p.unit_id, qty: "2", price: p.price!, discount: "0" }] }, Date.now());
  const id = (await db.first<{ local_id: string }>("SELECT local_id FROM sales"))!.local_id;
  const eng = new SyncEngine(api, db);
  const r = await eng.syncNow();
  assert.equal(r.pushed, 1, JSON.stringify(r));
  const s = (await getSale(db, id))!;
  assert.equal(s.state, "synced");
  assert.match(s.doc_number!, /^INV\//);
  assert.ok(Number(s.total) > Number(p.price) * 2 - 0.01, "server added tax on top");

  // Verify on the server itself.
  const doc = await api.get<{ document: { Status: string; GrandTotalAmount: { amount: string }; CustomerReference: string }; lines: unknown[] }>(`/sales/documents/${s.progress.docId}`);
  assert.equal(doc.document.Status, "FINALIZED");
  assert.equal(doc.lines.length, 1);
  assert.equal(doc.document.CustomerReference, `mobile:${id}`);
  const rc = await api.get<{ receipts: { Amount: { amount: string }; Method: string }[] }>(`/accounting/sales-documents/${s.progress.docId}/receipts`);
  assert.equal(rc.receipts.length, 1); assert.equal(rc.receipts[0]!.Method, "UPI");
  assert.equal(Number(rc.receipts[0]!.Amount.amount), Number(doc.document.GrandTotalAmount.amount), "paid in full at the server's total");

  // Syncing again must not create anything new.
  const again = await eng.syncNow();
  assert.equal(again.pushed, 0);
  assert.equal((await countByState(db)).synced, 1);
});

test("live: a standalone phone links to a server that already has data, merges, uploads its sale", { skip: !URL }, async () => {
  let cookie: string | null = null;
  const api = new Api({ baseUrl: () => URL!, cookie: () => cookie, onCookie: (c) => (cookie = c), onUnauthorized: () => {} });
  await api.post("/auth/login", { email: EMAIL, password: PASSWORD, mfa_code: "" });
  const db = memoryDb(); await migrate(db);
  const { createItem, createParty, takeInvoiceNumber } = await import("../src/db/repo");
  const tag = String(Date.now()).slice(-6);

  // What a phone has after weeks of standalone use: its own walk-in, a customer, a brand-new item and a sale using them.
  await createParty(db, "w-local", { name: "Walk-in Customer", phone: "", gstin: "", address: "" }, 1, true);
  await createParty(db, "c-local", { name: `Phone Customer ${tag}`, phone: `91${tag}00`, gstin: "", address: "" }, 2);
  await createItem(db, "i-local", { name: `Phone Item ${tag}`, sku: `PH-${tag}`, barcode: `99${tag}`, hsn: "7318", price: "40", gstRate: "18", stock: "50", unitLabel: "pcs" }, 3);
  const pl = { customerId: "c-local", customerName: `Phone Customer ${tag}`, paymentMethod: "CASH", lines: [{ variantId: "i-local", name: `Phone Item ${tag}`, unitId: "", qty: "3", price: "40", discount: "0", gstRate: "18" }] };
  const walk = { customerId: "w-local", customerName: "Walk-in Customer", paymentMethod: null, lines: pl.lines };
  const { insertSale } = await import("../src/db/repo");
  await insertSale(db, "s1", pl, 10, await takeInvoiceNumber(db, "INV-"));
  await insertSale(db, "s2", walk, 11, await takeInvoiceNumber(db, "INV-"));

  const before = await api.get<{ parties: { LegalName: string }[] }>("/contacts/parties");
  const walkBefore = before.parties.filter((p) => p.LegalName === "Walk-in Customer").length;

  const eng = new SyncEngine(api, db);
  const r = await eng.syncNow();
  const dbg = await db.all("SELECT local_id,state,error FROM sales"); assert.equal(r.message, undefined, JSON.stringify(r) + JSON.stringify(dbg));
  assert.equal(r.pushed, 2, JSON.stringify(r) + JSON.stringify(await db.all("SELECT local_id,state,error FROM sales")));

  const after = await api.get<{ parties: { LegalName: string }[] }>("/contacts/parties");
  assert.equal(after.parties.filter((p) => p.LegalName === "Walk-in Customer").length, Math.max(walkBefore, 1), "walk-in merged, not duplicated");
  assert.equal(after.parties.filter((p) => p.LegalName === `Phone Customer ${tag}`).length, 1);
  const s1 = (await getSale(db, "s1"))!;
  assert.equal(s1.state, "synced", s1.error ?? "");
  const doc = await api.get<{ document: { Status: string; GrandTotalAmount: { amount: string } }; lines: unknown[] }>(`/sales/documents/${s1.progress.docId}`);
  assert.equal(doc.document.Status, "FINALIZED");
  assert.equal(Number(doc.document.GrandTotalAmount.amount), 141.6, "3 x 40 + 18% GST, taxed by the server's own rate table");
  const stock = await api.get<{ results: { SKUCode: string; QuantityOnHand: string }[] }>(`/sales/billing-lookup?q=PH-${tag}&warehouse_id=${(await getOrg(db))!.warehouseId}`);
  assert.equal(Number(stock.results[0]!.QuantityOnHand), 44, "opening stock 50 (the figure BEFORE the two unsent sales) uploaded once, minus the 2 x 3 sold = 44, not 38");
  assert.equal((await getSale(db, "s2"))!.state, "synced");
});

test("live: GST-inclusive invoice, partial payments and a reversal agree with the server to the paisa", { skip: !URL }, async () => {
  let cookie: string | null = null;
  const api = new Api({ baseUrl: () => URL!, cookie: () => cookie, onCookie: (c) => (cookie = c), onUnauthorized: () => {} });
  await api.post("/auth/login", { email: EMAIL, password: PASSWORD, mfa_code: "" });
  const db = memoryDb(); await migrate(db);
  const { createItem, createParty, insertPayments, insertSale, reversePayment, getPayment } = await import("../src/db/repo");
  const { calcInvoice, snapshot } = await import("../src/lib/tax");
  const { totalMismatch } = await import("../src/lib/sale");
  const tag = String(Date.now()).slice(-6);

  await createParty(db, "w", { name: "Walk-in Customer", phone: "", gstin: "", address: "" }, 1, true);
  await createParty(db, "cust", { name: `Inclusive Customer ${tag}`, phone: `93${tag}00`, gstin: "27ABCDE1234F1Z5", address: "" }, 2);
  await createItem(db, "it", { name: `Sticker Item ${tag}`, sku: `ST-${tag}`, barcode: "", hsn: "8805", price: "105", gstRate: "5", stock: "50", unitLabel: "pcs", priceBasis: "INCL" }, 3);
  const lines = [{ variantId: "it", name: `Sticker Item ${tag}`, unitId: "", qty: "2", price: "105", discount: "0", gstRate: "5", hsn: "8805" }];
  const calc = calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), "INCLUSIVE", true);
  assert.equal(calc.totalMinor, 21000n);
  await insertSale(db, "s1", { customerId: "cust", customerName: "x", lines, paymentMethod: null, pricingMode: "INCLUSIVE", sellerState: "20", customerState: "27", calc: snapshot(calc) }, Date.now(), "INV-0001");
  await insertPayments(db, { groupId: "g1", partyId: "cust", method: "UPI", reference: "UTR1", note: "", paidAt: Date.now() - 2000, parts: [{ id: "pay1", saleLocalId: "s1", amount: "100.00" }] }, Date.now());
  await insertPayments(db, { groupId: "g2", partyId: "cust", method: "CASH", reference: "", note: "", paidAt: Date.now() - 1000, parts: [{ id: "pay2", saleLocalId: "s1", amount: "110.00" }] }, Date.now());

  const eng = new SyncEngine(api, db);
  const r = await eng.syncNow();
  assert.equal(r.message, undefined, JSON.stringify(r) + JSON.stringify(await db.all("SELECT local_id,state,error FROM sales")));
  const s = (await getSale(db, "s1"))!;
  assert.equal(s.state, "synced", s.error ?? "");
  assert.equal(s.total, "210", "the server booked exactly the GST-inclusive total entered: 2 x 105");
  assert.equal(totalMismatch(s), false, "phone invoice and server invoice agree");

  const pv = await api.get<{ taxes: { label: string; amount: string }[]; grand_total: string; subtotal: string }>(`/sales/documents/${s.progress.docId}/preview`);
  assert.equal(pv.grand_total, "210.00");
  assert.deepEqual(pv.taxes, calc.components.map((c) => ({ label: c.label, amount: (Number(c.minor) / 100).toFixed(2) })), "server tax split (IGST for another state) equals the phone's");
  assert.equal(pv.subtotal, "200.00");

  const receipts = await api.get<{ receipts: { ID: string; Amount: { amount: string }; ReversedAt: string | null; ClientRef: string }[] }>(`/accounting/sales-documents/${s.progress.docId}/receipts`);
  assert.deepEqual(receipts.receipts.map((x) => Number(x.Amount.amount)).sort((a, b) => a - b), [100, 110]);
  assert.ok(receipts.receipts.every((x) => x.ClientRef.startsWith("mobile:pay:")));
  const doc1 = await api.get<{ document: { PaidAmount: string } }>(`/sales/documents/${s.progress.docId}`);
  assert.equal(Number(doc1.document.PaidAmount), 210);

  await eng.syncNow(); // nothing new
  assert.equal((await api.get<{ receipts: unknown[] }>(`/accounting/sales-documents/${s.progress.docId}/receipts`)).receipts.length, 2, "a second sync books no extra receipt");

  await reversePayment(db, "pay1", "Entered the wrong amount", Date.now());
  await eng.syncNow();
  assert.equal((await getPayment(db, "pay1"))!.reversal_state, "synced");
  const doc2 = await api.get<{ document: { PaidAmount: string } }>(`/sales/documents/${s.progress.docId}`);
  assert.equal(Number(doc2.document.PaidAmount), 110, "after reversing the ₹100 receipt, only ₹110 counts as paid");
  const after = await api.get<{ receipts: { ReversedAt: string | null; ReversalReason: string }[] }>(`/accounting/sales-documents/${s.progress.docId}/receipts`);
  assert.equal(after.receipts.filter((x) => x.ReversedAt).length, 1); assert.equal(after.receipts.find((x) => x.ReversedAt)!.ReversalReason, "Entered the wrong amount");
});
