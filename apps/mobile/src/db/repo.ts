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

export interface SaleLine { variantId: string; name: string; unitId: string; qty: string; price: string; discount: string; gstRate?: string | null; /** Customer-facing entered price when the bill was tax-inclusive. */ grossPrice?: string }
export interface PaymentEntry { amount: string; method: string; recordedAt: number; note?: string }
export interface SalePayload {
  customerId: string;
  customerName: string;
  lines: SaleLine[];
  /** null = pay later (credit). Otherwise the sale is receipted in full once the server has computed the total. */
  paymentMethod: string | null;
  /** Additional payments recorded against an unpaid invoice on this phone. */
  payments?: PaymentEntry[];
}
export interface Progress { docId?: string; finalized?: boolean; receipted?: boolean }
export type SaleState = "pending" | "syncing" | "synced" | "failed";

export interface SaleRow {
  local_id: string; state: SaleState; payload: string; progress: string; doc_number: string | null; total: string | null;
  error: string | null; attempts: number; created_at: number; synced_at: number | null; local_number: string | null;
}
export interface Sale extends Omit<SaleRow, "payload" | "progress"> { payload: SalePayload; progress: Progress }
export const parseSale = (r: SaleRow): Sale => ({ ...r, payload: JSON.parse(r.payload) as SalePayload, progress: JSON.parse(r.progress) as Progress });

export interface Product { variant_id: string; product_id: string; name: string; sku: string; hsn: string; unit_id: string; price: string | null; stock: string | null; origin: string; pushed: number; gst_rate: string | null; barcode: string; unit_label: string; archived: number; created_at: number }
export interface Party { id: string; name: string; phone: string; type: string; is_walk_in: number; origin: string; pushed: number; gstin: string; address: string; created_at: number }
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
    "SELECT * FROM products WHERE archived=0 AND (name LIKE ? ESCAPE '\\' OR sku LIKE ? ESCAPE '\\' OR hsn LIKE ? ESCAPE '\\' OR barcode LIKE ? ESCAPE '\\') ORDER BY name COLLATE NOCASE LIMIT ?",
    [like, like, like, like, limit],
  );
}
export const productByCode = (db: Db, code: string): Promise<Product | null> =>
  db.first<Product>("SELECT * FROM products WHERE archived=0 AND (barcode = ? OR sku = ? COLLATE NOCASE) LIMIT 1", [code.trim(), code.trim()]);

export async function searchParties(db: Db, q: string, limit = 40): Promise<Party[]> {
  const like = `%${q.trim().replace(/[%_]/g, (c) => `\\${c}`)}%`;
  return db.all<Party>("SELECT * FROM parties WHERE name LIKE ? ESCAPE '\\' OR phone LIKE ? ESCAPE '\\' ORDER BY is_walk_in DESC, name COLLATE NOCASE LIMIT ?", [like, like, limit]);
}

/** Saves a sale and takes its quantities out of local stock in one step, so the two can never disagree. */
export async function insertSale(db: Db, localId: string, payload: SalePayload, now: number, localNumber: string | null = null): Promise<void> {
  await db.tx(async () => {
    await db.run("INSERT INTO sales(local_id,state,payload,created_at,local_number) VALUES(?,?,?,?,?)", [localId, "pending", JSON.stringify(payload), now, localNumber]);
    for (const l of payload.lines) {
      await db.run("UPDATE products SET stock = CAST(CAST(stock AS REAL) - ? AS TEXT) WHERE variant_id=? AND stock IS NOT NULL", [Number(l.qty), l.variantId]);
    }
  });
}
export async function recordSalePayment(db: Db, localId: string, payment: PaymentEntry): Promise<void> {
  const row = await db.first<{ payload: string }>("SELECT payload FROM sales WHERE local_id=?", [localId]);
  if (!row) throw new Error("Invoice not found.");
  const payload = JSON.parse(row.payload) as SalePayload;
  payload.payments = [...(payload.payments ?? []), payment];
  await db.run("UPDATE sales SET payload=?, state=CASE WHEN state='synced' THEN 'pending' ELSE state END, error=NULL WHERE local_id=?", [JSON.stringify(payload), localId]);
}
export async function updateSaleLines(db: Db, localId: string, lines: SaleLine[]): Promise<void> {
  await db.tx(async () => {
    const row = await db.first<{ payload: string; state: string; progress: string }>("SELECT payload,state,progress FROM sales WHERE local_id=?", [localId]);
    if (!row || row.state === "synced" || (JSON.parse(row.progress) as Progress).docId) throw new Error("Only an invoice that has not reached the server can be edited on this phone.");
    const payload = JSON.parse(row.payload) as SalePayload;
    for (const old of payload.lines) {
      const next = lines.find((l) => l.variantId === old.variantId);
      const diff = Number(old.qty) - Number(next?.qty ?? 0);
      if (diff) await db.run("UPDATE products SET stock=CAST(CAST(stock AS REAL)+? AS TEXT) WHERE variant_id=? AND stock IS NOT NULL", [diff, old.variantId]);
    }
    for (const next of lines) {
      if (!payload.lines.some((l) => l.variantId === next.variantId)) await db.run("UPDATE products SET stock=CAST(CAST(stock AS REAL)-? AS TEXT) WHERE variant_id=? AND stock IS NOT NULL", [Number(next.qty), next.variantId]);
    }
    payload.lines = lines;
    await db.run("UPDATE sales SET payload=?,total=NULL,error=NULL WHERE local_id=?", [JSON.stringify(payload), localId]);
  });
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


// ---------------------------------------------------------------- business profile (standalone)

export interface Profile {
  name: string; phone: string; address: string; gstin: string;
  country: string; currency: string; gstState: string; defaultGst: string; invoicePrefix: string;
  invoiceTheme?: "classic" | "clean" | "bold";
}
export const DEFAULT_PROFILE: Profile = { name: "", phone: "", address: "", gstin: "", country: "IN", currency: "INR", gstState: "", defaultGst: "18", invoicePrefix: "INV-", invoiceTheme: "classic" };
export async function getProfile(db: Db): Promise<Profile | null> {
  const v = await kvGet(db, "profile");
  return v ? { ...DEFAULT_PROFILE, ...(JSON.parse(v) as Partial<Profile>) } : null;
}
export const setProfile = (db: Db, p: Profile): Promise<void> => kvSet(db, "profile", JSON.stringify(p));

/** Gap-free per-phone invoice counter. Only used until the sale reaches a server, which then issues its own number. */
export async function takeInvoiceNumber(db: Db, prefix: string): Promise<string> {
  let n = 0;
  await db.tx(async () => {
    n = Number((await kvGet(db, "inv_counter")) ?? "0") + 1;
    await kvSet(db, "inv_counter", String(n));
  });
  return `${prefix}${String(n).padStart(4, "0")}`;
}

// ---------------------------------------------------------------- items and customers made on the phone

export interface ItemInput { name: string; sku: string; barcode: string; hsn: string; price: string; gstRate: string; stock: string; unitLabel: string }
const slug = (s: string) => s.toUpperCase().replace(/[^A-Z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 24);

export async function createItem(db: Db, id: string, i: ItemInput, now: number): Promise<void> {
  const sku = i.sku.trim() || slug(i.name) || `ITEM-${id.slice(0, 6).toUpperCase()}`;
  await db.run(
    `INSERT INTO products(variant_id,product_id,name,sku,hsn,unit_id,price,stock,gen,origin,pushed,gst_rate,barcode,unit_label,created_at)
     VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    [id, id, i.name.trim(), sku, i.hsn.trim(), "", i.price.trim() || null, i.stock.trim() === "" ? null : i.stock.trim(), 0, "local", 0, i.gstRate === "" ? null : i.gstRate, i.barcode.trim(), i.unitLabel.trim() || "pcs", now],
  );
}
/** Edits a local item. Items that came from a server are read-only here: change them on the web so both sides agree. */
export const updateItem = (db: Db, id: string, i: ItemInput): Promise<void> =>
  db.run(
    "UPDATE products SET name=?, sku=?, barcode=?, hsn=?, price=?, gst_rate=?, stock=?, unit_label=? WHERE variant_id=? AND origin='local' AND pushed=0",
    [i.name.trim(), i.sku.trim(), i.barcode.trim(), i.hsn.trim(), i.price.trim() || null, i.gstRate === "" ? null : i.gstRate, i.stock.trim() === "" ? null : i.stock.trim(), i.unitLabel.trim() || "pcs", id],
  );
export const archiveItem = (db: Db, id: string): Promise<void> => db.run("UPDATE products SET archived=1 WHERE variant_id=? AND origin='local' AND pushed=0", [id]);
export const getItem = (db: Db, id: string): Promise<Product | null> => db.first<Product>("SELECT * FROM products WHERE variant_id=?", [id]);

export interface PartyInput { name: string; phone: string; gstin: string; address: string }
export const createParty = (db: Db, id: string, p: PartyInput, now: number, walkIn = false): Promise<void> =>
  db.run("INSERT INTO parties(id,name,phone,type,is_walk_in,gen,origin,pushed,gstin,address,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
    [id, p.name.trim(), p.phone.trim(), "CUSTOMER", walkIn ? 1 : 0, 0, "local", 0, p.gstin.trim().toUpperCase(), p.address.trim(), now]);
export const updateParty = (db: Db, id: string, p: PartyInput): Promise<void> =>
  db.run("UPDATE parties SET name=?, phone=?, gstin=?, address=? WHERE id=? AND origin='local' AND pushed=0", [p.name.trim(), p.phone.trim(), p.gstin.trim().toUpperCase(), p.address.trim(), id]);
export const getParty = (db: Db, id: string): Promise<Party | null> => db.first<Party>("SELECT * FROM parties WHERE id=?", [id]);

// ---------------------------------------------------------------- local -> server id mapping

export interface ItemMapExtra { productId: string; unitId: string; stockDone?: boolean; priceDone?: boolean; barcodeDone?: boolean; taxDone?: boolean }
export const mapSet = (db: Db, kind: "item" | "party", localId: string, serverId: string, extra: object = {}): Promise<void> =>
  db.run("INSERT INTO id_map(kind,local_id,server_id,extra) VALUES(?,?,?,?) ON CONFLICT(kind,local_id) DO UPDATE SET server_id=excluded.server_id, extra=excluded.extra", [kind, localId, serverId, JSON.stringify(extra)]);
export async function mapGet(db: Db, kind: "item" | "party", localId: string): Promise<{ serverId: string; extra: ItemMapExtra } | null> {
  const r = await db.first<{ server_id: string; extra: string }>("SELECT server_id, extra FROM id_map WHERE kind=? AND local_id=?", [kind, localId]);
  return r ? { serverId: r.server_id, extra: JSON.parse(r.extra) as ItemMapExtra } : null;
}
export const unpushedParties = (db: Db): Promise<Party[]> => db.all<Party>("SELECT * FROM parties WHERE origin='local' AND pushed=0 ORDER BY created_at");
export const unpushedItems = (db: Db): Promise<Product[]> => db.all<Product>("SELECT * FROM products WHERE origin='local' AND pushed=0 AND archived=0 ORDER BY created_at");
export const markPartyPushed = (db: Db, id: string): Promise<void> => db.run("UPDATE parties SET pushed=1 WHERE id=?", [id]);
export const markItemPushed = (db: Db, id: string): Promise<void> => db.run("UPDATE products SET pushed=1 WHERE variant_id=?", [id]);

/** Pending sales have already left stock on the phone but the server doesn't know yet: after a pull replaces stock with the
 *  server's figure, take those quantities off again so the screen never shows stock that is already sold. */
export async function reapplyPendingStock(db: Db): Promise<void> {
  for (const sale of await dueSales(db)) {
    for (const l of sale.payload.lines) {
      const m = await mapGet(db, "item", l.variantId);
      // Only server rows: a pull overwrote those. Items made on the phone were already reduced when the sale was saved.
      await db.run("UPDATE products SET stock = CAST(CAST(stock AS REAL) - ? AS TEXT) WHERE variant_id=? AND origin='server' AND stock IS NOT NULL", [Number(l.qty), m?.serverId ?? l.variantId]);
    }
  }
}

export interface Insights { todayTotal: number; todayCount: number; weekTotal: number; unpaid: number; unpaidCount: number; lowStock: Product[] }
