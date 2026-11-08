import { ApiError, NetworkError } from "../src/api/client";
import type { SagaApi } from "../src/sync/saga";

interface Doc { ID: string; Status: string; DocumentNumber: string; ref: string; lines: { ID: string; qty: string; price: string; variant: string; discount?: string }[]; total: string }

/** A stand-in for the Rechvix sales API that behaves like the real one where it matters: lines only on drafts,
 *  finalise assigns the next number and posts exactly once, receipts accumulate. `crashAt` simulates the connection
 *  dying *after the server did the work* (the nastiest case: the phone never hears the answer). */
export class FakeServer implements SagaApi {
  docs = new Map<string, Doc>();
  receipts: { id: string; doc: string | undefined; amount: string; method: string; clientRef?: string; party?: string; reversed: boolean; reason?: string }[] = [];
  createBodies: Record<string, string>[] = [];
  finalizeCount = 0;
  cancelCount = 0;
  nextNo = 1;
  calls: string[] = [];
  /** true = behave like servers before the fix: a part-return copies the line's whole discount. */
  oldDiscountBehaviour = false;
  converts: { source: string; target: string; type: string; quantities: Record<string, string> }[] = [];
  crashAt: string | null = null;
  parties: { ID: string; LegalName: string; PartyType: string }[] = [];
  products: { ID: string; Name: string; hsn: string }[] = [];
  variants: { ID: string; productId: string; sku: string }[] = [];
  openingStock: { variant: string; qty: string }[] = [];
  prices: { variant: string; amount: string }[] = [];
  taxRates = new Map<string, string>();
  reject: { path: RegExp; status: number; code: string; message: string } | null = null;

  private maybeCrash(step: string): void {
    if (this.crashAt === step) { this.crashAt = null; throw new NetworkError(); }
  }

  async get<T>(path: string): Promise<T> {
    this.calls.push(`GET ${path}`);
    let m = /^\/sales\/documents\/([^/]+)$/.exec(path);
    if (m) {
      const d = this.docs.get(m[1]!)!;
      return { document: { ID: d.ID, Status: d.Status, DocumentNumber: d.Status === "FINALIZED" ? d.DocumentNumber : "", GrandTotalAmount: d.Status === "FINALIZED" ? { amount: d.total, currency: "INR" } : null }, lines: d.lines.map((l, i) => ({ ...l, LineNumber: i + 1, Quantity: l.qty, LineDiscountAmount: { amount: l.discount ?? "0", currency: "INR" } })) } as T;
    }
    m = /^\/accounting\/sales-documents\/([^/]+)\/receipts$/.exec(path);
    if (m) return { receipts: this.receipts.filter((r) => r.doc === m![1] && !r.reversed).map((r) => ({ Amount: { amount: r.amount, currency: "INR" } })) } as T;
    m = /^\/contacts\/parties\?q=(.*)$/.exec(path);
    if (m) { const q = decodeURIComponent(m[1]!).toLowerCase(); return { parties: this.parties.filter((p) => p.LegalName.toLowerCase().includes(q)) } as T; }
    m = /^\/sales\/billing-lookup\?q=([^&]*)/.exec(path);
    if (m) { const q = decodeURIComponent(m[1]!).toLowerCase(); return { results: this.variants.filter((v) => v.sku.toLowerCase().includes(q)).map((v) => ({ ProductID: v.productId, ProductVariantID: v.ID, SKUCode: v.sku, QuantityOnHand: String(this.openingStock.filter((o) => o.variant === v.ID).reduce((n, o) => n + Number(o.qty), 0)) })) } as T; }
    if (path === "/catalogue/units") return { units_of_measure: [{ ID: "u-pcs", Code: "PCS" }, { ID: "u-kg", Code: "KG" }] } as T;
    m = /^\/catalogue\/products\/([^/]+)$/.exec(path);
    if (m) return { BaseUOMID: "u-pcs" } as T;
    m = /^\/gst\/tax-rates\/(.+)$/.exec(path);
    if (m) { const r = this.taxRates.get(decodeURIComponent(m[1]!)); return { tax_rates: r ? [{ GSTRate: r }] : [] } as T; }
    throw new Error(`fake: unhandled GET ${path}`);
  }

  async post<T>(path: string, body?: unknown): Promise<T> {
    this.calls.push(`POST ${path}`);
    if (this.reject?.path.test(path)) throw new ApiError(this.reject.status, this.reject.code, this.reject.message);
    const b = (body ?? {}) as Record<string, string>;
    if (path === "/sales/documents") {
      this.createBodies.push(b);
      if (!this.parties.some((p) => p.ID === b.customer_party_id) && b.customer_party_id !== "cust" && !String(b.customer_party_id).startsWith("c")) throw new ApiError(500, "INTERNAL_ERROR", `FK: no customer ${b.customer_party_id}`);
      const id = `doc-${this.docs.size + 1}`;
      this.docs.set(id, { ID: id, Status: "DRAFT", DocumentNumber: "", ref: b.customer_reference ?? "", lines: [], total: "0" });
      this.maybeCrash("after-create");
      return { ID: id } as T;
    }
    let m = /^\/sales\/documents\/([^/]+)\/lines$/.exec(path);
    if (m) {
      const d = this.docs.get(m[1]!)!;
      if (d.Status !== "DRAFT") throw new ApiError(409, "NOT_DRAFT", "Document is not a draft.");
      if (String(b.product_variant_id).endsWith("-local")) throw new ApiError(400, "BAD_REQUEST", "Request body is malformed.");
      d.lines.push({ ID: `l${d.lines.length + 1}`, qty: b.quantity!, price: b.unit_price!, variant: b.product_variant_id!, discount: b.line_discount_amount ?? "0" });
      if (this.crashAt === `after-line-${d.lines.length}`) { this.crashAt = null; throw new NetworkError(); }
      return {} as T;
    }
    m = /^\/sales\/documents\/([^/]+)\/finalize$/.exec(path);
    if (m) {
      const d = this.docs.get(m[1]!)!;
      if (d.Status !== "DRAFT") throw new ApiError(409, "NOT_DRAFT", "Already finalised.");
      d.Status = "FINALIZED";
      d.DocumentNumber = `INV/${String(this.nextNo++).padStart(6, "0")}`;
      const sub = d.lines.reduce((s, l) => s + Number(l.qty) * Number(l.price), 0);
      d.total = (sub * 1.18).toFixed(2); // 18% GST computed by the server, never by the phone
      this.finalizeCount++;
      this.maybeCrash("after-finalize");
      return {} as T;
    }
    m = /^\/sales\/documents\/([^/]+)\/convert$/.exec(path);
    if (m) {
      const src = this.docs.get(m[1]!)!;
      if (src.Status !== "FINALIZED") throw new ApiError(409, "DOCUMENT_NOT_FINALIZED", "Only a FINALIZED document can be converted or cancelled.");
      const q = (body as { line_quantities?: Record<string, string>; target_type: string }).line_quantities ?? {};
      for (const l of src.lines) if (q[l.ID] != null && Number(q[l.ID]) > Number(l.qty)) throw new ApiError(409, "RETURN_QUANTITY_EXCEEDS_SOURCE", "too many");
      const id = `doc-${this.docs.size + 1}`;
      const lines = src.lines.filter((l) => q[l.ID] != null).map((l, i) => ({ ID: `l${i + 1}`, qty: q[l.ID]!, price: l.price, variant: l.variant,
        discount: this.oldDiscountBehaviour ? l.discount ?? "0" : String((Number(l.discount ?? "0") * Number(q[l.ID]!)) / Number(l.qty)) }));
      this.docs.set(id, { ID: id, Status: "DRAFT", DocumentNumber: "", ref: "", lines, total: "0" });
      this.converts.push({ source: src.ID, target: id, type: (body as { target_type: string }).target_type, quantities: q });
      this.maybeCrash("after-convert");
      return { ID: id } as T;
    }
    m = /^\/sales\/documents\/([^/]+)\/cancel$/.exec(path);
    if (m) {
      const d = this.docs.get(m[1]!)!;
      if (d.Status !== "FINALIZED") throw new ApiError(409, "DOCUMENT_NOT_FINALIZED", "Only a FINALIZED document can be converted or cancelled.");
      d.Status = "CANCELLED"; this.cancelCount++; this.maybeCrash("after-cancel");
      return {} as T;
    }
    if (path === "/accounting/receipts") {
      const dup = b.client_ref ? this.receipts.find((r) => r.clientRef === b.client_ref) : undefined;
      if (dup) return { ID: dup.id } as T;           // the real server returns the receipt it already booked
      const rec = { id: `rc-${this.receipts.length + 1}`, doc: b.sales_document_id, amount: b.amount!, method: b.method!, clientRef: b.client_ref, party: b.party_id, reversed: false };
      this.receipts.push(rec);
      this.maybeCrash("after-receipt");
      return { ID: rec.id } as T;
    }
    m = /^\/accounting\/receipts\/([^/]+)\/reverse$/.exec(path);
    if (m) {
      const r = this.receipts.find((x) => x.id === m![1]);
      if (!r) throw new ApiError(404, "NOT_FOUND", "no such receipt");
      if (r.reversed) throw new ApiError(409, "ALREADY_REVERSED", "already reversed");
      if (!b.reason) throw new ApiError(400, "INVALID_INPUT", "a reason is required");
      r.reversed = true; r.reason = b.reason; this.maybeCrash("after-reverse");
      return {} as T;
    }
    if (path === "/contacts/parties") {
      const p = { ID: `party-${this.parties.length + 1}`, LegalName: b.legal_name!, PartyType: b.party_type! };
      this.parties.push(p); this.maybeCrash("after-party"); return p as T;
    }
    if (path === "/catalogue/products") { const p = { ID: `prod-${this.products.length + 1}`, Name: b.name!, hsn: b.hsn_sac_code ?? "" }; this.products.push(p); return p as T; }
    if (path === "/catalogue/variants") {
      const v = { ID: `var-${this.variants.length + 1}`, productId: b.product_id!, sku: b.sku_code! };
      this.variants.push(v); this.maybeCrash("after-variant"); return v as T;
    }
    if (path === "/catalogue/barcodes") return {} as T;
    if (path === "/inventory/opening-stock") { this.openingStock.push({ variant: b.product_variant_id!, qty: b.quantity! }); this.maybeCrash("after-stock"); return {} as T; }
    if (path === "/pricing/price-lists/ensure-default") return { ID: "pl-1" } as T;
    m = /^\/pricing\/price-lists\/[^/]+\/items$/.exec(path);
    if (m) { this.prices.push({ variant: b.product_variant_id!, amount: b.amount! }); this.maybeCrash("after-price"); return {} as T; }
    if (path === "/gst/tax-rates") { this.taxRates.set(b.hsn_sac_code!, b.gst_rate!); return {} as T; }
    throw new Error(`fake: unhandled POST ${path}`);
  }
}
