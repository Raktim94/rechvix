import type { Db } from "./types";

/** Versioned by PRAGMA user_version. Add a new entry to MIGRATIONS, never edit a shipped one. */
const MIGRATIONS: string[] = [
  `
  CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);

  -- Catalogue, refreshed on every pull (rows carry the pull's generation; older rows are deleted afterwards, no big transaction). price/stock are decimal strings, exactly as the server sent them.
  CREATE TABLE products (
    variant_id TEXT PRIMARY KEY, product_id TEXT NOT NULL, name TEXT NOT NULL, sku TEXT NOT NULL DEFAULT '',
    hsn TEXT NOT NULL DEFAULT '', unit_id TEXT NOT NULL, price TEXT, stock TEXT, gen INTEGER NOT NULL DEFAULT 0
  );
  CREATE INDEX products_name ON products(name COLLATE NOCASE);
  CREATE INDEX products_sku ON products(sku COLLATE NOCASE);

  CREATE TABLE parties (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, phone TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, is_walk_in INTEGER NOT NULL DEFAULT 0, gen INTEGER NOT NULL DEFAULT 0
  );
  CREATE INDEX parties_name ON parties(name COLLATE NOCASE);

  -- Sales made on this device. Rows are never overwritten by a pull: this table is the outbox.
  -- state: pending (waiting to sync) | syncing | synced | failed (the server refused it; needs a person)
  CREATE TABLE sales (
    local_id TEXT PRIMARY KEY, state TEXT NOT NULL, payload TEXT NOT NULL, progress TEXT NOT NULL DEFAULT '{}',
    doc_number TEXT, total TEXT, error TEXT, attempts INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, synced_at INTEGER
  );
  CREATE INDEX sales_state ON sales(state, created_at);

  -- Recent invoices that exist on the server (made on the web or another phone), for the sales history list.
  CREATE TABLE server_docs (
    id TEXT PRIMARY KEY, number TEXT NOT NULL, doc_type TEXT NOT NULL, status TEXT NOT NULL, issue_date TEXT NOT NULL,
    customer_id TEXT NOT NULL, total TEXT, gen INTEGER NOT NULL DEFAULT 0
  );
  `,
];

export async function migrate(db: Db): Promise<void> {
  const row = await db.first<{ user_version: number }>("PRAGMA user_version");
  let v = row?.user_version ?? 0;
  while (v < MIGRATIONS.length) {
    const sql = MIGRATIONS[v]!;
    await db.tx(async () => {
      await db.exec(sql);
    });
    v += 1;
    await db.exec(`PRAGMA user_version = ${v}`);
  }
}
