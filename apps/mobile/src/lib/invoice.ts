import type { Profile, SaleLine } from "../db/repo";
import { calcTotals } from "./tax";
import { formatMoney, lineAmount, num, round2 } from "./money";

export interface InvoiceData {
  number: string;
  date: number;
  business: Profile;
  customerName: string;
  customerGstin?: string;
  lines: SaleLine[];
  paymentMethod: string | null;
  currency: string;
  /** Server-issued figures once synced; otherwise computed here. */
  serverTotal?: string | null;
  payments?: { amount: string; method: string; recordedAt: number }[];
  theme?: "classic" | "clean" | "bold";
}

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);

/** Intra-state supply splits GST into CGST + SGST; a customer in another state gets IGST. Unknown state: assume intra-state. */
export function gstSplit(business: Profile, customerGstin?: string): "CGST_SGST" | "IGST" {
  const bs = business.gstState || business.gstin.slice(0, 2);
  const cs = (customerGstin ?? "").slice(0, 2);
  return business.country === "IN" && bs && cs && bs !== cs ? "IGST" : "CGST_SGST";
}

/** A plain, print-safe tax invoice. No images, no web fonts: it must render identically in any PDF engine. */
export function invoiceHtml(d: InvoiceData): string {
  const t = calcTotals(d.lines);
  const m = (n: number) => esc(formatMoney(n, d.currency));
  const theme = d.theme ?? d.business.invoiceTheme ?? "classic";
  const themeColors = { classic: "#0f6e5c", clean: "#2454a6", bold: "#153f2e" } as const;
  const themeCss = theme === "clean" ? "th{border-bottom:1px solid #c9d6e8}td{border-bottom:1px solid #e7edf5}.grand td{border-top:1px solid #2454a6}" : theme === "bold" ? "h1{font-size:26px;text-transform:uppercase;letter-spacing:.4px}th{background:#153f2e;color:#fff;padding:9px 5px}.grand td{background:#eef4ef}" : "th{border-bottom:2px solid #16211c}";
  const hasInclusivePrice = d.lines.some((l) => l.grossPrice != null);
  const lineTotal = (l: SaleLine) => l.grossPrice != null ? round2(num(l.qty) * num(l.grossPrice) - num(l.discount)) : lineAmount(l);
  const split = gstSplit(d.business, d.customerGstin);
  const taxRows = t.byRate.filter((r) => r.rate > 0).map((r) =>
    split === "IGST"
      ? `<tr><td>IGST @ ${r.rate}%</td><td class="r">${m(r.tax)}</td></tr>`
      : `<tr><td>CGST @ ${r.rate / 2}%</td><td class="r">${m(r.tax / 2)}</td></tr><tr><td>SGST @ ${r.rate / 2}%</td><td class="r">${m(r.tax / 2)}</td></tr>`).join("");
  const rows = d.lines.map((l, i) => `<tr><td>${i + 1}</td><td>${esc(l.name)}</td><td class="r">${esc(l.qty)}</td><td class="r">${m(num(l.grossPrice ?? l.price))}</td><td class="r">${l.gstRate ? esc(l.gstRate) + "%" : "–"}</td><td class="r">${m(lineTotal(l))}</td></tr>`).join("");
  const total = d.serverTotal ? num(d.serverTotal) : t.total;
  const paid = d.paymentMethod ? total : (d.payments ?? []).reduce((sum, payment) => sum + num(payment.amount), 0);
  const due = Math.max(0, round2(total - paid));
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<style>
 body{font-family:-apple-system,Roboto,Helvetica,Arial,sans-serif;color:#16211c;margin:28px;font-size:13px}
 h1{font-size:22px;margin:0 0 2px;color:${themeColors[theme]}} .muted{color:#5b6b62} .row{display:flex;justify-content:space-between;gap:24px}
 table{width:100%;border-collapse:collapse;margin-top:18px} th{font-size:11px;text-transform:uppercase;letter-spacing:.6px;color:#5b6b62;text-align:left;border-bottom:2px solid #16211c;padding:6px 4px}
 td{padding:8px 4px;border-bottom:1px solid #dbdfd8;vertical-align:top} .r{text-align:right;font-variant-numeric:tabular-nums} th.r{text-align:right}
 .totals{width:55%;margin-left:auto;margin-top:12px} .totals td{border:0;padding:4px} .grand td{border-top:2px solid #16211c;font-size:16px;font-weight:700}
 .foot{margin-top:36px;font-size:11px;color:#5b6b62}
 ${themeCss}
</style></head><body>
<div class="row"><div><h1>${esc(d.business.name || "Tax invoice")}</h1>
 <div class="muted">${esc(d.business.address)}${d.business.phone ? "<br>Phone " + esc(d.business.phone) : ""}${d.business.gstin ? "<br>GSTIN " + esc(d.business.gstin) : ""}</div></div>
 <div style="text-align:right"><div style="font-size:16px;font-weight:700">TAX INVOICE</div><div>${esc(d.number)}</div><div class="muted">${esc(new Date(d.date).toLocaleDateString())}</div></div></div>
<div style="margin-top:18px"><div class="muted" style="font-size:11px;text-transform:uppercase;letter-spacing:.6px">Billed to</div><div style="font-size:15px;font-weight:600">${esc(d.customerName)}</div>${d.customerGstin ? `<div class="muted">GSTIN ${esc(d.customerGstin)}</div>` : ""}</div>
<table><thead><tr><th>#</th><th>Item</th><th class="r">Qty</th><th class="r">${hasInclusivePrice ? "Unit price (incl. GST)" : "Rate"}</th><th class="r">GST</th><th class="r">Amount</th></tr></thead><tbody>${rows}</tbody></table>
<table class="totals"><tr><td>${hasInclusivePrice ? "Taxable subtotal" : "Subtotal"}</td><td class="r">${m(t.subtotal)}</td></tr>${taxRows}<tr class="grand"><td>Total</td><td class="r">${m(total)}</td></tr>
<tr><td class="muted">Payment</td><td class="r muted">${d.paymentMethod ? esc(d.paymentMethod.toLowerCase()) : "credit"}</td></tr><tr><td class="muted">Paid</td><td class="r muted">${m(paid)}</td></tr><tr><td class="muted">Balance due</td><td class="r muted">${m(due)}</td></tr></table>
<div class="foot">Thank you for your business.${d.business.name ? "" : ""}</div></body></html>`;
}
