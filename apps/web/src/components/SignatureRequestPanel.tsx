import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import ui from "./ui.module.css";

interface Req { id: string; status: string; requested_name?: string; requested_email?: string; signer_name?: string; signed_at?: string; signer_ip?: string; document_hash: string; has_drawing: boolean; decline_reason?: string; expires_at: string; created_at: string }

/** Ask a customer to sign a document electronically, and see who signed, when, and the evidence kept. */
export function SignatureRequestPanel({ documentId }: { documentId: string }) {
  const qc = useQueryClient();
  const key = ["signature-requests", documentId];
  const list = useQuery({ queryKey: key, queryFn: () => api.getListField<Req>(`/sales/documents/${documentId}/signature-requests`, "requests") });
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [link, setLink] = useState("");
  const create = useMutation({
    mutationFn: () => api.post<{ link: string }>(`/sales/documents/${documentId}/signature-requests`, { name, email }),
    onSuccess: (r) => { setLink(r.link); setName(""); setEmail(""); void qc.invalidateQueries({ queryKey: key }); },
  });
  const revoke = useMutation({ mutationFn: (id: string) => api.delete(`/signature-requests/${id}`), onSuccess: () => void qc.invalidateQueries({ queryKey: key }) });
  return (
    <div style={{ marginTop: 16 }}>
      <h3>Electronic signature</h3>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}><label htmlFor="sig-name">Signer's name (optional)</label><input id="sig-name" className={ui.input} value={name} onChange={(e) => setName(e.target.value)} /></div>
          <div className={ui.field}><label htmlFor="sig-email">Email the link to (optional)</label><input id="sig-email" type="email" className={ui.input} value={email} onChange={(e) => setEmail(e.target.value)} /></div>
        </div>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
          <button type="submit" className={ui.btnSecondary} disabled={create.isPending}>{create.isPending ? "Creating…" : "Ask for a signature"}</button>
        </div>
        {create.isError ? <p role="alert">{create.error instanceof ApiError ? create.error.message : "Could not create the request."}</p> : null}
      </form>
      {link ? (
        <p role="status">Signing link (shown once, valid 14 days): <input readOnly value={link} className={ui.input} onFocus={(e) => e.currentTarget.select()} aria-label="Signing link" style={{ width: "100%", maxWidth: 520 }} /></p>
      ) : null}
      {list.data && list.data.length > 0 ? (
        <ul style={{ listStyle: "none", padding: 0, margin: "8px 0 0", display: "flex", flexDirection: "column", gap: 6 }}>
          {list.data.map((r) => (
            <li key={r.id} style={{ display: "flex", gap: 10, alignItems: "baseline", flexWrap: "wrap" }}>
              <span className={ui.badge} data-tone={r.status === "SIGNED" ? "positive" : r.status === "PENDING" ? "warning" : "neutral"}>{r.status.toLowerCase()}</span>
              <span>{r.status === "SIGNED" ? `${r.signer_name} signed ${r.signed_at ? new Date(r.signed_at).toLocaleString() : ""}${r.has_drawing ? " (drawn)" : ""}${r.signer_ip ? ` from ${r.signer_ip}` : ""}` : r.status === "DECLINED" ? `Declined${r.decline_reason ? `: ${r.decline_reason}` : ""}` : `Waiting${r.requested_name ? ` for ${r.requested_name}` : ""}, expires ${new Date(r.expires_at).toLocaleDateString()}`}</span>
              <span className={ui.muted} title="SHA-256 fingerprint of the document content they were shown">ref {r.document_hash.slice(0, 12)}</span>
              {r.status === "PENDING" ? <button type="button" className={ui.btnGhost} onClick={() => revoke.mutate(r.id)}>Withdraw</button> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
