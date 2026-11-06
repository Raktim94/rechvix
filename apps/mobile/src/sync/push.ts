import type { Api } from "../api/client";
import { ApiError } from "../api/client";
import {
  dueSales, kvDel, kvGet, kvSet, mapGet, mapSet, markItemPushed, markPartyPushed, unpushedItems, unpushedParties, type ItemMapExtra, type Org, type Party, type Product,
} from "../db/repo";
import { num } from "../lib/money";
import type { Db } from "../db/types";

/** Uploads what was created on the phone: customers first, then items. Each is matched against what the server already has
 *  (customer by exact name, item by SKU) so connecting a phone to a business that already has data merges instead of duplicating.
 *  Item upload has several steps; progress is recorded after each so a retry never posts opening stock or a price twice. */

interface PartyRow { ID: string; LegalName: string; PartyType: string }
interface Unit { ID: string; Code: string }
interface LookupRow { ProductID: string; ProductVariantID: string; SKUCode: string; QuantityOnHand?: string }

/** A 4xx for one customer or item (a duplicate phone, a bad value) is that record's problem, not the whole sync's. It is reported,
 *  the record is retried next time, and sales that don't need it carry on. Network and auth errors still stop everything. */
export class PushProblems extends Error {
  constructor(readonly problems: string[]) { super(problems[0] ?? "Some items couldn't be uploaded."); }
}
const isRecordProblem = (e: unknown): e is ApiError => e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 401 && e.status !== 408 && e.status !== 429;

export async function pushParties(api: Api, db: Db, org: Org): Promise<{ done: number; problems: string[] }> {
  let n = 0;
  const problems: string[] = [];
  for (const p of await unpushedParties(db)) {
    try {
      await pushParty(api, db, org, p);
      n++;
    } catch (e) {
      if (!isRecordProblem(e)) throw e;
      problems.push(`Customer "${p.name}": ${e.message}`);
    }
  }
  return { done: n, problems };
}

async function pushParty(api: Api, db: Db, org: Org, p: Party): Promise<void> {
  {
    if (await mapGet(db, "party", p.id)) { await markPartyPushed(db, p.id); return; }
    const found = (await api.get<{ parties: PartyRow[] | null }>(`/contacts/parties?q=${encodeURIComponent(p.name)}`)).parties ?? [];
    let server = found.find((x) => x.LegalName.trim().toLowerCase() === p.name.trim().toLowerCase() && x.PartyType !== "SUPPLIER");
    if (!server && p.phone) {
      // The server allows one party per phone number. Same number = same person: reuse them instead of failing.
      const byPhone = (await api.get<{ parties: (PartyRow & { Phone?: string })[] | null }>(`/contacts/parties?q=${encodeURIComponent(p.phone)}`)).parties ?? [];
      server = byPhone.find((x) => (x.Phone ?? "") === p.phone && x.PartyType !== "SUPPLIER");
    }
    if (!server) {
      server = await api.post<PartyRow>("/contacts/parties", { party_type: "CUSTOMER", legal_name: p.name, trade_name: "", phone: p.phone, email: "", currency_code: org.currency });
      if (p.gstin) {
        try { await api.post(`/contacts/parties/${server.ID}/tax-registrations`, { country_code: org.country, registration_number: p.gstin, state_code: org.country === "IN" ? p.gstin.slice(0, 2) : "", is_primary: true }); } catch { /* the customer is saved; a bad GSTIN can be fixed on the web */ }
      }
      if (p.address) {
        try { await api.post(`/contacts/parties/${server.ID}/addresses`, { address_type: "BILLING", line1: p.address, line2: "", city: "", state: "", postal_code: "", country_code: org.country }); } catch { /* same */ }
      }
    }
    await mapSet(db, "party", p.id, server.ID);
    await markPartyPushed(db, p.id);
  }
}

async function resolveUnit(api: Api, label: string): Promise<string> {
  let units = (await api.get<{ units_of_measure: Unit[] | null }>("/catalogue/units")).units_of_measure ?? [];
  if (units.length === 0) units = (await api.post<{ units_of_measure: Unit[] }>("/catalogue/units/ensure-default", {})).units_of_measure;
  const want = label.trim().toUpperCase();
  return (units.find((u) => u.Code === want) ?? units.find((u) => u.Code === "PCS") ?? units[0]!).ID;
}

/** Quantity per local item still sitting in unsent sales. Those sales will take it off the server's stock when they sync, so the
 *  opening stock we upload must be the figure from *before* them, or they would be deducted twice. */
async function pendingQty(db: Db): Promise<Map<string, number>> {
  const m = new Map<string, number>();
  for (const sale of await dueSales(db)) for (const l of sale.payload.lines) m.set(l.variantId, (m.get(l.variantId) ?? 0) + num(l.qty));
  return m;
}
const fmtQty = (n: number): string => String(Math.round(n * 10000) / 10000);

export async function pushItems(api: Api, db: Db, org: Org): Promise<{ done: number; problems: string[] }> {
  const todo = await unpushedItems(db);
  if (todo.length === 0) return { done: 0, problems: [] };
  const pending = await pendingQty(db);
  let n = 0;
  const problems: string[] = [];
  for (const raw of todo) {
   try {
    const it: Product = { ...raw, stock: raw.stock == null ? null : fmtQty(num(raw.stock) + (pending.get(raw.variant_id) ?? 0)) };
    const have = await mapGet(db, "item", it.variant_id);
    if (have && isComplete(have.extra, it)) { await markItemPushed(db, it.variant_id); await kvDel(db, startedKey(it.variant_id)); continue; }
    await pushOne(api, db, org, it, have);
    await kvDel(db, startedKey(it.variant_id));
    await markItemPushed(db, it.variant_id);
    n++;
   } catch (e) {
     if (!isRecordProblem(e)) throw e;
     problems.push(`Item "${raw.name}": ${e.message}`);
   }
  }
  return { done: n, problems };
}

const startedKey = (id: string): string => `upload:item:${id}`;

const isComplete = (x: ItemMapExtra, it: Product): boolean =>
  (!it.barcode || !!x.barcodeDone) && (Number(it.stock) <= 0 || !!x.stockDone) && (!(Number(it.price) > 0) || !!x.priceDone) && (!(it.hsn && it.gst_rate) || !!x.taxDone);

async function pushOne(api: Api, db: Db, org: Org, it: Product, existing: { serverId: string; extra: ItemMapExtra } | null): Promise<void> {
  let variantId = existing?.serverId ?? null;
  let x: ItemMapExtra | null = existing?.extra ?? null;
  let recovered = false;

  if (!variantId) {
    // Same SKU already on the server: it is the same item, so use it as-is and don't touch its price or stock.
    const hits = (await api.get<{ results: LookupRow[] | null }>(`/sales/billing-lookup?q=${encodeURIComponent(it.sku)}&warehouse_id=${org.warehouseId}`)).results ?? [];
    const dup = hits.find((h) => h.SKUCode.toLowerCase() === it.sku.toLowerCase());
    if (dup) {
      const prod = await api.get<{ BaseUOMID: string }>(`/catalogue/products/${dup.ProductID}`);
      if (!(await kvGet(db, startedKey(it.variant_id)))) {
        await mapSet(db, "item", it.variant_id, dup.ProductVariantID, { productId: dup.ProductID, unitId: prod.BaseUOMID, stockDone: true, priceDone: true, barcodeDone: true, taxDone: true } satisfies ItemMapExtra);
        return;
      }
      // A previous attempt created it but the answer never reached us: it is ours, so carry on with the remaining steps.
      variantId = dup.ProductVariantID;
      x = { productId: dup.ProductID, unitId: prod.BaseUOMID };
      await mapSet(db, "item", it.variant_id, variantId, x);
      recovered = true;
    }
  }
  if (!variantId) {
    await kvSet(db, startedKey(it.variant_id), "1");
    const unitId = await resolveUnit(api, it.unit_label);
    const product = await api.post<{ ID: string }>("/catalogue/products", { category_id: null, brand_id: null, base_uom_id: unitId, name: it.name, description: "", hsn_sac_code: it.hsn });
    const variant = await api.post<{ ID: string }>("/catalogue/variants", { product_id: product.ID, sku_code: it.sku, attributes: {} });
    variantId = variant.ID;
    x = { productId: product.ID, unitId };
    await mapSet(db, "item", it.variant_id, variantId, x);
  }
  const ext = x!;
  const save = () => mapSet(db, "item", it.variant_id, variantId!, ext);

  if (it.barcode && !ext.barcodeDone) {
    try { await api.post("/catalogue/barcodes", { variant_id: variantId, unit_id: ext.unitId, barcode: it.barcode }); } catch (e) { if (!(e instanceof ApiError) || e.status >= 500) throw e; }
    ext.barcodeDone = true; await save();
  }
  if (Number(it.stock) > 0 && !ext.stockDone) {
    // If an earlier attempt may already have posted it (we lost the answer), ask the server instead of posting a second time.
    if (existing || recovered) {
      const rows = (await api.get<{ results: LookupRow[] | null }>(`/sales/billing-lookup?q=${encodeURIComponent(it.sku)}&warehouse_id=${org.warehouseId}`)).results ?? [];
      const row = rows.find((r) => r.ProductVariantID === variantId);
      if (row && num(row.QuantityOnHand) > 0) ext.stockDone = true;
    }
  }
  if (Number(it.stock) > 0 && !ext.stockDone) {
    await api.post("/inventory/opening-stock", { warehouse_id: org.warehouseId, product_variant_id: variantId, unit_id: ext.unitId, quantity: it.stock, unit_cost: "0" });
    ext.stockDone = true; await save();
  }
  if (Number(it.price) > 0 && !ext.priceDone) {
    const list = await api.post<{ ID: string }>("/pricing/price-lists/ensure-default", { currency_code: org.currency });
    await api.post(`/pricing/price-lists/${list.ID}/items`, { product_variant_id: variantId, unit_id: ext.unitId, amount: it.price, currency_code: org.currency });
    ext.priceDone = true; await save();
  }
  if (it.hsn && it.gst_rate && !ext.taxDone) {
    // The rate belongs to the HSN code. A code that already has a rate keeps it.
    try {
      const have = (await api.get<{ tax_rates: unknown[] | null }>(`/gst/tax-rates/${encodeURIComponent(it.hsn)}`)).tax_rates ?? [];
      if (have.length === 0) await api.post("/gst/tax-rates", { hsn_sac_code: it.hsn, gst_rate: it.gst_rate, cess_rate: "0", valid_from: "2017-07-01" });
    } catch (e) { if (!(e instanceof ApiError) || e.status >= 500) throw e; }
    ext.taxDone = true; await save();
  }
}
