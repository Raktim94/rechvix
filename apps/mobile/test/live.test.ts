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
