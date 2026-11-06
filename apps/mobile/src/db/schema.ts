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
  // v2: standalone mode. The phone can own items and customers (origin='local') and number its own invoices.
  `
  ALTER TABLE products ADD COLUMN origin TEXT NOT NULL DEFAULT 'server';
  ALTER TABLE products ADD COLUMN pushed INTEGER NOT NULL DEFAULT 1;
  ALTER TABLE products ADD COLUMN gst_rate TEXT;
  ALTER TABLE products ADD COLUMN barcode TEXT NOT NULL DEFAULT '';
  ALTER TABLE products ADD COLUMN unit_label TEXT NOT NULL DEFAULT 'pcs';
  ALTER TABLE products ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
  ALTER TABLE products ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;
  CREATE INDEX products_barcode ON products(barcode);

  ALTER TABLE parties ADD COLUMN origin TEXT NOT NULL DEFAULT 'server';
  ALTER TABLE parties ADD COLUMN pushed INTEGER NOT NULL DEFAULT 1;
  ALTER TABLE parties ADD COLUMN gstin TEXT NOT NULL DEFAULT '';
  ALTER TABLE parties ADD COLUMN address TEXT NOT NULL DEFAULT '';
  ALTER TABLE parties ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;

  -- Once a local item/customer reaches the server, its server id is recorded here so unsent sales that still
  -- mention the local id keep working. extra = JSON (item: productId, unitId and which upload steps are done).
  CREATE TABLE id_map (kind TEXT NOT NULL, local_id TEXT NOT NULL, server_id TEXT NOT NULL, extra TEXT NOT NULL DEFAULT '{}', PRIMARY KEY(kind, local_id));

  ALTER TABLE sales ADD COLUMN local_number TEXT;
  `,
  // v3: GST-inclusive pricing, payments and receivables, invoice revisions, reminders, server balances. Purely additive: nothing is
  // dropped or rewritten, so every existing item, customer, sale (including queued ones) and its meaning survive the upgrade.
  `
  -- Items made before this migration were typed "before tax" (price_basis EXCL); items from the server are also pre-tax. New items
  -- made while billing GST-inclusive are INCL. A one-time item is a real catalogue item (so it posts to the server) flagged is_custom.
  ALTER TABLE products ADD COLUMN price_basis TEXT NOT NULL DEFAULT 'EXCL';
  ALTER TABLE products ADD COLUMN is_custom INTEGER NOT NULL DEFAULT 0;
  ALTER TABLE products ADD COLUMN qty_decimals INTEGER;

  -- Every sale queued before this migration keeps pricing_mode EXCLUSIVE, the only meaning it ever had.
  ALTER TABLE sales ADD COLUMN pricing_mode TEXT NOT NULL DEFAULT 'EXCLUSIVE';
  ALTER TABLE sales ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;
  ALTER TABLE sales ADD COLUMN edited_at INTEGER;
  ALTER TABLE sales ADD COLUMN cancelled INTEGER NOT NULL DEFAULT 0;
  ALTER TABLE sales ADD COLUMN due_date INTEGER;
  ALTER TABLE sales ADD COLUMN party_id TEXT;
  UPDATE sales SET party_id = json_extract(payload, '$.customerId');
  CREATE INDEX sales_party ON sales(party_id, created_at);

  -- Money received from customers. One payment event can be split over several invoices (rows share group_id). id doubles as the
  -- idempotency key sent to the server, so a retried post can never book the money twice. Rows are never deleted: a mistake is
  -- reversed (reversed_at / reversal_reason) and the history stays.
  CREATE TABLE payments (
    id TEXT PRIMARY KEY, group_id TEXT NOT NULL, party_id TEXT NOT NULL,
    sale_local_id TEXT, server_doc_id TEXT,
    amount TEXT NOT NULL, method TEXT NOT NULL, reference TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '',
    paid_at INTEGER NOT NULL,
    state TEXT NOT NULL,            -- pending | synced | failed | void (reversed before it ever reached a server)
    server_receipt_id TEXT, error TEXT, attempts INTEGER NOT NULL DEFAULT 0,
    reversed_at INTEGER, reversal_reason TEXT, reversal_state TEXT,   -- reversal_state: NULL | pending | synced | failed
    created_at INTEGER NOT NULL
  );
  CREATE INDEX payments_party ON payments(party_id, paid_at);
  CREATE INDEX payments_sale ON payments(sale_local_id);
  CREATE INDEX payments_state ON payments(state, reversal_state);

  -- Audit trail for edits to an invoice that has not reached the server: every earlier version is kept.
  CREATE TABLE sale_revisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT, sale_local_id TEXT NOT NULL, revision INTEGER NOT NULL,
    edited_at INTEGER NOT NULL, reason TEXT NOT NULL DEFAULT '', snapshot TEXT NOT NULL
  );
  CREATE INDEX sale_revisions_sale ON sale_revisions(sale_local_id, revision);

  -- "A reminder was opened for sharing" (never "was delivered": the phone cannot know that).
  CREATE TABLE reminders (id INTEGER PRIMARY KEY AUTOINCREMENT, party_id TEXT NOT NULL, at INTEGER NOT NULL, channel TEXT NOT NULL);

  -- Organisation-wide receivables from the server (includes invoices made on other devices), refreshed on every pull.
  CREATE TABLE server_balances (
    party_id TEXT PRIMARY KEY, name TEXT NOT NULL, phone TEXT NOT NULL DEFAULT '',
    total TEXT NOT NULL, current_amt TEXT NOT NULL, d30 TEXT NOT NULL, d60 TEXT NOT NULL, d90 TEXT NOT NULL, d90plus TEXT NOT NULL,
    last_reminder_at INTEGER, reminder_count INTEGER NOT NULL DEFAULT 0, gen INTEGER NOT NULL DEFAULT 0
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
