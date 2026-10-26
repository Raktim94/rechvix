import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

interface SyncStatus {
  linked: boolean;
  remote_url?: string;
  enabled: boolean;
  last_sync_at?: string;
  last_error?: string;
  last_pushed: number;
  last_pulled: number;
  last_conflicts: number;
  waiting: number;
}

const errorText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

/** Keeps products and contacts identical between this install and a central Rechvix server. Sales, stock and accounting stay on the device that made them for now. */
export function SyncPanel() {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["sync"], queryFn: () => api.get<SyncStatus>("/sync/status"), refetchInterval: 15000 });
  const [url, setUrl] = useState("");
  const [key, setKey] = useState("");
  const done = (d: SyncStatus) => qc.setQueryData(["sync"], d);
  const link = useMutation({ mutationFn: () => api.put<SyncStatus>("/sync/link", { url, api_key: key }), onSuccess: (d) => { done(d); setKey(""); } });
  const now = useMutation({ mutationFn: () => api.post<SyncStatus>("/sync/now", {}), onSuccess: done });
  const unlink = useMutation({ mutationFn: () => api.delete("/sync/link"), onSuccess: () => { setUrl(""); void qc.invalidateQueries({ queryKey: ["sync"] }); } });
  const s = status.data;
  if (!s) return null;

  return (
    <section className={layout.panel} aria-labelledby="sync-heading">
      <h2 id="sync-heading">Sync with your server</h2>
      <p className={ui.muted}>
        Products, units, brands and contacts are copied both ways between this install and your central Rechvix server, so you can work offline and stay in step. Sales, stock and accounting are not synced yet.
      </p>
      {s.linked ? (
        <>
          <dl className={ui.muted} style={{ margin: "12px 0" }}>
            <div><strong>Server:</strong> {s.remote_url}</div>
            <div><strong>Last sync:</strong> {s.last_sync_at ? new Date(s.last_sync_at).toLocaleString() : "not yet"}</div>
            <div><strong>Last round:</strong> {s.last_pushed} sent, {s.last_pulled} received{s.last_conflicts ? `, ${s.last_conflicts} could not be applied` : ""}</div>
            <div><strong>Waiting to send:</strong> {s.waiting}</div>
          </dl>
          {s.last_error ? <p role="alert" style={{ color: "var(--color-negative)" }}>{s.last_error}</p> : null}
          {s.last_conflicts ? <p role="status">Some items could not be applied, usually because the other side already has a different record with the same code or barcode. Rename one of them and the next sync will carry it over.</p> : null}
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <button type="button" className={ui.btnPrimary} disabled={now.isPending} onClick={() => now.mutate()}>{now.isPending ? "Syncing…" : "Sync now"}</button>
            <button type="button" className={ui.btnSecondary} disabled={unlink.isPending} onClick={() => unlink.mutate()}>Disconnect</button>
          </div>
          {now.isError ? <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>{errorText(now.error, "Could not sync.")}</p> : null}
        </>
      ) : (
        <form onSubmit={(e) => { e.preventDefault(); link.mutate(); }}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="sync-url">Server address</label>
              <input id="sync-url" className={ui.input} inputMode="url" placeholder="https://rechvix.example.com" value={url} onChange={(e) => setUrl(e.target.value)} required />
            </div>
            <div className={ui.field}>
              <label htmlFor="sync-key">API key</label>
              <input id="sync-key" type="password" autoComplete="off" className={ui.input} value={key} onChange={(e) => setKey(e.target.value)} required />
              <p className={ui.muted}>On the server: Integrations, API keys, new key with the scope <code>sync:readwrite</code>.</p>
            </div>
          </div>
          <button type="submit" className={ui.btnPrimary} disabled={link.isPending}>{link.isPending ? "Connecting…" : "Connect"}</button>
          {link.isError ? <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>{errorText(link.error, "Could not connect.")}</p> : null}
        </form>
      )}
    </section>
  );
}
