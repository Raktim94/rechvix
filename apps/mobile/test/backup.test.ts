import assert from "node:assert/strict";
import { test } from "node:test";
import { createItem, createParty, getPayment, getQuote, insertCreditNote, insertPayments, insertQuote, insertSale, kvGet, kvSet, setLowAt, type SalePayload } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { BACKUP_TABLES, BackupError, buildBackup, inspectBackup, restoreBackup } from "../src/lib/backup";
import { memoryDb } from "./nodedb";

const payload = (): SalePayload => ({ customerId: "c1", customerName: "Gupta", paymentMethod: null, pricingMode: "EXCLUSIVE", lines: [{ variantId: "i1", name: "Bolt", unitId: "", qty: "2", price: "100", discount: "0", gstRate: "18" }] });

async function populated() {
  const db = memoryDb(); await migrate(db);
  await createParty(db, "c1", { name: "Gupta", phone: "9", gstin: "", address: "" }, 1);
  await createItem(db, "i1", { name: "Bolt", sku: "BOLT", barcode: "", hsn: "", price: "100", gstRate: "18", stock: "50", unitLabel: "pcs" }, 1);
  await setLowAt(db, "BOLT", "12");
  await insertSale(db, "s1", payload(), 10, "INV-0001");
  await insertPayments(db, { groupId: "g1", partyId: "c1", method: "UPI", reference: "UTR1", note: "n", paidAt: 11, parts: [{ id: "p1", saleLocalId: "s1", amount: "100.00" }] }, 11);
  await insertQuote(db, "q1", "QT-0001", payload(), "236.00", 12, 99);
  await insertCreditNote(db, "cn1", "s1", payload(), "damaged", 13);
  await kvSet(db, "inv_counter", "7"); await kvSet(db, "server", "http://keep.me");
  return db;
}
const dump = async (db: Awaited<ReturnType<typeof populated>>) => Object.fromEntries(await Promise.all(BACKUP_TABLES.map(async (t) => [t, await db.all(`SELECT * FROM ${t} ORDER BY 1`)])));

test("a backup holds every record, including payments, quotations, credit notes and low-stock levels", async () => {
  const b = await buildBackup(await populated());
  assert.equal(b.format, 2); assert.equal(b.schema, 5);
  for (const t of ["payments", "quotes", "credit_notes", "item_low", "sales", "parties", "products"] as const) assert.ok(b.tables[t]!.length > 0, `${t} is in the backup`);
  assert.ok(!b.kv.some((r) => r.key === "server"), "the server link is not part of a backup");
});

test("restoring into an empty phone brings back exactly what was backed up", async () => {
  const src = await populated();
  const text = JSON.stringify(await buildBackup(src));
  const fresh = memoryDb(); await migrate(fresh);
  const counts = await restoreBackup(fresh, text);
  assert.equal(counts.payments, 1);
  assert.deepEqual(await dump(fresh), await dump(src));
  assert.equal((await getPayment(fresh, "p1"))!.amount, "100.00");
  assert.equal((await getQuote(fresh, "q1"))!.number, "QT-0001");
  assert.equal(await kvGet(fresh, "inv_counter"), "7");
});

test("restoring replaces what is on the phone now, keeps the server link, and leaves nothing of the later work behind", async () => {
  const db = await populated();
  const text = JSON.stringify(await buildBackup(db));
  await insertSale(db, "s2", payload(), 20, "INV-0002");
  await insertPayments(db, { groupId: "g2", partyId: "c1", method: "CASH", reference: "", note: "", paidAt: 21, parts: [{ id: "p2", saleLocalId: "s2", amount: "50.00" }] }, 21);
  await restoreBackup(db, text);
  assert.equal(await db.first("SELECT 1 FROM sales WHERE local_id='s2'"), null);
  assert.equal(await getPayment(db, "p2"), null);
  assert.ok(await getPayment(db, "p1"));
  assert.equal(await kvGet(db, "server"), "http://keep.me");
});

test("a bad file changes nothing: not ours, from a newer app, an unknown field, or an unknown table", async () => {
  const db = await populated();
  const before = await dump(db);
  const good = await buildBackup(db);
  const bad: [string, string, RegExp][] = [
    ["not json", "hello", /not a Rechvix backup/],
    ["another app", JSON.stringify({ app: "other", kv: [] }), /not a Rechvix backup/],
    ["newer schema", JSON.stringify({ ...good, schema: 99 }), /newer version/],
    ["unknown field", JSON.stringify({ ...good, tables: { ...good.tables, parties: [{ ...good.tables.parties![0]!, "name) VALUES(1); DROP TABLE sales; --": "x" }] } }), /does not know/],
    ["unknown table", JSON.stringify({ ...good, tables: { ...good.tables, sqlite_master: [] } }), /unknown table/],
    ["missing kv", JSON.stringify({ app: "rechvix-mobile", format: 2, schema: 5, tables: {} }), /damaged/],
  ];
  for (const [name, text, re] of bad) {
    await assert.rejects(restoreBackup(db, text), (e: unknown) => e instanceof BackupError && re.test(e.message), name);
    assert.deepEqual(await dump(db), before, `${name}: the phone is untouched`);
  }
});

test("a failure part-way through rolls the whole restore back", async () => {
  const db = await populated();
  const before = await dump(db);
  const good = await buildBackup(db);
  const broken = JSON.stringify({ ...good, tables: { ...good.tables, sales: [good.tables.sales![0]!, good.tables.sales![0]!] } });   // a duplicate key, hit after products and parties were already replaced
  await assert.rejects(restoreBackup(db, broken));
  assert.deepEqual(await dump(db), before);
});

test("an old-format backup (no payments in it) restores its records and does not wipe the payments on the phone", async () => {
  const db = await populated();
  const b = await buildBackup(db);
  const legacy = JSON.stringify({ app: "rechvix-mobile", exportedAt: "x", kv: b.kv, products: b.tables.products, parties: b.tables.parties, sales: b.tables.sales, id_map: b.tables.id_map });
  assert.equal(inspectBackup(legacy).file.format, 1);
  await restoreBackup(db, legacy);
  assert.ok(await getPayment(db, "p1"), "payments on the phone were left alone");
});

test("an older backup (made before quotations existed) restores into a newer database", async () => {
  const db = await populated();
  const b = await buildBackup(db);
  const { quotes: _q, credit_notes: _c, item_low: _i, ...older } = b.tables;
  void _q; void _c; void _i;
  await restoreBackup(db, JSON.stringify({ ...b, schema: 3, tables: older }));
  assert.equal((await db.all("SELECT * FROM quotes")).length, 0, "tables that did not exist then are empty, as they were");
  assert.ok(await getPayment(db, "p1"));
});
