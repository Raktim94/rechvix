import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import ui from "./ui.module.css";

/** Emails a document to a customer as a secure link. The share-links list below shows when the customer opens it. */
export function EmailDocumentPanel({ documentType, documentId, subject, defaultTo }: { documentType: string; documentId: string; subject: string; defaultTo?: string }) {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["email-status"], queryFn: () => api.get<{ configured: boolean }>("/notifications/email-status") });
  const [to, setTo] = useState(defaultTo ?? "");
  const [message, setMessage] = useState("");
  const send = useMutation({
    mutationFn: () => api.post("/notifications/email-document", { document_type: documentType, document_id: documentId, to, subject, message }),
    onSuccess: () => {
      setMessage("");
      void qc.invalidateQueries({ queryKey: ["share-links", documentType, documentId] });
    },
  });
  if (status.isPending) return null;
  if (!status.data?.configured) {
    return <p className={ui.muted} style={{ marginTop: 16 }}>Email is not set up on this server (an administrator sets SMTP_HOST), so documents can be shared by link or WhatsApp only.</p>;
  }
  return (
    <form
      style={{ marginTop: 16 }}
      onSubmit={(e) => {
        e.preventDefault();
        send.mutate();
      }}
    >
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="email-doc-to">Email this document to</label>
          <input id="email-doc-to" type="email" required className={ui.input} value={to} onChange={(e) => setTo(e.target.value)} autoComplete="off" />
        </div>
        <div className={ui.field}>
          <label htmlFor="email-doc-msg">Message (optional)</label>
          <input id="email-doc-msg" className={ui.input} value={message} maxLength={1000} onChange={(e) => setMessage(e.target.value)} />
        </div>
      </div>
      <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
        <button type="submit" className={ui.btnSecondary} disabled={!to || send.isPending}>{send.isPending ? "Queuing…" : "Send by email"}</button>
        {send.isSuccess ? <span role="status">Queued. It is sent in the background.</span> : null}
      </div>
      {send.isError ? <p role="alert">{send.error instanceof ApiError ? send.error.message : "Could not queue the email."}</p> : null}
    </form>
  );
}
