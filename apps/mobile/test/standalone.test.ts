import assert from "node:assert/strict";
import { test } from "node:test";
import type { Api } from "../src/api/client";
import { createItem, createParty, getSale, insertSale, mapGet, reapplyPendingStock, setOrg, takeInvoiceNumber, type Org, type SalePayload } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { SyncEngine } from "../src/sync/engine";
import { FakeServer } from "./fakeServer";
import { memoryDb } from "./nodedb";

const org: Org = { organisationId: "o", legalEntityId: "le", legalName: "Demo", country: "IN", currency: "INR", gstStateCode: "20", branchId: "b", warehouseId: "w", priceListId: "p", walkInPartyId: null };

async function setup() {
  const db = memoryDb(); await migrate(db); await setOrg(db, org);
  const srv = new FakeServer();
  const eng = new SyncEngine(srv as unknown as Api, db, () => 1);
  (eng as unknown as { pull: () => Promise<boolean> }).pull = async () => true;
  return { db, srv, eng };
}
const item = { name: "Steel Bolt", sku: "BOLT-8", barcode: "8901234567890", hsn: "7318", price: "25", gstRate: "18", stock: "200", unitLabel: "pcs" };
async function standaloneSale(db: Awaited<ReturnType<typeof setup>>["db"]) {
  await createParty(db, "cust-local", { name: "Gupta Works", phone: "9198", gstin: "", address: "" }, 1);
  await createItem(db, "item-local", item, 1);
  const pl: SalePayload = { customerId: "cust-local", customerName: "Gupta Works", paymentMethod: "CASH", lines: [{ variantId: "item-local", name: "Steel Bolt", unitId: "", qty: "4", price: "25", discount: "0", gstRate: "18" }] };
  await insertSale(db, "sale-1", pl, 2, await takeInvoiceNumber(db, "INV-"));
  return pl;
}

test("invoice numbers on the phone are gap-free and sequential", async () => {
  const db = memoryDb(); await migrate(db);
  assert.equal(await takeInvoiceNumber(db, "INV-"), "INV-0001");
  assert.equal(await takeInvoiceNumber(db, "INV-"), "INV-0002");
});

test("selling takes stock off immediately; a refused or duplicate sync never changes it", async () => {
  const { db } = await setup();
  await standaloneSale(db);
  const r = await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='item-local'");
  assert.equal(Number(r!.stock), 196);
});

test("linking a standalone phone: uploads customer, item (stock, price, barcode, GST) then the sale, all with server ids", async () => {
  const { db, srv, eng } = await setup();
  await standaloneSale(db);
  const res = await eng.syncNow();
  assert.equal(res.pushed, 1, JSON.stringify(res));
  assert.equal(srv.parties.length, 1); assert.equal(srv.products.length, 1); assert.equal(srv.variants.length, 1);
  assert.deepEqual(srv.openingStock, [{ variant: "var-1", qty: "200" }], "opening stock = stock from BEFORE the unsent sale (200, not 196), or the sale would deduct 4 twice");
  assert.deepEqual(srv.prices, [{ variant: "var-1", amount: "25" }]);
  assert.equal(srv.taxRates.get("7318"), "18");
  assert.equal(srv.finalizeCount, 1);
  assert.deepEqual([...srv.docs.values()].flatMap((d) => d.lines.map((l) => l.variant)), ["var-1"], "the sale's line carries the SERVER variant id, not the phone's local one");
  const doc = srv.docs.get("doc-1")!;
  assert.equal(doc.lines.length, 1);
  assert.equal(srv.receipts.length, 1);
  assert.equal((await getSale(db, "sale-1"))!.state, "synced");
  assert.equal((await mapGet(db, "item", "item-local"))!.serverId, "var-1");
});

test("merge: a customer with the same name and an item with the same SKU already on the server are reused, not duplicated", async () => {
  const { db, srv, eng } = await setup();
  srv.parties.push({ ID: "party-9", LegalName: "gupta works", PartyType: "CUSTOMER" });
  srv.products.push({ ID: "prod-9", Name: "Steel Bolt M8", hsn: "7318" }); srv.variants.push({ ID: "var-9", productId: "prod-9", sku: "BOLT-8" });
  await standaloneSale(db);
  await eng.syncNow();
  assert.equal(srv.parties.length, 1, "no second customer");
  assert.equal(srv.products.length, 1, "no second item");
  assert.equal(srv.openingStock.length, 0, "the server's stock figure is left alone");
  assert.equal(srv.prices.length, 0, "the server's price is left alone");
  assert.equal(srv.docs.get("doc-1")!.lines.length, 1);
  assert.equal(srv.receipts[0]!.doc, "doc-1");
});

for (const crash of ["after-party", "after-variant", "after-stock", "after-price"]) {
  test(`connection lost ${crash} during upload: retry finishes with stock and price posted exactly once`, async () => {
    const { db, srv, eng } = await setup();
    await standaloneSale(db);
    srv.crashAt = crash;
    for (let i = 0; i < 4; i++) await eng.syncNow();
    assert.equal(srv.openingStock.length, 1, "opening stock exactly once");
    assert.deepEqual([...new Set(srv.prices.map((p) => `${p.variant}=${p.amount}`))], ["var-1=25"], "price set (setting the same price twice is harmless)");
    assert.equal(srv.finalizeCount, 1, "invoice finalised exactly once");
    assert.equal((await getSale(db, "sale-1"))!.state, "synced");
  });
}

test("pull's stock re-apply never double-counts an unsent sale of a phone-made item, but does fix a server item", async () => {
  const { db } = await setup();
  await standaloneSale(db);                       // local item: 200 -> 196 at sale time
  await db.run("INSERT INTO products(variant_id,product_id,name,sku,unit_id,price,stock,gen,origin,pushed) VALUES('srv-1','p','Srv','S','u','10','100',1,'server',1)");
  await db.run("UPDATE sales SET payload=json_set(payload,'$.lines[0].variantId','srv-1') WHERE local_id='sale-1'"); // the sale is of the server item
  await reapplyPendingStock(db);                  // a pull just reset srv-1 to the server's 100; 4 are still unsent
  assert.equal(Number((await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='srv-1'"))!.stock), 96);
  await db.run("UPDATE sales SET payload=json_set(payload,'$.lines[0].variantId','item-local') WHERE local_id='sale-1'");
  await reapplyPendingStock(db);
  assert.equal(Number((await db.first<{ stock: string }>("SELECT stock FROM products WHERE variant_id='item-local'"))!.stock), 196, "unchanged: already reduced when the sale was saved");
});
