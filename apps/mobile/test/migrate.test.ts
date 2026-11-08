import assert from "node:assert/strict";
import { test } from "node:test";
import { migrate } from "../src/db/schema";
import { memoryDb } from "./nodedb";

test("the v3 migration really upgrades a v2 database in place (data kept, party_id backfilled)", async () => {
  const db = memoryDb();
  // Hand-build the v2 schema's relevant tables with data, set user_version = 2, then run migrate: it must apply only v3.
  await db.exec(`
    CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
    CREATE TABLE products (variant_id TEXT PRIMARY KEY, product_id TEXT NOT NULL, name TEXT NOT NULL, sku TEXT NOT NULL DEFAULT '', hsn TEXT NOT NULL DEFAULT '', unit_id TEXT NOT NULL, price TEXT, stock TEXT, gen INTEGER NOT NULL DEFAULT 0,
      origin TEXT NOT NULL DEFAULT 'server', pushed INTEGER NOT NULL DEFAULT 1, gst_rate TEXT, barcode TEXT NOT NULL DEFAULT '', unit_label TEXT NOT NULL DEFAULT 'pcs', archived INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL DEFAULT 0);
    CREATE TABLE parties (id TEXT PRIMARY KEY, name TEXT NOT NULL, phone TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, is_walk_in INTEGER NOT NULL DEFAULT 0, gen INTEGER NOT NULL DEFAULT 0, origin TEXT NOT NULL DEFAULT 'server', pushed INTEGER NOT NULL DEFAULT 1, gstin TEXT NOT NULL DEFAULT '', address TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL DEFAULT 0);
    CREATE TABLE sales (local_id TEXT PRIMARY KEY, state TEXT NOT NULL, payload TEXT NOT NULL, progress TEXT NOT NULL DEFAULT '{}', doc_number TEXT, total TEXT, error TEXT, attempts INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, synced_at INTEGER, local_number TEXT);
    CREATE TABLE server_docs (id TEXT PRIMARY KEY, number TEXT NOT NULL, doc_type TEXT NOT NULL, status TEXT NOT NULL, issue_date TEXT NOT NULL, customer_id TEXT NOT NULL, total TEXT, gen INTEGER NOT NULL DEFAULT 0);
    CREATE TABLE id_map (kind TEXT NOT NULL, local_id TEXT NOT NULL, server_id TEXT NOT NULL, extra TEXT NOT NULL DEFAULT '{}', PRIMARY KEY(kind, local_id));
    PRAGMA user_version = 2;`);
  await db.run("INSERT INTO products(variant_id,product_id,name,unit_id,price,stock,origin,pushed,gst_rate,created_at) VALUES('i1','i1','Steel Bolt','', '25','200','local',0,'18',5)");
  await db.run("INSERT INTO parties(id,name,type,origin,pushed) VALUES('c1','Gupta','CUSTOMER','local',0)");
  const payload = JSON.stringify({ customerId: "c1", customerName: "Gupta", paymentMethod: null, lines: [{ variantId: "i1", name: "Steel Bolt", unitId: "", qty: "4", price: "25", discount: "0", gstRate: "18" }] });
  await db.run("INSERT INTO sales(local_id,state,payload,progress,created_at,local_number) VALUES('s1','pending',?,'{\"docId\":\"d9\"}',10,'INV-0001')", [payload]);
  await db.run("INSERT INTO sales(local_id,state,payload,created_at,doc_number,total) VALUES('s2','synced',?,11,'INV/26-27/000004','1711.00')", [payload]);

  await migrate(db);   // applies v3 only

  assert.equal((await db.first<{ user_version: number }>("PRAGMA user_version"))!.user_version, 4);
  assert.equal((await db.first<{ n: number }>("SELECT COUNT(*) AS n FROM sales"))!.n, 2, "no sale lost");
  const s1 = (await db.first<{ progress: string; state: string; pricing_mode: string; party_id: string; payload: string }>("SELECT * FROM sales WHERE local_id='s1'"))!;
  assert.equal(s1.state, "pending"); assert.equal(s1.progress, '{"docId":"d9"}', "sync progress (retry safety) untouched");
  assert.equal(s1.pricing_mode, "EXCLUSIVE", "legacy sale keeps EXCLUSIVE semantics"); assert.equal(s1.party_id, "c1", "party_id backfilled from the payload");
  assert.equal(s1.payload, payload, "payload byte-for-byte unchanged");
  const s2 = (await db.first<{ total: string; doc_number: string }>("SELECT total, doc_number FROM sales WHERE local_id='s2'"))!;
  assert.equal(s2.total, "1711.00"); assert.equal(s2.doc_number, "INV/26-27/000004");
  const it = (await db.first<{ price: string; price_basis: string; stock: string; is_custom: number }>("SELECT * FROM products WHERE variant_id='i1'"))!;
  assert.equal(it.price, "25"); assert.equal(it.stock, "200"); assert.equal(it.price_basis, "EXCL", "items typed before tax stay pre-tax"); assert.equal(it.is_custom, 0);
  for (const t of ["payments", "sale_revisions", "reminders", "server_balances"]) assert.ok(await db.first(`SELECT 1 FROM ${t} LIMIT 1`).then(() => true), `${t} exists`);
  await migrate(db);   // idempotent
  assert.equal((await db.first<{ user_version: number }>("PRAGMA user_version"))!.user_version, 4);
});
