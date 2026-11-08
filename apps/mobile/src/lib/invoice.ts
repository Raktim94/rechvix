import type { Profile, SaleLine } from "../db/repo";
import { minorToString } from "./decimal";
import { formatMinor } from "./money";
import { calcInvoice, type InvoiceCalc } from "./tax";

/** Presentation only. Choosing a design never changes a figure: every design draws the same InvoiceCalc. */
export type InvoiceTemplate = "classic" | "modern" | "thermal";
export const TEMPLATES: { key: InvoiceTemplate; label: string; blurb: string; paper: string }[] = [
  { key: "classic", label: "Classic tax invoice", blurb: "Bordered table with HSN, GST by rate and a full tax summary. The traditional layout accountants expect.", paper: "A4" },
  { key: "modern", label: "Clean modern", blurb: "Light, spacious layout with a clear total. Easy to read on a phone screen.", paper: "A4" },
  { key: "thermal", label: "Compact receipt", blurb: "Narrow layout for 80 mm thermal printers and quick WhatsApp receipts.", paper: "80 mm" },
];
export const DEFAULT_TEMPLATE: InvoiceTemplate = "classic";
export const isTemplate = (v: unknown): v is InvoiceTemplate => v === "classic" || v === "modern" || v === "thermal";

/** One live (not reversed) payment against the invoice, oldest first. */
export interface PaymentEntry { at: number; amountMinor: bigint; method: string; reference?: string }

/** What the document is. A quotation is an offer (no payment block, no tax-invoice wording); a credit note is a return against an invoice. */
export type DocKind = "invoice" | "quotation" | "credit" | "delivery";

export interface PaymentSummary {
  paidMinor: bigint; dueMinor: bigint; status: "PAID" | "PARTIAL" | "UNPAID";
  /** Credit notes issued against the invoice (reduce what is owed). */
  creditMinor?: bigint;
  /** Money received beyond what is owed after credit notes: owed back to the customer. Never invented: it is paid - (total - credits). */
  refundMinor?: bigint;
}

export interface InvoiceData {
  number: string;
  date: number;
  business: Profile;
  customerName: string;
  customerGstin?: string;
  customerPhone?: string;
  /** "27 – Maharashtra" style text, or undefined when the customer's state is not recorded. */
  placeOfSupply?: string;
  lines: SaleLine[];
  calc: InvoiceCalc;
  currency: string;
  payment: PaymentSummary;
  kind?: DocKind;
  /** The linked server's company is GST-registered (it issues tax invoices) even if this phone's own business details have no GSTIN typed in. */
  gstRegistered?: boolean;
  /** Quotations: the last day the offer stands. */
  validUntil?: number | null;
  /** Credit notes: the invoice being credited. */
  against?: string;
  /** Credit notes already issued against this invoice. */
  credits?: { number: string; totalMinor: bigint }[];
  /** Each installment received. The invoice total is never replaced by what is left: it is printed with these. */
  payments?: PaymentEntry[];
  cancelled?: boolean;
  /** Number of the invoice this one corrects, when it is a re-issue. */
  corrects?: string | null;
  note?: string;
}

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
const nl2br = (s: string) => esc(s).replace(/\n/g, "<br>");

const STATUS_TEXT: Record<PaymentSummary["status"], string> = { PAID: "PAID", PARTIAL: "PART PAID", UNPAID: "UNPAID" };

function body(d: InvoiceData) {
  const m = (x: bigint) => esc(formatMinor(x, d.currency));
  const incl = d.calc.mode === "INCLUSIVE";
  const rows = d.lines.map((l, i) => ({ l, c: d.calc.lines[i]!, i: i + 1 }));
  return { m, incl, rows };
}

const taxSummaryRows = (d: InvoiceData, m: (x: bigint) => string) =>
  d.calc.components.map((c) => `<tr><td>${c.label}</td><td class="r">${m(c.minor)}</td></tr>`).join("");

const payLine = (p: PaymentEntry) => `${esc(new Date(p.at).toLocaleDateString())} · ${esc(p.method.replace(/_/g, " "))}${p.reference ? ` (${esc(p.reference)})` : ""}`;

interface SettleLine { k: string; v: string; tone: "hist" | "plain" | "due" }
/** The lines under the invoice total: credit notes, each payment, total paid, balance due. Quotations and credit notes have none. */
function settleLines(d: InvoiceData, m: (x: bigint) => string): SettleLine[] {
  if (d.kind && d.kind !== "invoice") return [];
  const out: SettleLine[] = [];
  for (const c of d.credits ?? []) out.push({ k: `Credit note ${esc(c.number)}`, v: `-${m(c.totalMinor)}`, tone: "plain" });
  for (const p of d.payments ?? []) out.push({ k: payLine(p), v: m(p.amountMinor), tone: "hist" });
  out.push({ k: "Total paid", v: m(d.payment.paidMinor), tone: "plain" });
  out.push({ k: "Balance due", v: m(d.payment.dueMinor), tone: "due" });
  if ((d.payment.refundMinor ?? 0n) > 0n) out.push({ k: "Refund due to customer", v: m(d.payment.refundMinor!), tone: "plain" });
  return out;
}
/** A GST-registered Indian business issues a TAX INVOICE; an unregistered one may only issue a BILL OF SUPPLY (no tax invoice); a
 *  business outside India gets a plain INVOICE. The label follows what the business has configured, never a claim it has not made. */
export const invoiceTitle = (b: { country?: string; gstin?: string; gstRegistered?: boolean }): string => ((b.country ?? "IN") !== "IN" ? "INVOICE" : b.gstin?.trim() || b.gstRegistered ? "TAX INVOICE" : "BILL OF SUPPLY");
const titleOf = (d: InvoiceData) => (d.kind === "quotation" ? "QUOTATION" : d.kind === "credit" ? "CREDIT NOTE" : d.kind === "delivery" ? "DELIVERY NOTE" : invoiceTitle({ ...d.business, gstRegistered: d.gstRegistered }));
const wordOf = (d: InvoiceData) => (d.kind === "quotation" ? "Quotation" : d.kind === "credit" ? "Credit note" : d.kind === "delivery" ? "Delivery note" : invoiceTitle({ ...d.business, gstRegistered: d.gstRegistered }) === "BILL OF SUPPLY" ? "Bill of supply" : "Invoice");
/** Pay status for an invoice; for a quotation, how long the offer stands; nothing for a credit note. */
const statusText = (d: InvoiceData) => (!d.kind || d.kind === "invoice" ? STATUS_TEXT[d.payment.status] : d.kind === "quotation" && d.validUntil ? `Valid until ${new Date(d.validUntil).toLocaleDateString()}` : d.kind === "credit" && d.against ? `Against ${d.against}` : "");

const paymentRows = (d: InvoiceData, m: (x: bigint) => string) =>
  settleLines(d, m).map((l) => `<tr class="${l.tone === "hist" ? "hist" : l.tone === "due" ? "due" : ""}"><td>${l.k}</td><td class="r">${l.v}</td></tr>`).join("");

const headerInfo = (d: InvoiceData) => `${nl2br(d.business.address)}${d.business.phone ? `<br>Phone ${esc(d.business.phone)}` : ""}${d.business.gstin ? `<br>GSTIN ${esc(d.business.gstin)}` : ""}`;
const customerBlock = (d: InvoiceData) => `<div class="k">Billed to</div><div class="big">${esc(d.customerName)}</div>${d.customerGstin ? `<div>GSTIN ${esc(d.customerGstin)}</div>` : ""}${d.customerPhone ? `<div>${esc(d.customerPhone)}</div>` : ""}<div class="muted">Place of supply: ${d.placeOfSupply ? esc(d.placeOfSupply) : "same state as seller (customer state not recorded)"}</div>`;
const flags = (d: InvoiceData) => `${d.cancelled ? `<div class="stamp">CANCELLED</div>` : ""}${d.corrects ? `<div class="muted">Corrects invoice ${esc(d.corrects)}</div>` : ""}`;

/** Classic: bordered A4 tax invoice. */
function classic(d: InvoiceData): string {
  const { m, incl, rows } = body(d);
  const lines = rows.map(({ l, c, i }) => `<tr><td>${i}</td><td>${esc(l.name)}</td><td>${esc(l.hsn ?? "")}</td><td class="r">${esc(l.qty)}</td><td class="r">${m(parse(l.price))}${incl ? "*" : ""}</td><td class="r">${l.discount && l.discount !== "0" ? m(parse(l.discount)) : "–"}</td><td class="r">${m(c.taxableMinor)}</td><td class="r">${c.rate}%</td><td class="r">${m(c.grossMinor)}</td></tr>`).join("");
  const byRate = d.calc.byRate.filter((g) => g.rate !== "0").map((g) => `<tr><td>${g.rate}%</td><td class="r">${m(g.taxableMinor)}</td><td class="r">${m(g.taxMinor)}</td></tr>`).join("");
  return `<style>
 body{font-family:Roboto,Helvetica,Arial,sans-serif;color:#16211c;margin:26px;font-size:12px}
 h1{font-size:20px;margin:0;color:#0f6e5c} .r{text-align:right} .muted{color:#5b6b62} .k{font-size:10px;text-transform:uppercase;letter-spacing:.6px;color:#5b6b62} .big{font-size:15px;font-weight:700}
 .head{display:flex;justify-content:space-between;gap:20px;border-bottom:2px solid #16211c;padding-bottom:10px} .meta{text-align:right}
 table{width:100%;border-collapse:collapse;margin-top:14px} th{font-size:10px;text-transform:uppercase;border:1px solid #16211c;background:#eef0ec;padding:5px;text-align:left} th.r{text-align:right}
 td{border:1px solid #c3cac0;padding:5px;vertical-align:top} .two{display:flex;gap:18px;margin-top:6px} .two>div{flex:1}
 .tot tr.hist td{color:#6a756f;font-size:11px;padding-top:1px;padding-bottom:1px} .tot td{border:0;border-bottom:1px solid #dbdfd8;padding:4px} .grand td{font-size:15px;font-weight:700;border-top:2px solid #16211c} .due td{font-weight:700;color:#9c3a26}
 .status{display:inline-block;border:2px solid #0f6e5c;color:#0f6e5c;padding:2px 8px;font-weight:700;letter-spacing:1px} .stamp{color:#9c3a26;border:3px solid #9c3a26;font-weight:800;font-size:22px;padding:2px 10px;display:inline-block;transform:rotate(-6deg);margin:6px 0}
</style>
<div class="head"><div><h1>${esc(d.business.name || "Tax invoice")}</h1><div class="muted">${headerInfo(d)}</div></div>
 <div class="meta"><div class="big">${titleOf(d)}</div><div>${esc(d.number)}</div><div class="muted">${esc(new Date(d.date).toLocaleDateString())}</div><div class="status">${esc(statusText(d))}</div>${flags(d)}</div></div>
<div style="margin-top:12px">${customerBlock(d)}</div>
<table><thead><tr><th>#</th><th>Item</th><th>HSN</th><th class="r">Qty</th><th class="r">Rate</th><th class="r">Disc.</th><th class="r">Taxable</th><th class="r">GST</th><th class="r">Amount</th></tr></thead><tbody>${lines}</tbody></table>
${incl ? `<div class="muted" style="margin-top:4px">* Rates include GST. Tax is shown separately below.</div>` : ""}
<div class="two"><div><table><thead><tr><th>GST rate</th><th class="r">Taxable</th><th class="r">Tax</th></tr></thead><tbody>${byRate || `<tr><td colspan="3" class="muted">No GST on this invoice</td></tr>`}</tbody></table></div>
<div><table class="tot"><tr><td>Taxable value</td><td class="r">${m(d.calc.taxableMinor)}</td></tr>${taxSummaryRows(d, m)}<tr class="grand"><td>Total</td><td class="r">${m(d.calc.totalMinor)}</td></tr>${paymentRows(d, m)}</table></div></div>
${d.note ? `<div style="margin-top:10px">${nl2br(d.note)}</div>` : ""}<div class="muted" style="margin-top:26px">Thank you for your business.</div>`;
}

/** Modern: airy, one-column, big total. */
function modern(d: InvoiceData): string {
  const { m, incl, rows } = body(d);
  const lines = rows.map(({ l, c }) => `<div class="li"><div><div class="nm">${esc(l.name)}</div><div class="muted">${esc(l.qty)} × ${m(parse(l.price))}${incl ? " incl. GST" : ""}${c.rate !== "0" ? ` · GST ${c.rate}%` : ""}${l.discount && l.discount !== "0" ? ` · discount ${m(parse(l.discount))}` : ""}</div></div><div class="amt">${m(c.grossMinor)}</div></div>`).join("");
  const comps = d.calc.components.map((c) => `<div class="row"><span>${c.label}</span><span>${m(c.minor)}</span></div>`).join("");
  return `<style>
 body{font-family:Roboto,Helvetica,Arial,sans-serif;color:#16211c;margin:34px;font-size:13px}
 .top{display:flex;justify-content:space-between;align-items:flex-start} h1{font-size:24px;margin:0;letter-spacing:-.4px} .muted{color:#5b6b62}
 .pill{display:inline-block;background:#e3f0ec;color:#0f6e5c;border-radius:99px;padding:3px 12px;font-weight:700;font-size:12px} .pill.due{background:#fbeae5;color:#9c3a26} .pill.part{background:#fbf1de;color:#7a5407}
 .k{font-size:10px;text-transform:uppercase;letter-spacing:.8px;color:#5b6b62;margin-top:22px} .big{font-size:16px;font-weight:700}
 .li{display:flex;justify-content:space-between;gap:16px;padding:12px 0;border-bottom:1px solid #e6e9e4} .nm{font-weight:600} .amt{font-variant-numeric:tabular-nums;font-weight:600}
 .row{display:flex;justify-content:space-between;padding:5px 0;color:#5b6b62} .total{display:flex;justify-content:space-between;align-items:baseline;margin-top:14px;padding:16px 18px;background:#0f6e5c;color:#fff;border-radius:14px}
 .total b{font-size:26px} .stamp{color:#9c3a26;font-weight:800;letter-spacing:2px;margin-top:8px}
</style>
<div class="top"><div><h1>${esc(d.business.name || "Invoice")}</h1><div class="muted">${headerInfo(d)}</div></div>
 <div style="text-align:right"><div class="muted">${wordOf(d)}</div><div class="big">${esc(d.number)}</div><div class="muted">${esc(new Date(d.date).toLocaleDateString())}</div>${statusText(d) ? `<div class="pill ${d.kind && d.kind !== "invoice" ? "part" : d.payment.status === "PAID" ? "" : d.payment.status === "PARTIAL" ? "part" : "due"}">${esc(statusText(d))}</div>` : ""}${flags(d)}</div></div>
<div class="k">Billed to</div><div class="big">${esc(d.customerName)}</div><div class="muted">${d.customerGstin ? `GSTIN ${esc(d.customerGstin)} · ` : ""}${d.customerPhone ? esc(d.customerPhone) + " · " : ""}Place of supply: ${d.placeOfSupply ? esc(d.placeOfSupply) : "customer state not recorded (seller's state)"}</div>
<div class="k">Items</div>${lines}
<div style="margin-top:14px"><div class="row"><span>Taxable value</span><span>${m(d.calc.taxableMinor)}</span></div>${comps}</div>
<div class="total"><span>Total</span><b>${m(d.calc.totalMinor)}</b></div>
${settleLines(d, m).map((l) => `<div class="row${l.tone === "hist" ? " muted" : ""}"${l.tone === "due" ? ' style="color:#16211c;font-weight:700"' : ""}><span>${l.k}</span><span>${l.v}</span></div>`).join("")}
${d.note ? `<div style="margin-top:14px" class="muted">${nl2br(d.note)}</div>` : ""}<div class="muted" style="margin-top:30px;text-align:center">Thank you for your business.</div>`;
}

/** Thermal: 80 mm receipt. Monospace, single column, no colour. */
function thermal(d: InvoiceData): string {
  const { m, incl, rows } = body(d);
  const lines = rows.map(({ l, c }) => `<div class="it">${esc(l.name)}</div><div class="ln"><span>${esc(l.qty)} × ${m(parse(l.price))}${c.rate !== "0" ? ` @${c.rate}%` : ""}</span><span>${m(c.grossMinor)}</span></div>`).join("");
  const comps = d.calc.components.map((c) => `<div class="ln"><span>${c.label}</span><span>${m(c.minor)}</span></div>`).join("");
  return `<style>
 body{font-family:"Courier New",monospace;width:72mm;margin:0 auto;padding:6px;font-size:11px;color:#000} .c{text-align:center} .b{font-weight:700} hr{border:0;border-top:1px dashed #000;margin:6px 0}
 .ln{display:flex;justify-content:space-between;gap:6px} .it{margin-top:4px} .tot{font-size:14px;font-weight:700}
</style>
<div class="c b" style="font-size:14px">${esc(d.business.name || "Invoice")}</div><div class="c">${headerInfo(d)}</div><hr>
<div class="c b">${titleOf(d)}</div>
<div class="ln"><span>${esc(d.number)}</span><span>${esc(new Date(d.date).toLocaleDateString())}</span></div><div>To: ${esc(d.customerName)}${d.customerGstin ? ` (GSTIN ${esc(d.customerGstin)})` : ""}</div><div>Supply: ${d.placeOfSupply ? esc(d.placeOfSupply) : "customer state not recorded"}</div>${d.cancelled ? `<div class="c b">** CANCELLED **</div>` : ""}${d.corrects ? `<div>Corrects ${esc(d.corrects)}</div>` : ""}<hr>
${lines}<hr>
<div class="ln"><span>Taxable value</span><span>${m(d.calc.taxableMinor)}</span></div>${comps}
<div class="ln tot"><span>TOTAL</span><span>${m(d.calc.totalMinor)}</span></div>${incl ? `<div class="c">(prices include GST)</div>` : ""}<hr>
${settleLines(d, m).map((l) => `<div class="ln${l.tone === "due" ? " b" : ""}"><span>${l.k}</span><span>${l.v}</span></div>`).join("")}${statusText(d) ? `<div class="c b" style="margin-top:4px">${esc(statusText(d))}</div>` : ""}<hr>
<div class="c">Thank you!</div>`;
}

const parse = (s: string): bigint => {
  const [w = "0", f = ""] = (s || "0").split(".");
  return BigInt(w || "0") * 100n + BigInt((f + "00").slice(0, 2));
};

/** Delivery note: what was handed over, with no prices, tax or totals, and a line for the receiver to sign. Always on A4. */
function delivery(d: InvoiceData): string {
  const rows = d.lines.map((l, i) => `<tr><td>${i + 1}</td><td>${esc(l.name)}${l.hsn ? `<div class="muted">HSN ${esc(l.hsn)}</div>` : ""}</td><td class="r">${esc(l.qty)} ${esc(l.unitLabel ?? "")}</td></tr>`).join("");
  return `<style>
 body{font-family:Helvetica,Arial,sans-serif;color:#16211c;font-size:13px;margin:0;padding:28px} h1{margin:0;font-size:20px} .muted{color:#6a756f;font-size:11px}
 table{width:100%;border-collapse:collapse;margin-top:16px} th,td{border:1px solid #cfd6d1;padding:7px 9px;text-align:left} th{background:#eef2ee} .r{text-align:right}
 .top{display:flex;justify-content:space-between;gap:16px} .big{font-size:18px;font-weight:700} .sign{display:flex;justify-content:space-between;margin-top:70px} .sign div{border-top:1px solid #16211c;padding-top:6px;width:42%;text-align:center}
</style>
<div class="top"><div><h1>${esc(d.business.name || "Delivery note")}</h1><div class="muted">${headerInfo(d)}</div></div>
 <div style="text-align:right"><div class="big">DELIVERY NOTE</div><div>${esc(d.number)}</div><div class="muted">${esc(new Date(d.date).toLocaleDateString())}</div></div></div>
<div style="margin-top:14px">${customerBlock(d)}</div>
<table><thead><tr><th style="width:36px">#</th><th>Item</th><th class="r" style="width:120px">Quantity</th></tr></thead><tbody>${rows}</tbody></table>
${d.note ? `<div style="margin-top:12px" class="muted">${nl2br(d.note)}</div>` : ""}
<div class="sign"><div>Delivered by</div><div>Received by (name and signature)</div></div>`;
}

export function invoiceHtml(d: InvoiceData, template: InvoiceTemplate = DEFAULT_TEMPLATE): string {
  const inner = d.kind === "delivery" ? delivery(d) : template === "modern" ? modern(d) : template === "thermal" ? thermal(d) : classic(d);
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body>${inner}</body></html>`;
}

/** Page size in points (expo-print). Thermal rolls are 80 mm wide (~227 pt); A4 is the default. */
export function paperFor(t: InvoiceTemplate): { width?: number; height?: number } {
  return t === "thermal" ? { width: 227, height: 842 } : { width: 595, height: 842 }; // A4 (India), not the US Letter default
}

/** Pay status from money amounts. A zero-total invoice counts as paid. Credit notes lower what is owed; money received beyond that is
 *  reported as owed back to the customer (never silently dropped, never turned into a payment). */
export function paymentSummary(totalMinor: bigint, paidMinor: bigint, creditMinor = 0n): PaymentSummary {
  const owed = totalMinor - creditMinor > 0n ? totalMinor - creditMinor : 0n;
  const due = owed - paidMinor;
  const dueMinor = due > 0n ? due : 0n;
  const refundMinor = paidMinor > owed ? paidMinor - owed : 0n;
  return { paidMinor, dueMinor, status: dueMinor === 0n ? "PAID" : paidMinor > 0n ? "PARTIAL" : "UNPAID", ...(creditMinor > 0n ? { creditMinor, refundMinor } : {}) };
}

/** Realistic sample data, for the design previews in Settings. */
export function sampleInvoice(business: Profile, currency: string): InvoiceData {
  const lines: SaleLine[] = [
    { variantId: "s1", name: "Cotton shirt (M)", unitId: "", qty: "2", price: "525", discount: "0", gstRate: "5", hsn: "6205" },
    { variantId: "s2", name: "Denim jeans", unitId: "", qty: "1", price: "1180", discount: "80", gstRate: "12", hsn: "6203" },
    { variantId: "s3", name: "Leather belt", unitId: "", qty: "1", price: "590", discount: "0", gstRate: "18", hsn: "4203" },
  ];
  const calc = calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), "INCLUSIVE", false);
  return {
    number: `${business.invoicePrefix || "INV-"}0042`, date: Date.now(), business: { ...business, name: business.name || "Your Business Name" },
    customerName: "Aarav Sharma", customerPhone: "98765 43210", lines, calc, currency, payment: paymentSummary(calc.totalMinor, calc.totalMinor / 2n),
  };
}

export { minorToString };
