import type { Api } from "../api/client";
import type { Db } from "../db/types";
import { getOrg, kvSet, reapplyPendingStock, setOrg, type Org } from "../db/repo";

interface LegalEntity { ID: string; OrganisationID: string; LegalName: string; CountryCode: string; BaseCurrencyCode: string; GSTStateCode: string; Status: string }
interface Branch { ID: string; Status: string }
interface Warehouse { ID: string; Status: string }
interface Lookup { ProductID: string; ProductName: string; HSNSACCode: string; ProductVariantID: string; SKUCode: string; QuantityAvailable: string; UnitPrice: { amount: string } | null }
interface ProductRow { ID: string; BaseUOMID: string }
interface PartyRow { ID: string; PartyType: string; LegalName: string; Phone: string; Status: string }
interface DocRow { ID: string; DocumentNumber: string; DocumentType: string; Status: string; IssueDate: string; CustomerPartyID: string; GrandTotalAmount: { amount: string } | null }

const WALK_IN = "Walk-in Customer";
const PAGE = 100;

/** Refreshes everything the phone needs to work offline. Writes are per-row upserts tagged with this pull's generation,
 *  then older rows are deleted: a crash halfway leaves the previous catalogue intact and never blocks a sale being saved. */
export async function pullMaster(api: Api, db: Db, now: () => number = Date.now): Promise<{ products: number; parties: number }> {
  const gen = now();
  const prev = await getOrg(db);

  const les = (await api.get<{ legal_entities: LegalEntity[] | null }>("/legal-entities")).legal_entities ?? [];
  const le = les.find((x) => x.Status === "ACTIVE") ?? les[0];
  if (!le) throw new Error("This account has no business set up yet. Finish setup on the web first.");
  const branches = (await api.get<{ branches: Branch[] | null }>("/branches")).branches ?? [];
  const branch = branches.find((b) => b.Status === "ACTIVE") ?? branches[0];
  if (!branch) throw new Error("This business has no branch yet.");
  const whs = (await api.get<{ warehouses: Warehouse[] | null }>(`/branches/${branch.ID}/warehouses`)).warehouses ?? [];
  const wh = whs.find((w) => w.Status === "ACTIVE") ?? whs[0];
  if (!wh) throw new Error("This branch has no warehouse yet.");
  const priceList = await api.post<{ ID: string }>("/pricing/price-lists/ensure-default", { currency_code: le.BaseCurrencyCode });

  // Products: the billing lookup carries price and stock; the product list carries the unit each line needs.
  const lookup = (await api.get<{ results: Lookup[] | null }>(`/sales/billing-lookup?q=&price_list_id=${priceList.ID}&warehouse_id=${wh.ID}`)).results ?? [];
  const unitByProduct = new Map<string, string>();
  for (let page = 1; ; page++) {
    const r = await api.get<{ products: ProductRow[] | null }>(`/catalogue/products?page=${page}&limit=${PAGE}`);
    const rows = r.products ?? [];
    for (const p of rows) unitByProduct.set(p.ID, p.BaseUOMID);
    if (rows.length < PAGE) break;
  }
  let products = 0;
  for (const l of lookup) {
    const unit = unitByProduct.get(l.ProductID);
    if (!unit) continue;
    await db.run(
      `INSERT INTO products(variant_id,product_id,name,sku,hsn,unit_id,price,stock,gen) VALUES(?,?,?,?,?,?,?,?,?)
       ON CONFLICT(variant_id) DO UPDATE SET product_id=excluded.product_id,name=excluded.name,sku=excluded.sku,hsn=excluded.hsn,unit_id=excluded.unit_id,price=excluded.price,stock=excluded.stock,gen=excluded.gen`,
      [l.ProductVariantID, l.ProductID, l.ProductName, l.SKUCode ?? "", l.HSNSACCode ?? "", unit, l.UnitPrice?.amount ?? null, l.QuantityAvailable === "" ? null : l.QuantityAvailable, gen],
    );
    products++;
  }
  // Stale server rows go; so do local items that have been uploaded (the server's own copy was just pulled).
  // Local items not yet uploaded are never touched.
  await db.run("DELETE FROM products WHERE gen < ? AND (origin='server' OR pushed=1)", [gen]);

  // Tax rates belong to the HSN code. Fetch each distinct code once so offline invoices show the same GST the server will add.
  if (le.CountryCode === "IN") {
    const codes = await db.all<{ hsn: string }>("SELECT DISTINCT hsn FROM products WHERE hsn != '' AND origin='server' LIMIT 80");
    for (const { hsn } of codes) {
      try {
        const r = await api.get<{ tax_rates: { GSTRate?: string; gst_rate?: string }[] | null }>(`/gst/tax-rates/${encodeURIComponent(hsn)}`);
        const rate = r.tax_rates?.[0]?.GSTRate ?? r.tax_rates?.[0]?.gst_rate;
        if (rate != null) await db.run("UPDATE products SET gst_rate=? WHERE hsn=? AND origin='server'", [String(rate), hsn]);
      } catch { /* leave the rate unknown; the server still taxes the sale correctly */ }
    }
  }

  // Customers (and the shared walk-in customer, created once if the business never had one).
  let parties = (await api.get<{ parties: PartyRow[] | null }>("/contacts/parties")).parties ?? [];
  let walkIn = parties.find((p) => p.LegalName.toLowerCase() === WALK_IN.toLowerCase());
  if (!walkIn) {
    const created = await api.post<PartyRow>("/contacts/parties", { party_type: "CUSTOMER", legal_name: WALK_IN, trade_name: "", phone: "", email: "", currency_code: le.BaseCurrencyCode });
    parties = [...parties, created];
    walkIn = created;
  }
  let count = 0;
  for (const p of parties) {
    if (p.Status !== "ACTIVE" && p.ID !== walkIn.ID) continue;
    if (p.PartyType === "SUPPLIER") continue;
    await db.run(
      `INSERT INTO parties(id,name,phone,type,is_walk_in,gen) VALUES(?,?,?,?,?,?)
       ON CONFLICT(id) DO UPDATE SET name=excluded.name,phone=excluded.phone,type=excluded.type,is_walk_in=excluded.is_walk_in,gen=excluded.gen`,
      [p.ID, p.LegalName, p.Phone ?? "", p.PartyType, p.ID === walkIn.ID ? 1 : 0, gen],
    );
    count++;
  }
  await db.run("DELETE FROM parties WHERE gen < ? AND (origin='server' OR pushed=1)", [gen]);

  // Recent invoices from anywhere, for the history list. Best effort: history is nice to have, not needed to sell.
  try {
    const docs = ((await api.get<{ documents: DocRow[] | null }>("/sales/documents?limit=60")).documents ?? []).filter((d) => d.Status !== "DRAFT").slice(0, 60);
    for (const d of docs) {
      await db.run(
        `INSERT INTO server_docs(id,number,doc_type,status,issue_date,customer_id,total,gen) VALUES(?,?,?,?,?,?,?,?)
         ON CONFLICT(id) DO UPDATE SET number=excluded.number,doc_type=excluded.doc_type,status=excluded.status,issue_date=excluded.issue_date,customer_id=excluded.customer_id,total=excluded.total,gen=excluded.gen`,
        [d.ID, d.DocumentNumber, d.DocumentType, d.Status, d.IssueDate, d.CustomerPartyID, d.GrandTotalAmount?.amount ?? null, gen],
      );
    }
    await db.run("DELETE FROM server_docs WHERE gen < ?", [gen]);
  } catch { /* keep the previous history */ }

  // Who owes money, across the whole business (invoices made on the web or other phones too). Best effort, like history.
  try {
    const rows = (await api.get<{ parties: { PartyID: string; PartyName: string; Phone: string; Current: { amount: string }; Days1To30: { amount: string }; Days31To60: { amount: string }; Days61To90: { amount: string }; Days90Plus: { amount: string }; Total: { amount: string }; LastReminderSentAt: string | null; ReminderCount: number }[] | null }>("/reports/accounting/receivables/detailed")).parties ?? [];
    for (const r of rows) {
      await db.run(
        `INSERT INTO server_balances(party_id,name,phone,total,current_amt,d30,d60,d90,d90plus,last_reminder_at,reminder_count,gen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
         ON CONFLICT(party_id) DO UPDATE SET name=excluded.name,phone=excluded.phone,total=excluded.total,current_amt=excluded.current_amt,d30=excluded.d30,d60=excluded.d60,d90=excluded.d90,d90plus=excluded.d90plus,last_reminder_at=excluded.last_reminder_at,reminder_count=excluded.reminder_count,gen=excluded.gen`,
        [r.PartyID, r.PartyName, r.Phone ?? "", r.Total.amount, r.Current.amount, r.Days1To30.amount, r.Days31To60.amount, r.Days61To90.amount, r.Days90Plus.amount, r.LastReminderSentAt ? Date.parse(r.LastReminderSentAt) : null, r.ReminderCount ?? 0, gen],
      );
    }
    await db.run("DELETE FROM server_balances WHERE gen < ?", [gen]);
  } catch { /* keep the previous balances */ }

  const org: Org = {
    organisationId: le.OrganisationID, legalEntityId: le.ID, legalName: le.LegalName, country: le.CountryCode, currency: le.BaseCurrencyCode,
    gstStateCode: le.GSTStateCode, branchId: branch.ID, warehouseId: wh.ID, priceListId: priceList.ID, walkInPartyId: walkIn.ID,
  };
  await setOrg(db, org);
  await reapplyPendingStock(db);
  await kvSet(db, "last_pull_at", String(gen));
  void prev;
  return { products, parties: count };
}
