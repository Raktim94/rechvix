import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError, apiUrl } from "../../lib/api-client";
import layout from "../DashboardPage.module.css";

interface Doc { id: string; number: string; type: string; date: string; total: string; currency: string; outstanding?: string }
interface Portal { customer: string; shop: string; documents: Doc[]; can_pay: boolean }

const TYPE: Record<string, string> = { QUOTATION: "Quotation", PROFORMA_INVOICE: "Proforma invoice", SALES_ORDER: "Order", DELIVERY_CHALLAN: "Delivery note", TAX_INVOICE: "Invoice", POS_INVOICE: "Receipt", CREDIT_NOTE: "Credit note", DEBIT_NOTE: "Debit note" };
const SIGNABLE = new Set(["QUOTATION", "PROFORMA_INVOICE", "SALES_ORDER", "DELIVERY_CHALLAN"]);

function SignIn({ slug }: { slug: string }) {
  const [email, setEmail] = useState("");
  const ask = useMutation({ mutationFn: () => api.post<{ status: string }>(`/shop/${slug}/portal/request-link`, { email }) });
  const expired = new URLSearchParams(window.location.search).get("expired");
  return (
    <main className={layout.page} style={{ maxWidth: 560, margin: "0 auto" }}>
      <h1>Your client area</h1>
      {expired ? <p role="alert">That link has expired or was already used. Ask for a new one.</p> : null}
      <p>Enter the email address we have for you and we will send a one-time sign-in link. There is no password to remember.</p>
      <form onSubmit={(e) => { e.preventDefault(); ask.mutate(); }}>
        <div className={ui.field}><label htmlFor="p-email">Email</label><input id="p-email" type="email" className={ui.input} autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} /></div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="submit" className={ui.btnPrimary} disabled={!email.includes("@") || ask.isPending}>Email me a link</button></div>
        {ask.isSuccess ? <p role="status">{ask.data.status}</p> : null}
        {ask.isError ? <p role="alert">{ask.error instanceof ApiError ? ask.error.message : "Could not send the link."}</p> : null}
      </form>
      <p><a href={`/shop/${slug}`}>Back to the shop</a></p>
    </main>
  );
}

/** The signed-in customer's documents, with the ways to act on them: open, pay, sign. */
export function PortalPage({ slug }: { slug: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["portal", slug], queryFn: () => api.get<Portal>(`/shop/${slug}/portal`), retry: false });
  const go = useMutation({
    mutationFn: (v: { id: string; action: "pay-card" | "pay-paypal" | "sign" }) =>
      v.action === "sign" ? api.post<{ url: string }>(`/shop/${slug}/portal/documents/${v.id}/sign`) : api.post<{ url: string }>(`/shop/${slug}/portal/documents/${v.id}/pay`, { provider: v.action === "pay-card" ? "STRIPE" : "PAYPAL" }),
    onSuccess: (r) => { window.location.assign(r.url); },
  });
  const signout = useMutation({ mutationFn: () => api.post(`/shop/${slug}/portal/signout`), onSuccess: () => void qc.invalidateQueries({ queryKey: ["portal", slug] }) });
  if (q.isPending) return <main className={layout.page}><p>Loading…</p></main>;
  if (q.isError || !q.data) return <SignIn slug={slug} />;
  const p = q.data;
  return (
    <main className={layout.page} style={{ maxWidth: 1000, margin: "0 auto" }}>
      <h1>{p.shop}</h1>
      <p>Signed in as <strong>{p.customer}</strong>. <button type="button" className={ui.btnGhost} onClick={() => signout.mutate()}>Sign out</button> · <a href={`/shop/${slug}`}>Shop</a></p>
      {p.documents.length === 0 ? <p className={layout.emptyState}>You have no documents yet.</p> : (
        <div className={ui.tableScroll} tabIndex={0}>
          <table className={ui.table}>
            <caption className="srOnly">Your documents</caption>
            <thead><tr><th scope="col">Document</th><th scope="col">Date</th><th scope="col">Total</th><th scope="col">To pay</th><th scope="col"><span className="srOnly">Actions</span></th></tr></thead>
            <tbody>
              {p.documents.map((d) => (
                <tr key={d.id}>
                  <td>{TYPE[d.type] ?? d.type} {d.number}</td><td>{d.date}</td><td>{d.total} {d.currency}</td><td>{d.outstanding ? `${d.outstanding} ${d.currency}` : ""}</td>
                  <td style={{ whiteSpace: "nowrap" }}>
                    <a className={ui.btnGhost} href={apiUrl(`/shop/${slug}/portal/documents/${d.id}/file`)} target="_blank" rel="noreferrer">Open<span className="srOnly"> {d.number} (opens in a new tab)</span></a>
                    {d.outstanding && p.can_pay ? <> <button type="button" className={ui.btnSecondary} disabled={go.isPending} onClick={() => go.mutate({ id: d.id, action: "pay-card" })}>Pay by card</button> <button type="button" className={ui.btnSecondary} disabled={go.isPending} onClick={() => go.mutate({ id: d.id, action: "pay-paypal" })}>PayPal</button></> : null}
                    {SIGNABLE.has(d.type) ? <> <button type="button" className={ui.btnSecondary} disabled={go.isPending} onClick={() => go.mutate({ id: d.id, action: "sign" })}>Sign</button></> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {go.isError ? <p role="alert">{go.error instanceof ApiError ? go.error.message : "Could not do that."}</p> : null}
    </main>
  );
}
