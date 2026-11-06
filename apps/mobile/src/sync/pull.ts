import type { Api } from "../api/client";
import type { Db } from "../db/types";
import { getOrg, kvSet, setOrg, type Org } from "../db/repo";

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
  await db.run("DELETE FROM products WHERE gen < ?", [gen]);

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
  await db.run("DELETE FROM parties WHERE gen < ?", [gen]);

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

  const org: Org = {
    organisationId: le.OrganisationID, legalEntityId: le.ID, legalName: le.LegalName, country: le.CountryCode, currency: le.BaseCurrencyCode,
    gstStateCode: le.GSTStateCode, branchId: branch.ID, warehouseId: wh.ID, priceListId: priceList.ID, walkInPartyId: walkIn.ID,
  };
  await setOrg(db, org);
  await kvSet(db, "last_pull_at", String(gen));
  void prev;
  return { products, parties: count };
}
