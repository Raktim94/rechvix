import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

interface Provider { provider: "STRIPE" | "PAYPAL"; enabled: boolean; live: boolean; client_id?: string; has_secret: boolean; has_webhook_secret: boolean }
interface PayLink { id: string; provider: string; url: string; amount: string; currency: string; status: string; paid_at?: string; note?: string; created_at: string }

function ProviderForm({ p, webhookUrl }: { p: Provider; webhookUrl: string }) {
  const qc = useQueryClient();
  const [enabled, setEnabled] = useState(p.enabled);
  const [live, setLive] = useState(p.live);
  const [clientId, setClientId] = useState(p.client_id ?? "");
  const [secret, setSecret] = useState("");
  const [hook, setHook] = useState("");
  const save = useMutation({
    mutationFn: () => api.put(`/payments/providers/${p.provider}`, { enabled, live, client_id: clientId, secret, webhook_secret: hook }),
    onSuccess: () => { setSecret(""); setHook(""); void qc.invalidateQueries({ queryKey: ["payment-providers"] }); },
  });
  const stripe = p.provider === "STRIPE";
  const id = p.provider.toLowerCase();
  return (
    <form onSubmit={(e) => { e.preventDefault(); save.mutate(); }} style={{ marginBottom: 16 }}>
      <h3>{stripe ? "Card payments (Stripe)" : "PayPal"}</h3>
      <div className={ui.formGrid}>
        {!stripe ? <div className={ui.field}><label htmlFor={`${id}-client`}>Client ID</label><input id={`${id}-client`} className={ui.input} value={clientId} autoComplete="off" onChange={(e) => setClientId(e.target.value)} /></div> : null}
        <div className={ui.field}>
          <label htmlFor={`${id}-secret`}>{stripe ? "Secret key" : "Client secret"}</label>
          <input id={`${id}-secret`} type="password" className={ui.input} value={secret} autoComplete="new-password" placeholder={p.has_secret ? "Saved. Type a new one to replace it." : ""} onChange={(e) => setSecret(e.target.value)} />
        </div>
        {stripe ? (
          <div className={ui.field}>
            <label htmlFor="stripe-hook">Webhook signing secret</label>
            <input id="stripe-hook" type="password" className={ui.input} value={hook} autoComplete="new-password" placeholder={p.has_webhook_secret ? "Saved. Type a new one to replace it." : "whsec_…"} onChange={(e) => setHook(e.target.value)} />
          </div>
        ) : null}
      </div>
      {stripe ? <p className={ui.muted}>In Stripe, add a webhook endpoint at <code>{webhookUrl}</code> for the event <code>checkout.session.completed</code>, then paste its signing secret above. Payments are recorded only when Stripe confirms them through this webhook.</p> : <p className={ui.muted}>PayPal payments are captured when the customer returns from PayPal.</p>}
      <label style={{ display: "flex", gap: 8, alignItems: "center", margin: "4px 0" }}><input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Allow payment links for this provider</label>
      {!stripe ? <label style={{ display: "flex", gap: 8, alignItems: "center", margin: "4px 0" }}><input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} /> Live account (leave off to test in PayPal's sandbox)</label> : null}
      <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
        <button type="submit" className={ui.btnPrimary} disabled={save.isPending}>{save.isPending ? "Saving…" : "Save"}</button>
        {save.isSuccess ? <span role="status">Saved.</span> : null}
      </div>
      {save.isError ? <p role="alert">{save.error instanceof ApiError ? save.error.message : "Could not save."}</p> : null}
    </form>
  );
}

/** Settings: connect Stripe and PayPal so invoices can be paid online. Secrets are stored encrypted and never shown again. */
export function OnlinePaymentsSettings({ organisationId }: { organisationId: string }) {
  const q = useQuery({ queryKey: ["payment-providers"], queryFn: () => api.getListField<Provider>("/payments/providers", "providers") });
  if (!q.data) return null;
  const webhookUrl = `${window.location.origin}/api/v1/payments/stripe/webhook/${organisationId}`;
  return (
    <section className={layout.panel} aria-labelledby="onlinepay-heading">
      <h2 id="onlinepay-heading">Online payments</h2>
      <p className={ui.muted}>Let customers pay invoices by card or PayPal from a link. A confirmed payment is recorded on the invoice for you. Only invoices in your company currency can be paid online.</p>
      {q.data.map((p) => <ProviderForm key={p.provider + String(p.enabled) + String(p.has_secret)} p={p} webhookUrl={webhookUrl} />)}
    </section>
  );
}

/** On an invoice: create a payment link and see whether it was paid. */
export function PaymentLinksPanel({ documentId, outstanding }: { documentId: string; outstanding: boolean }) {
  const qc = useQueryClient();
  const key = ["payment-links", documentId];
  const providers = useQuery({ queryKey: ["payment-providers"], queryFn: () => api.getListField<Provider>("/payments/providers", "providers") });
  const links = useQuery({ queryKey: key, queryFn: () => api.getListField<PayLink>(`/sales/documents/${documentId}/payment-links`, "links") });
  const create = useMutation({ mutationFn: (provider: string) => api.post<PayLink>(`/sales/documents/${documentId}/payment-links`, { provider }), onSuccess: () => void qc.invalidateQueries({ queryKey: key }) });
  const enabled = (providers.data ?? []).filter((p) => p.enabled);
  if (enabled.length === 0 && (links.data ?? []).length === 0) return null;
  return (
    <div style={{ marginTop: 16 }}>
      <h3>Pay online</h3>
      {outstanding ? (
        <div className={ui.formActions} style={{ justifyContent: "flex-start", gap: 8, flexWrap: "wrap" }}>
          {enabled.map((p) => <button key={p.provider} type="button" className={ui.btnSecondary} disabled={create.isPending} onClick={() => create.mutate(p.provider)}>{p.provider === "STRIPE" ? "Create card payment link" : "Create PayPal link"}</button>)}
        </div>
      ) : null}
      {create.isError ? <p role="alert">{create.error instanceof ApiError ? create.error.message : "Could not create the link."}</p> : null}
      {links.data && links.data.length > 0 ? (
        <ul style={{ listStyle: "none", padding: 0, margin: "8px 0 0", display: "flex", flexDirection: "column", gap: 6 }}>
          {links.data.map((l) => (
            <li key={l.id}>
              <span className={ui.badge} data-tone={l.status === "PAID" ? "positive" : "warning"}>{l.status.toLowerCase()}</span>{" "}
              {l.provider === "STRIPE" ? "Card" : "PayPal"} · {l.amount} {l.currency}
              {l.status === "PENDING" ? <> · <input readOnly value={l.url} aria-label={`${l.provider} payment link`} className={ui.input} onFocus={(e) => e.currentTarget.select()} style={{ width: "100%", maxWidth: 420 }} /></> : null}
              {l.paid_at ? ` · paid ${new Date(l.paid_at).toLocaleString()}` : ""}
              {l.note ? <span role="alert"> · {l.note}</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
