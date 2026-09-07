import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import layout from "../DashboardPage.module.css";

/** internal/modules/identity/domain's 7-scope list (APIScope) — the exact
 * set app.Service.CreateAPIKey validates against. */
const API_SCOPES = [
  { value: "products:read", label: "Products — read" },
  { value: "inventory:read", label: "Inventory — read" },
  { value: "customers:read", label: "Customers — read" },
  { value: "customers:write", label: "Customers — write" },
  { value: "invoices:read", label: "Invoices — read" },
  { value: "invoices:write", label: "Invoices — write" },
  { value: "reports:read", label: "Reports — read" },
] as const;

/** Only the webhook events a backend module actually fires today
 * (internal/modules/sales/app/service.go, internal/modules/einvoice/app/
 * service.go) — the brief's full event catalog is bigger, but listing
 * events nothing ever publishes would let someone "subscribe" to
 * something that silently never fires. */
const WEBHOOK_EVENTS = ["invoice.finalized", "einvoice.generated", "einvoice.failed"] as const;

interface APIKey {
  id: string;
  name: string;
  key_prefix: string;
  scopes: string[];
  expires_at?: string;
  last_used_at?: string;
  created_at: string;
}
interface WebhookEndpoint {
  id: string;
  url: string;
  subscribed_events: string[];
  is_active: boolean;
}

function CopyableSecret({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className={layout.panel} style={{ borderColor: "var(--color-warning)", background: "var(--color-warning-soft)" }}>
      <p style={{ marginTop: 0, fontWeight: 600 }}>{label} — shown only once. Copy it now; it can't be retrieved again.</p>
      <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
        <code style={{ flex: 1, padding: "8px 10px", background: "var(--color-surface)", borderRadius: "var(--radius-sm)", wordBreak: "break-all" }}>
          {value}
        </code>
        <button
          type="button"
          className={ui.btnSecondary}
          onClick={() => {
            void navigator.clipboard.writeText(value).then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 2000);
            });
          }}
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
    </div>
  );
}

function ApiKeysPanel() {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [expiresAt, setExpiresAt] = useState("");
  const [justCreatedKey, setJustCreatedKey] = useState<string | null>(null);

  const keys = useQuery({
    queryKey: ["api-keys"],
    queryFn: () => api.getListField<APIKey>("/api-keys", "api_keys"),
  });

  const create = useMutation({
    mutationFn: () =>
      api.post<{ key: string }>("/api-keys", {
        name,
        scopes,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined,
      }),
    onSuccess: (res) => {
      void queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      setJustCreatedKey(res.key);
      setName("");
      setScopes([]);
      setExpiresAt("");
      setShowForm(false);
    },
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/api-keys/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
  });

  function toggleScope(s: string) {
    setScopes((cur) => (cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s]));
  }

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>API keys</h2>
        <div className={ui.toolbarSpacer} />
        {!showForm ? (
          <button type="button" className={ui.btnPrimary} onClick={() => setShowForm(true)}>
            + New API key
          </button>
        ) : null}
      </div>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Lets an external app or script call the NodeDR Business API (<code>/api/v1/...</code>) on this business's behalf, scoped to
        only what you allow below.
      </p>

      {justCreatedKey ? <CopyableSecret label="API key" value={justCreatedKey} /> : null}

      {showForm ? (
        <div style={{ marginTop: 12, marginBottom: 20 }}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="apikey-name">Name</label>
              <input id="apikey-name" className={ui.input} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Website integration" />
            </div>
            <div className={ui.field}>
              <label htmlFor="apikey-expires">Expires (optional)</label>
              <input id="apikey-expires" type="date" className={ui.input} value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} />
            </div>
          </div>
          <fieldset style={{ border: "none", padding: 0, margin: "12px 0" }}>
            <legend className={ui.muted} style={{ marginBottom: 6 }}>
              Scopes
            </legend>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 12 }}>
              {API_SCOPES.map((s) => (
                <label key={s.value} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <input type="checkbox" checked={scopes.includes(s.value)} onChange={() => toggleScope(s.value)} />
                  {s.label}
                </label>
              ))}
            </div>
          </fieldset>
          <div className={ui.formActions}>
            <button type="button" className={ui.btnSecondary} onClick={() => setShowForm(false)}>
              Cancel
            </button>
            <button type="button" className={ui.btnPrimary} disabled={!name || scopes.length === 0 || create.isPending} onClick={() => create.mutate()}>
              {create.isPending ? "Creating…" : "Create key"}
            </button>
          </div>
          {create.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {create.error instanceof ApiError ? create.error.message : "Could not create this key."}
            </p>
          ) : null}
        </div>
      ) : null}

      {keys.isPending ? (
        <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />
      ) : keys.isError ? (
        <p className={layout.errorState} role="alert">
          Couldn't load API keys.
        </p>
      ) : (keys.data ?? []).length === 0 ? (
        <p className={layout.emptyState}>No API keys yet.</p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Key</th>
                <th scope="col">Scopes</th>
                <th scope="col">Last used</th>
                <th scope="col">Expires</th>
                <th scope="col" />
              </tr>
            </thead>
            <tbody>
              {(keys.data ?? []).map((k) => (
                <tr key={k.id}>
                  <td>{k.name}</td>
                  <td>
                    <code>{k.key_prefix}…</code>
                  </td>
                  <td>{k.scopes.join(", ")}</td>
                  <td>{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : "Never"}</td>
                  <td>{k.expires_at ? new Date(k.expires_at).toLocaleDateString() : "Never"}</td>
                  <td>
                    <button type="button" className={ui.btnDanger} disabled={revoke.isPending} onClick={() => revoke.mutate(k.id)}>
                      Revoke
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function WebhooksPanel() {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [url, setUrl] = useState("");
  const [events, setEvents] = useState<string[]>([]);
  const [justCreatedSecret, setJustCreatedSecret] = useState<string | null>(null);

  const endpoints = useQuery({
    queryKey: ["webhook-endpoints"],
    queryFn: () => api.getListField<WebhookEndpoint>("/webhooks/endpoints", "endpoints"),
  });

  const register = useMutation({
    mutationFn: () => api.post<{ signing_secret: string }>("/webhooks/endpoints", { url, subscribed_events: events }),
    onSuccess: (res) => {
      void queryClient.invalidateQueries({ queryKey: ["webhook-endpoints"] });
      setJustCreatedSecret(res.signing_secret);
      setUrl("");
      setEvents([]);
      setShowForm(false);
    },
  });

  const deactivate = useMutation({
    mutationFn: (id: string) => api.delete(`/webhooks/endpoints/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["webhook-endpoints"] }),
  });

  function toggleEvent(e: string) {
    setEvents((cur) => (cur.includes(e) ? cur.filter((x) => x !== e) : [...cur, e]));
  }

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>Webhooks</h2>
        <div className={ui.toolbarSpacer} />
        {!showForm ? (
          <button type="button" className={ui.btnPrimary} onClick={() => setShowForm(true)}>
            + New webhook
          </button>
        ) : null}
      </div>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Notifies your own server (HMAC-signed, so you can verify it really came from here) when something happens in this business.
      </p>

      {justCreatedSecret ? <CopyableSecret label="Signing secret" value={justCreatedSecret} /> : null}

      {showForm ? (
        <div style={{ marginTop: 12, marginBottom: 20 }}>
          <div className={ui.field} style={{ marginBottom: 12 }}>
            <label htmlFor="webhook-url">Endpoint URL</label>
            <input id="webhook-url" className={ui.input} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://yourapp.example.com/webhooks/rechvix" />
          </div>
          <fieldset style={{ border: "none", padding: 0, margin: "0 0 12px" }}>
            <legend className={ui.muted} style={{ marginBottom: 6 }}>
              Events
            </legend>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 12 }}>
              {WEBHOOK_EVENTS.map((e) => (
                <label key={e} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <input type="checkbox" checked={events.includes(e)} onChange={() => toggleEvent(e)} />
                  <code>{e}</code>
                </label>
              ))}
            </div>
          </fieldset>
          <div className={ui.formActions}>
            <button type="button" className={ui.btnSecondary} onClick={() => setShowForm(false)}>
              Cancel
            </button>
            <button type="button" className={ui.btnPrimary} disabled={!url || events.length === 0 || register.isPending} onClick={() => register.mutate()}>
              {register.isPending ? "Registering…" : "Register webhook"}
            </button>
          </div>
          {register.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {register.error instanceof ApiError ? register.error.message : "Could not register this webhook."}
            </p>
          ) : null}
        </div>
      ) : null}

      {endpoints.isPending ? (
        <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />
      ) : endpoints.isError ? (
        <p className={layout.errorState} role="alert">
          Couldn't load webhooks.
        </p>
      ) : (endpoints.data ?? []).length === 0 ? (
        <p className={layout.emptyState}>No webhooks registered yet.</p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">URL</th>
                <th scope="col">Events</th>
                <th scope="col">Status</th>
                <th scope="col" />
              </tr>
            </thead>
            <tbody>
              {(endpoints.data ?? []).map((e) => (
                <tr key={e.id}>
                  <td style={{ wordBreak: "break-all" }}>{e.url}</td>
                  <td>{e.subscribed_events.join(", ")}</td>
                  <td>
                    <span className={ui.badge} data-tone={e.is_active ? "positive" : "neutral"}>
                      {e.is_active ? "Active" : "Deactivated"}
                    </span>
                  </td>
                  <td>
                    {e.is_active ? (
                      <button type="button" className={ui.btnDanger} disabled={deactivate.isPending} onClick={() => deactivate.mutate(e.id)}>
                        Deactivate
                      </button>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export function IntegrationsPage() {
  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Integrations</h1>
          <p className={layout.subtitle}>API keys and webhooks for connecting your own website or software to this business.</p>
        </div>
      </div>
      <ApiKeysPanel />
      <WebhooksPanel />
    </div>
  );
}
