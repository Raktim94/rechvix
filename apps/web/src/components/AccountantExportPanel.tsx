import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, apiUrl } from "../lib/api-client";
import type { LegalEntity } from "../lib/useOrgContext";
import ui from "./ui.module.css";
import layout from "../pages/DashboardPage.module.css";

interface ExportReview {
 status: string;
 document_count: number;
 snapshot_at: string;
 checks: { severity: string; code: string; document?: string; message: string }[];
 boxes: { code: string; label: string; amount: string; status: string; note: string }[];
}
function localDate(d: Date): string {
 return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`;
}
function previousMonth(): {from: string; to: string} {
 const now = new Date();
 return { from: localDate(new Date(now.getFullYear(), now.getMonth()-1, 1)), to: localDate(new Date(now.getFullYear(), now.getMonth(), 0)) };
}

/** No government credentials or submission action: downloads stay within the
 * authenticated company and all generated files carry the same review checks. */
export function AccountantExportPanel({ company }: { company: LegalEntity }) {
 const initial = previousMonth();
 const [from,setFrom] = useState(initial.from);
 const [to,setTo] = useState(initial.to);
 const query = new URLSearchParams({legal_entity_id:company.ID,from,to});
 const path = `/reports/tax/accountant-package?${query}`;
 const review = useMutation({mutationFn:()=>api.get<ExportReview>(path)});
 const country = company.CountryCode || "IN";
 if (!["IN","ES","GB","US"].includes(country)) return null;
 const errors = review.data?.checks.filter(c=>c.severity==="ERROR") ?? [];
 const updateFrom = (v:string)=>{setFrom(v);review.reset();};
 const updateTo = (v:string)=>{setTo(v);review.reset();};
 return <section className={layout.panel} aria-labelledby="accountant-export-heading">
  <h2 id="accountant-export-heading">Files for your accountant</h2>
  <p className={ui.muted}>{company.LegalName}: sales, purchases, tax calculations, credit notes, reconciliation and {country==="ES" ? "Modelo 303 worksheets" : country==="GB" ? "VAT return worksheets" : country==="IN" ? "GST worksheets and eligible offline-tool CSVs" : "state-by-state sales-tax schedules"}. No return is submitted.</p>
  <form onSubmit={e=>{e.preventDefault();review.mutate();}}>
   <div className={ui.formGrid}>
    <div className={ui.field}><label htmlFor="accountant-from">Period start</label><input id="accountant-from" type="date" className={ui.input} required min="2026-01-01" max={to||"2026-12-31"} value={from} disabled={review.isPending} onChange={e=>updateFrom(e.target.value)} /></div>
    <div className={ui.field}><label htmlFor="accountant-to">Period end</label><input id="accountant-to" type="date" className={ui.input} required min={from||"2026-01-01"} max="2026-12-31" value={to} disabled={review.isPending} onChange={e=>updateTo(e.target.value)} /></div>
   </div>
   <div className={ui.formActions} style={{ flexWrap: "wrap", justifyContent: "flex-start", gap: 8 }}>
    <button type="submit" className={ui.btnPrimary} disabled={!from||!to||from>to||review.isPending}>{review.isPending ? "Checking records…" : "Check accountant package"}</button>
    {review.data ? <><a className={ui.btnPrimary} href={apiUrl(path+"&format=zip")}>Download CA package</a><a className={ui.btnSecondary} href={apiUrl(path+"&format=xlsx")}>Workbook only</a></> : null}
   </div>
  </form>
  {review.isError ? <p role="alert">{review.error instanceof Error ? review.error.message : "Could not prepare the package. Try again."}</p> : null}
  {review.data ? <div>
   <p role="status">{review.data.document_count} finalized documents · {errors.length ? `${errors.length} issues need attention` : "Ready for accountant review"}. Files include the review checklist.</p>
   {country==="IN"&&errors.length ? <p role="alert">GST import CSVs are withheld while errors remain. The workbook and source registers still include all records for your CA to resolve.</p> : null}
   <details><summary>Review checks ({review.data.checks.length})</summary><ul>{review.data.checks.map((c,i)=><li key={`${c.code}-${i}`}><strong>{c.severity}{c.document ? ` · ${c.document}` : ""}:</strong> {c.message}</li>)}</ul></details>
   <details><summary>Calculated filing worksheet</summary><div className={ui.tableScroll} tabIndex={0}><table className={ui.table}><thead><tr><th scope="col">Field</th><th scope="col">Description</th><th scope="col">Recorded calculation</th></tr></thead><tbody>{review.data.boxes.map(b=><tr key={b.code}><td>{b.code}</td><td>{b.label}</td><td>{b.amount||"CA input required"}</td></tr>)}</tbody></table></div></details>
  </div> : null}
 </section>;
}
