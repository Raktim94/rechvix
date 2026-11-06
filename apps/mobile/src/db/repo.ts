import type { Db } from "./types";

export interface Org {
  organisationId: string;
  legalEntityId: string;
  legalName: string;
  country: string;
  currency: string;
  gstStateCode: string;
  branchId: string;
  warehouseId: string;
  priceListId: string;
  walkInPartyId: string | null;
}

export interface SaleLine { variantId: string; name: string; unitId: string; qty: string; price: string; discount: string }
export interface SalePayload {
  customerId: string;
  customerName: string;
  lines: SaleLine[];
  /** null = pay later (credit). Otherwise the sale is receipted in full once the server has computed the total. */
  paymentMethod: string | null;
}
export interface Progress { docId?: string; finalized?: boolean; receipted?: boolean }
export type SaleState = "pending" | "syncing" | "synced" | "failed";

export interface SaleRow {
  local_id: string; state: SaleState; payload: string; progress: string; doc_number: string | null; total: string | null;
  error: string | null; attempts: number; created_at: number; synced_at: number | null;
}
export interface Sale extends Omit<SaleRow, "payload" | "progress"> { payload: SalePayload; progress: Progress }
export const parseSale = (r: SaleRow): Sale => ({ ...r, payload: JSON.parse(r.payload) as SalePayload, progress: JSON.parse(r.progress) as Progress });

export interface Product { variant_id: string; product_id: string; name: string; sku: string; hsn: string; unit_id: string; price: string | null; stock: string | null }
export interface Party { id: string; name: string; phone: string; type: string; is_walk_in: number }
export interface ServerDoc { id: string; number: string; doc_type: string; status: string; issue_date: string; customer_id: string; total: string | null }

export const kvGet = async (db: Db, key: string): Promise<string | null> => (await db.first<{ value: string }>("SELECT value FROM kv WHERE key=?", [key]))?.value ?? null;
export const kvSet = (db: Db, key: string, value: string): Promise<void> => db.run("INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", [key, value]);
export const kvDel = (db: Db, key: string): Promise<void> => db.run("DELETE FROM kv WHERE key=?", [key]);

export async function getOrg(db: Db): Promise<Org | null> {
  const v = await kvGet(db, "org");
  return v ? (JSON.parse(v) as Org) : null;
}
export const setOrg = (db: Db, org: Org): Promise<void> => kvSet(db, "org", JSON.stringify(org));

export async function searchProducts(db: Db, q: string, limit = 60): Promise<Product[]> {
  const like = `%${q.trim().replace(/[%_]/g, (c) => `\\${c}`)}%`;
  return db.all<Product>(
    "SELECT * FROM products WHERE name LIKE ? ESCAPE '\\' OR sku LIKE ? ESCAPE '\\' OR hsn LIKE ? ESCAPE '\\' ORDER BY name COLLATE NOCASE LIMIT ?",
    [like, like, like, limit],
  );
}
export const productBySku = (db: Db, code: string): Promise<Product | null> =>
  db.first<Product>("SELECT * FROM products WHERE sku = ? COLLATE NOCASE LIMIT 1", [code.trim()]);

export async function searchParties(db: Db, q: string, limit = 40): Promise<Party[]> {
  const like = `%${q.trim().replace(/[%_]/g, (c) => `\\${c}`)}%`;
  return db.all<Party>("SELECT * FROM parties WHERE name LIKE ? ESCAPE '\\' OR phone LIKE ? ESCAPE '\\' ORDER BY is_walk_in DESC, name COLLATE NOCASE LIMIT ?", [like, like, limit]);
}

export async function insertSale(db: Db, localId: string, payload: SalePayload, now: number): Promise<void> {
  await db.run("INSERT INTO sales(local_id,state,payload,created_at) VALUES(?,?,?,?)", [localId, "pending", JSON.stringify(payload), now]);
}
export async function listSales(db: Db, limit = 200): Promise<Sale[]> {
  return (await db.all<SaleRow>("SELECT * FROM sales ORDER BY created_at DESC LIMIT ?", [limit])).map(parseSale);
}
export async function getSale(db: Db, localId: string): Promise<Sale | null> {
  const r = await db.first<SaleRow>("SELECT * FROM sales WHERE local_id=?", [localId]);
  return r ? parseSale(r) : null;
}
export async function dueSales(db: Db): Promise<Sale[]> {
  return (await db.all<SaleRow>("SELECT * FROM sales WHERE state IN ('pending','syncing') ORDER BY created_at ASC")).map(parseSale);
}
export async function countByState(db: Db): Promise<Record<SaleState, number>> {
  const rows = await db.all<{ state: SaleState; n: number }>("SELECT state, COUNT(*) AS n FROM sales GROUP BY state");
  const out: Record<SaleState, number> = { pending: 0, syncing: 0, synced: 0, failed: 0 };
  for (const r of rows) out[r.state] = r.n;
  return out;
}
export const saveProgress = (db: Db, localId: string, p: Progress): Promise<void> => db.run("UPDATE sales SET progress=? WHERE local_id=?", [JSON.stringify(p), localId]);
export const markState = (db: Db, localId: string, state: SaleState, fields: { error?: string | null; docNumber?: string | null; total?: string | null; attempts?: number; syncedAt?: number | null } = {}): Promise<void> =>
  db.run(
    "UPDATE sales SET state=?, error=COALESCE(?,CASE WHEN ?='synced' THEN NULL ELSE error END), doc_number=COALESCE(?,doc_number), total=COALESCE(?,total), attempts=COALESCE(?,attempts), synced_at=COALESCE(?,synced_at) WHERE local_id=?",
    [state, fields.error ?? null, state, fields.docNumber ?? null, fields.total ?? null, fields.attempts ?? null, fields.syncedAt ?? null, localId],
  );
export const retrySale = (db: Db, localId: string): Promise<void> => db.run("UPDATE sales SET state='pending', error=NULL, attempts=0 WHERE local_id=? AND state='failed'", [localId]);
export const discardSale = (db: Db, localId: string): Promise<void> => db.run("DELETE FROM sales WHERE local_id=? AND state IN ('failed','pending')", [localId]);
/** A crash mid-sync leaves rows in 'syncing'; they are safe to resume because every saga step is resumable. */
export const resetSyncing = (db: Db): Promise<void> => db.run("UPDATE sales SET state='pending' WHERE state='syncing'");

export const recentServerDocs = (db: Db, limit = 60): Promise<ServerDoc[]> => db.all<ServerDoc>("SELECT * FROM server_docs ORDER BY issue_date DESC, number DESC LIMIT ?", [limit]);
