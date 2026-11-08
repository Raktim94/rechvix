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

export interface SaleLine {
  variantId: string; name: string; unitId: string; qty: string;
  /** The unit price exactly as entered. In INCLUSIVE mode it already contains GST. */
  price: string; discount: string; gstRate?: string | null;
  unitLabel?: string; hsn?: string;
  /** Credit notes: index of this line on the original invoice, so a later return knows how much is already credited. */
  lineIndex?: number;
  /** A one-time item made while billing (still a real catalogue item underneath, so it posts to the server like any other). */
  custom?: boolean;
}
export interface SalePayload {
  customerId: string;
  customerName: string;
  lines: SaleLine[];
  /** Legacy: money method for "paid in full at sync" (sales made before payments were recorded separately). null = pay later. */
  paymentMethod: string | null;
  /** How the entered prices relate to GST. ABSENT means EXCLUSIVE: every sale queued before this field existed was priced that way
   *  and must keep that meaning after an upgrade. */
  pricingMode?: "INCLUSIVE" | "EXCLUSIVE";
  /** GST state codes at the time of sale; used for the CGST+SGST vs IGST split. Absent/unknown = same state (never guessed). */
  sellerState?: string | null;
  customerState?: string | null;
  /** The figures as issued (tax split, totals). Invoices are drawn from this, never recomputed, so they cannot drift. */
  calc?: import("../lib/tax").CalcSnapshot;
  dueDate?: number | null;
  note?: string;
  /** Number of the cancelled invoice this one re-issues (an audited correction). */
  corrects?: string;
  /** Credit notes: true when the goods came back (stock goes up again), false for a money-only credit. */
  goodsReturned?: boolean;
}
export interface Progress { docId?: string; finalized?: boolean; receipted?: boolean }
export type SaleState = "pending" | "syncing" | "synced" | "failed";

export interface SaleRow {
  local_id: string; state: SaleState; payload: string; progress: string; doc_number: string | null; total: string | null;
  error: string | null; attempts: number; created_at: number; synced_at: number | null; local_number: string | null;
  pricing_mode: "INCLUSIVE" | "EXCLUSIVE"; revision: number; edited_at: number | null; cancelled: number; due_date: number | null; party_id: string | null;
}
export interface Sale extends Omit<SaleRow, "payload" | "progress"> { payload: SalePayload; progress: Progress }
export const parseSale = (r: SaleRow): Sale => ({ ...r, payload: JSON.parse(r.payload) as SalePayload, progress: JSON.parse(r.progress) as Progress });

export interface Product { variant_id: string; product_id: string; name: string; sku: string; hsn: string; unit_id: string; price: string | null; stock: string | null; origin: string; pushed: number; gst_rate: string | null; barcode: string; unit_label: string; archived: number; created_at: number;
  /** INCL: the stored price is GST-inclusive (made on this phone while billing GST-inclusive). EXCL: pre-tax (server items, and anything made before this existed). */
  price_basis: "INCL" | "EXCL"; is_custom: number; qty_decimals: number | null }
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
    "SELECT * FROM products WHERE archived=0 AND is_custom=0 AND (name LIKE ? ESCAPE '\\' OR sku LIKE ? ESCAPE '\\' OR hsn LIKE ? ESCAPE '\\' OR barcode LIKE ? ESCAPE '\\') ORDER BY name COLLATE NOCASE LIMIT ?",
    [like, like, like, like, limit],
  );
}
export const productByCode = (db: Db, code: string): Promise<Product | null> =>
  db.first<Product>("SELECT * FROM products WHERE archived=0 AND (barcode = ? OR sku = ? COLLATE NOCASE) LIMIT 1", [code.trim(), code.trim()]);

export async function searchParties(db: Db, q: string, limit = 40): Promise<Party[]> {
  const like = `%${q.trim().replace(/[%_]/g, (c) => `\\${c}`)}%`;
  return db.all<Party>("SELECT * FROM parties WHERE name LIKE ? ESCAPE '\\' OR phone LIKE ? ESCAPE '\\' ORDER BY is_walk_in DESC, name COLLATE NOCASE LIMIT ?", [like, like, limit]);
}


/** Adds `delta` (negative to take stock out) to an item's tracked stock, keeping it a clean decimal string: "98", never "98.0".
 *  Items whose stock isn't tracked (NULL) are left alone. */
export const adjustStock = (db: Db, variantId: string, delta: number, serverOnly = false): Promise<void> =>
  db.run(
    `UPDATE products SET stock = (SELECT CASE WHEN r = CAST(r AS INTEGER) THEN CAST(CAST(r AS INTEGER) AS TEXT) ELSE CAST(r AS TEXT) END
                                  FROM (SELECT ROUND(CAST(products.stock AS REAL) + ?, 4) AS r))
     WHERE variant_id = ? AND stock IS NOT NULL${serverOnly ? " AND origin = 'server'" : ""}`,
    [delta, variantId],
  );

/** Saves a sale and takes its quantities out of local stock in one step, so the two can never disagree. */
export async function insertSale(db: Db, localId: string, payload: SalePayload, now: number, localNumber: string | null = null): Promise<void> {
  await db.tx(async () => {
    await db.run("INSERT INTO sales(local_id,state,payload,created_at,local_number,pricing_mode,party_id,due_date) VALUES(?,?,?,?,?,?,?,?)",
      [localId, "pending", JSON.stringify(payload), now, localNumber, payload.pricingMode ?? "EXCLUSIVE", payload.customerId, payload.dueDate ?? null]);
    for (const l of payload.lines) {
      await adjustStock(db, l.variantId, -Number(l.qty));
    }
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
  return (await db.all<SaleRow>("SELECT * FROM sales WHERE state IN ('pending','syncing') AND cancelled=0 ORDER BY created_at ASC")).map(parseSale);
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
  /** How prices are entered while billing. INCLUSIVE ("Price includes GST") is the default: the seller types the final price. */
  pricingMode: "INCLUSIVE" | "EXCLUSIVE";
  /** The look of invoices and shared PDFs. Presentation only. */
  invoiceTemplate: "classic" | "modern" | "thermal";
}
export const DEFAULT_PROFILE: Profile = { name: "", phone: "", address: "", gstin: "", country: "IN", currency: "INR", gstState: "", defaultGst: "18", invoicePrefix: "INV-", pricingMode: "INCLUSIVE", invoiceTemplate: "classic" };
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

export interface ItemInput { name: string; sku: string; barcode: string; hsn: string; price: string; gstRate: string; stock: string; unitLabel: string; priceBasis?: "INCL" | "EXCL"; isCustom?: boolean; qtyDecimals?: number | null }
const slug = (s: string) => s.toUpperCase().replace(/[^A-Z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 24);

export async function createItem(db: Db, id: string, i: ItemInput, now: number): Promise<void> {
  const sku = i.sku.trim() || slug(i.name) || `ITEM-${id.slice(0, 6).toUpperCase()}`;
  await db.run(
    `INSERT INTO products(variant_id,product_id,name,sku,hsn,unit_id,price,stock,gen,origin,pushed,gst_rate,barcode,unit_label,created_at,price_basis,is_custom,qty_decimals)
     VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    [id, id, i.name.trim(), sku, i.hsn.trim(), "", i.price.trim() || null, i.stock.trim() === "" ? null : i.stock.trim(), 0, "local", 0, i.gstRate === "" ? null : i.gstRate, i.barcode.trim(), i.unitLabel.trim() || "pcs", now, i.priceBasis ?? "EXCL", i.isCustom ? 1 : 0, i.qtyDecimals ?? null],
  );
}
/** Edits a local item. Items that came from a server are read-only here: change them on the web so both sides agree. */
export const updateItem = (db: Db, id: string, i: ItemInput): Promise<void> =>
  db.run(
    "UPDATE products SET name=?, sku=?, barcode=?, hsn=?, price=?, gst_rate=?, stock=?, unit_label=?, price_basis=COALESCE(?,price_basis), qty_decimals=? WHERE variant_id=? AND origin='local' AND pushed=0",
    [i.name.trim(), i.sku.trim(), i.barcode.trim(), i.hsn.trim(), i.price.trim() || null, i.gstRate === "" ? null : i.gstRate, i.stock.trim() === "" ? null : i.stock.trim(), i.unitLabel.trim() || "pcs", i.priceBasis ?? null, i.qtyDecimals ?? null, id],
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
      await adjustStock(db, m?.serverId ?? l.variantId, -Number(l.qty), true);
    }
  }
}

export interface Insights { todayTotal: number; todayCount: number; weekTotal: number; unpaid: number; unpaidCount: number; lowStock: Product[] }


// ---------------------------------------------------------------- payments received from customers

export type PaymentState = "pending" | "synced" | "failed" | "void";
export interface Payment {
  id: string; group_id: string; party_id: string; sale_local_id: string | null; server_doc_id: string | null;
  amount: string; method: string; reference: string; note: string; paid_at: number;
  state: PaymentState; server_receipt_id: string | null; error: string | null; attempts: number;
  reversed_at: number | null; reversal_reason: string | null; reversal_state: null | "pending" | "synced" | "failed"; created_at: number;
}
export interface PaymentInput {
  groupId: string; partyId: string; method: string; reference: string; note: string; paidAt: number;
  /** One entry per invoice the money goes to; `saleLocalId` for an invoice made on this phone, `serverDocId` for one made elsewhere. */
  parts: { id: string; saleLocalId?: string | null; serverDocId?: string | null; amount: string }[];
}

/** Saves a payment. Every part is its own row (a server receipt covers one invoice), all sharing the group id. A part with neither
 *  invoice is customer credit: money received that is not tied to an invoice yet. */
export async function insertPayments(db: Db, p: PaymentInput, now: number): Promise<void> {
  await db.tx(async () => {
    for (const part of p.parts) {
      await db.run(
        `INSERT INTO payments(id,group_id,party_id,sale_local_id,server_doc_id,amount,method,reference,note,paid_at,state,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
        [part.id, p.groupId, p.partyId, part.saleLocalId ?? null, part.serverDocId ?? null, part.amount, p.method, p.reference, p.note, p.paidAt, "pending", now],
      );
    }
  });
}
export const paymentsForSale = (db: Db, saleLocalId: string): Promise<Payment[]> => db.all<Payment>("SELECT * FROM payments WHERE sale_local_id=? ORDER BY paid_at, created_at", [saleLocalId]);
export const paymentsForParty = (db: Db, partyId: string): Promise<Payment[]> => db.all<Payment>("SELECT * FROM payments WHERE party_id=? ORDER BY paid_at, created_at", [partyId]);
export const allPayments = (db: Db): Promise<Payment[]> => db.all<Payment>("SELECT * FROM payments ORDER BY paid_at, created_at");
export const getPayment = (db: Db, id: string): Promise<Payment | null> => db.first<Payment>("SELECT * FROM payments WHERE id=?", [id]);
export const unallocatedPayments = (db: Db, partyId?: string): Promise<Payment[]> =>
  partyId ? db.all<Payment>("SELECT * FROM payments WHERE sale_local_id IS NULL AND server_doc_id IS NULL AND party_id=?", [partyId])
          : db.all<Payment>("SELECT * FROM payments WHERE sale_local_id IS NULL AND server_doc_id IS NULL");
export function groupPaymentsBySale(ps: Payment[]): Map<string, Payment[]> {
  const m = new Map<string, Payment[]>();
  for (const p of ps) if (p.sale_local_id) m.set(p.sale_local_id, [...(m.get(p.sale_local_id) ?? []), p]);
  return m;
}

/** Corrects a payment entered by mistake WITHOUT erasing it. A payment that never reached the server is simply voided; one the server
 *  already booked is marked for reversal there (an audited reversing entry), and stays visible with who/when/why. */
export async function reversePayment(db: Db, id: string, reason: string, now: number): Promise<void> {
  const r = reason.trim();
  if (!r) throw new Error("Say why this payment is being reversed.");
  await db.tx(async () => {
    const p = await getPayment(db, id);
    if (!p) throw new Error("Payment not found.");
    if (p.reversed_at != null) throw new Error("This payment has already been reversed.");
    if (p.state === "synced") await db.run("UPDATE payments SET reversed_at=?, reversal_reason=?, reversal_state='pending' WHERE id=?", [now, r, id]);
    else await db.run("UPDATE payments SET reversed_at=?, reversal_reason=?, state='void' WHERE id=?", [now, r, id]);
  });
}
export const markPaymentPosted = (db: Db, id: string, serverReceiptId: string | null): Promise<void> =>
  db.run("UPDATE payments SET state='synced', server_receipt_id=COALESCE(?,server_receipt_id), error=NULL WHERE id=? AND reversed_at IS NULL", [serverReceiptId, id]);
export const markPaymentFailed = (db: Db, id: string, error: string, attempts: number): Promise<void> => db.run("UPDATE payments SET state='failed', error=?, attempts=? WHERE id=?", [error, attempts, id]);
export const markPaymentRetry = (db: Db, id: string): Promise<void> => db.run("UPDATE payments SET state='pending', error=NULL, attempts=0 WHERE id=? AND state='failed'", [id]);
export const markReversalDone = (db: Db, id: string): Promise<void> => db.run("UPDATE payments SET reversal_state='synced' WHERE id=?", [id]);
export const markReversalFailed = (db: Db, id: string): Promise<void> => db.run("UPDATE payments SET reversal_state='failed' WHERE id=?", [id]);
export const pendingPaymentWork = async (db: Db): Promise<{ post: Payment[]; reverse: Payment[] }> => ({
  post: await db.all<Payment>("SELECT * FROM payments WHERE state='pending' AND reversed_at IS NULL ORDER BY paid_at, created_at"),
  reverse: await db.all<Payment>("SELECT * FROM payments WHERE reversal_state='pending' AND server_receipt_id IS NOT NULL ORDER BY reversed_at"),
});

// ---------------------------------------------------------------- editing an invoice that has not reached the server

/** Only an invoice the server has never seen can be edited in place: nothing of it exists in anyone's ledger yet. A synced invoice is
 *  corrected through the server's own audited cancel-and-reissue instead (see the correction workflow). */
export const canEditSale = (s: Pick<Sale, "state" | "progress" | "cancelled">): boolean =>
  (s.state === "pending" || s.state === "failed") && !s.progress.docId && !s.cancelled;

export interface SaleRevision { id: number; sale_local_id: string; revision: number; edited_at: number; reason: string; snapshot: string }
export const saleRevisions = (db: Db, saleLocalId: string): Promise<SaleRevision[]> => db.all<SaleRevision>("SELECT * FROM sale_revisions WHERE sale_local_id=? ORDER BY revision", [saleLocalId]);

/** Replaces an unsynced invoice's content. The previous version is kept (sale_revisions), stock is put back for the old lines and taken
 *  for the new ones, and it goes back in the queue. `totalMinor` is the new total, used to refuse an edit that would leave more money
 *  recorded against the invoice than it is now worth. */
export async function updateSaleContent(db: Db, localId: string, payload: SalePayload, reason: string, now: number, totalMinor: bigint): Promise<void> {
  await db.tx(async () => {
    const row = await db.first<SaleRow>("SELECT * FROM sales WHERE local_id=?", [localId]);
    if (!row) throw new Error("Invoice not found.");
    const sale = parseSale(row);
    if (!canEditSale(sale)) throw new Error("This invoice has reached the server, so it can't be edited in place. Use Correct invoice.");
    const paid = (await paymentsForSale(db, localId)).filter((p) => p.reversed_at == null && p.state !== "void").reduce((n, p) => n + toMinorStr(p.amount), 0n);
    if (paid > totalMinor) throw new Error("More money is recorded against this invoice than its new total. Reverse a payment first.");
    await db.run("INSERT INTO sale_revisions(sale_local_id,revision,edited_at,reason,snapshot) VALUES(?,?,?,?,?)", [localId, sale.revision, now, reason.trim(), row.payload]);
    for (const l of sale.payload.lines) await adjustStock(db, l.variantId, Number(l.qty));
    for (const l of payload.lines) await adjustStock(db, l.variantId, -Number(l.qty));
    await db.run("UPDATE sales SET payload=?, pricing_mode=?, party_id=?, due_date=?, revision=revision+1, edited_at=?, state='pending', error=NULL, attempts=0 WHERE local_id=?",
      [JSON.stringify(payload), payload.pricingMode ?? "EXCLUSIVE", payload.customerId, payload.dueDate ?? null, now, localId]);
  });
}
const toMinorStr = (s: string): bigint => {
  const [w = "0", f = ""] = s.split(".");
  return BigInt(w || "0") * 100n + BigInt((f + "00").slice(0, 2));
};

/** Marks a sale cancelled on this phone (after the server confirmed its own cancellation, or for a sale that never left the phone). */
export const markSaleCancelled = (db: Db, localId: string): Promise<void> => db.run("UPDATE sales SET cancelled=1 WHERE local_id=?", [localId]);

// ---------------------------------------------------------------- reminders and server balances

export const addReminder = (db: Db, partyId: string, at: number, channel: string): Promise<void> => db.run("INSERT INTO reminders(party_id,at,channel) VALUES(?,?,?)", [partyId, at, channel]);
export const remindersFor = (db: Db, partyId: string): Promise<{ at: number; channel: string }[]> => db.all("SELECT at, channel FROM reminders WHERE party_id=? ORDER BY at DESC", [partyId]);

export interface ServerBalance { party_id: string; name: string; phone: string; total: string; current_amt: string; d30: string; d60: string; d90: string; d90plus: string; last_reminder_at: number | null; reminder_count: number }
export const serverBalances = (db: Db): Promise<ServerBalance[]> => db.all<ServerBalance>("SELECT * FROM server_balances ORDER BY CAST(total AS REAL) DESC");


// ---------------------------------------------------------------- recent / frequent items (for fast billing)
export async function frequentItems(db: Db, limit = 8): Promise<Product[]> {
  return db.all<Product>(
    `SELECT p.* FROM products p JOIN (
       SELECT json_extract(l.value,'$.variantId') AS v, COUNT(*) AS c
       FROM sales s, json_each(s.payload,'$.lines') l WHERE s.cancelled=0 GROUP BY v
     ) f ON f.v = p.variant_id WHERE p.archived=0 AND p.is_custom=0 ORDER BY f.c DESC LIMIT ?`, [limit]);
}
export async function recentCustomers(db: Db, limit = 6): Promise<Party[]> {
  return db.all<Party>(
    `SELECT p.* FROM parties p JOIN (SELECT party_id AS id, MAX(created_at) AS t FROM sales WHERE party_id IS NOT NULL GROUP BY party_id) r ON r.id = p.id
     WHERE p.is_walk_in=0 ORDER BY r.t DESC LIMIT ?`, [limit]);
}

/** Cancels an invoice the server has never seen: it will never be sent, its stock goes back and any payments recorded against it are voided. */
export async function cancelLocalSale(db: Db, localId: string, reason: string, now: number): Promise<void> {
  if (!reason.trim()) throw new Error("Say why this invoice is being cancelled.");
  await db.tx(async () => {
    const row = await db.first<SaleRow>("SELECT * FROM sales WHERE local_id=?", [localId]);
    if (!row) throw new Error("Invoice not found.");
    const sale = parseSale(row);
    if (sale.cancelled) return;
    if (sale.progress.docId) throw new Error("This invoice has reached the server. Use Correct invoice, which cancels it there as well.");
    for (const l of sale.payload.lines) await adjustStock(db, l.variantId, Number(l.qty));
    await db.run("UPDATE payments SET reversed_at=?, reversal_reason=?, state='void' WHERE sale_local_id=? AND reversed_at IS NULL AND state<>'synced'", [now, reason.trim(), localId]);
    await db.run("UPDATE sales SET cancelled=1, error=NULL WHERE local_id=?", [localId]);
  });
}


// ---------------------------------------------------------------- quotations (kept on the phone; no stock or balance effect)

export type QuoteStatus = "open" | "converted" | "declined";
export interface QuoteRow { local_id: string; number: string; party_id: string; payload: string; total: string; created_at: number; valid_until: number | null; status: QuoteStatus; sale_local_id: string | null }
export interface Quote extends Omit<QuoteRow, "payload"> { payload: SalePayload }
const parseQuote = (r: QuoteRow): Quote => ({ ...r, payload: JSON.parse(r.payload) as SalePayload });
export async function takeQuoteNumber(db: Db): Promise<string> {
  let n = 0;
  await db.tx(async () => { n = Number((await kvGet(db, "quote_counter")) ?? "0") + 1; await kvSet(db, "quote_counter", String(n)); });
  return `QT-${String(n).padStart(4, "0")}`;
}
export async function insertQuote(db: Db, localId: string, number: string, payload: SalePayload, totalMinor: string, now: number, validUntil: number | null): Promise<void> {
  await db.run("INSERT OR IGNORE INTO quotes(local_id,number,party_id,payload,total,created_at,valid_until) VALUES(?,?,?,?,?,?,?)",
    [localId, number, payload.customerId, JSON.stringify(payload), totalMinor, now, validUntil]);
}
export const listQuotes = async (db: Db): Promise<Quote[]> => (await db.all<QuoteRow>("SELECT * FROM quotes ORDER BY created_at DESC LIMIT 300")).map(parseQuote);
export const getQuote = async (db: Db, id: string): Promise<Quote | null> => { const r = await db.first<QuoteRow>("SELECT * FROM quotes WHERE local_id=?", [id]); return r ? parseQuote(r) : null; };
export const setQuoteStatus = (db: Db, id: string, status: QuoteStatus, saleLocalId: string | null = null): Promise<void> =>
  db.run("UPDATE quotes SET status=?, sale_local_id=COALESCE(?,sale_local_id) WHERE local_id=?", [status, saleLocalId, id]);

// ---------------------------------------------------------------- credit notes (returns against a synced invoice)

export interface CreditNoteRow { local_id: string; sale_local_id: string; server_doc_id: string | null; number: string | null; payload: string; total: string | null; reason: string; created_at: number; state: "pending" | "issued" }
export interface CreditNote extends Omit<CreditNoteRow, "payload"> { payload: SalePayload }
const parseCredit = (r: CreditNoteRow): CreditNote => ({ ...r, payload: JSON.parse(r.payload) as SalePayload });
export async function insertCreditNote(db: Db, localId: string, saleLocalId: string, payload: SalePayload, reason: string, now: number): Promise<void> {
  await db.run("INSERT OR IGNORE INTO credit_notes(local_id,sale_local_id,payload,reason,created_at) VALUES(?,?,?,?,?)", [localId, saleLocalId, JSON.stringify(payload), reason, now]);
}
export const setCreditDraft = (db: Db, id: string, serverDocId: string): Promise<void> => db.run("UPDATE credit_notes SET server_doc_id=? WHERE local_id=?", [serverDocId, id]);
export const setCreditIssued = (db: Db, id: string, number: string, total: string): Promise<void> => db.run("UPDATE credit_notes SET state='issued', number=?, total=? WHERE local_id=?", [number, total, id]);
export const creditNotesForSale = async (db: Db, saleLocalId: string): Promise<CreditNote[]> =>
  (await db.all<CreditNoteRow>("SELECT * FROM credit_notes WHERE sale_local_id=? ORDER BY created_at", [saleLocalId])).map(parseCredit);
export const allCreditNotes = async (db: Db): Promise<CreditNote[]> => (await db.all<CreditNoteRow>("SELECT * FROM credit_notes WHERE state='issued'")).map(parseCredit);
export const getCreditNote = async (db: Db, id: string): Promise<CreditNote | null> => { const r = await db.first<CreditNoteRow>("SELECT * FROM credit_notes WHERE local_id=?", [id]); return r ? parseCredit(r) : null; };
