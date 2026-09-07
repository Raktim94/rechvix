import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { EwayBillCard } from "../../components/EwayBillCard";
import ui from "../../components/ui.module.css";
import { api, apiUrl, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import layout from "../DashboardPage.module.css";
import styles from "./SalesDetailPage.module.css";
import { DOCUMENT_TYPE_LABELS, EWB_ELIGIBLE_TYPES, type SalesDocument, type SalesDocumentLine } from "./types";

/** A WhatsApp "click to chat" deep link (`wa.me`) — no WhatsApp Business
 * API credential needed, works for any customer with a saved phone
 * number. Assumes an Indian 10-digit mobile number when the customer's
 * on-file number carries no country code, since that's what every
 * contact created via ContactsPage/BillingPage looks like today. */
function whatsAppShareUrl(phone: string, message: string): string | null {
  const digits = phone.replace(/\D/g, "");
  if (!digits) return null;
  const withCountryCode = digits.length === 10 ? `91${digits}` : digits;
  return `https://wa.me/${withCountryCode}?text=${encodeURIComponent(message)}`;
}

/** apiUrl() returns a same-origin-relative path (e.g. "/api/v1/..."); a
 * link handed to WhatsApp is opened on the RECIPIENT's device, not this
 * one, so it must be absolute. Most self-hosted installs serve the API
 * from the same origin as the SPA (Stage 10a's WEB_DIST_DIR embedding),
 * so window.location.origin is correct there; a deployment with
 * VITE_API_BASE_URL pointed at a separate origin already gets an
 * absolute URL out of apiUrl() itself, left untouched below. */
function absoluteApiUrl(path: string): string {
  const url = apiUrl(path);
  return /^https?:\/\//.test(url) ? url : `${window.location.origin}${url}`;
}

export function SalesDetailPage({ id }: { id: string }) {
  const doc = useQuery({
    queryKey: ["sales-document", id],
    queryFn: () => api.get<{ document: SalesDocument; lines: SalesDocumentLine[] }>(`/sales/documents/${id}`),
  });

  const customer = useQuery({
    queryKey: ["party", doc.data?.document.CustomerPartyID],
    queryFn: () => api.get<Party>(`/contacts/parties/${doc.data?.document.CustomerPartyID}`),
    enabled: !!doc.data?.document.CustomerPartyID,
  });

  // Creates a signed, expiring, revocable share link (internal/modules/
  // notifications, Stage 9) on demand, then opens wa.me with a message
  // that carries a real link to GET /share/{token}/pdf — the customer
  // opens it and sees the actual invoice, no app or login needed on
  // their end, no PDF attach-by-hand required on this end (unlike a
  // plain wa.me text-only message, which is all this button used to
  // send). Created fresh per click rather than reused/cached: each link
  // is independently revocable from this same click without needing a
  // "manage this document's share links" UI, and the 7-day-ish TTL
  // notifications/app.Service.CreateShareLink sets is generous enough
  // that click-to-share doesn't need its own expiry picker.
  const shareViaWhatsApp = useMutation({
    mutationFn: async (params: { documentId: string; phone: string; message: string }) => {
      const { token } = await api.post<{ token: string }>("/share-links", {
        document_type: "sales_document",
        document_id: params.documentId,
      });
      const pdfUrl = absoluteApiUrl(`/share/${token}/pdf`);
      const url = whatsAppShareUrl(params.phone, `${params.message}\n${pdfUrl}`);
      if (url) window.open(url, "_blank", "noopener,noreferrer");
    },
  });

  if (doc.isPending) {
    return (
      <div className={layout.page}>
        <div className={layout.skeleton} style={{ height: 320 }} aria-hidden="true" />
      </div>
    );
  }

  if (doc.isError) {
    return (
      <div className={layout.page}>
        <p className={layout.errorState} role="alert">
          Couldn't load this sale.
        </p>
      </div>
    );
  }

  const { document } = doc.data;
  // Go marshals a nil slice as JSON null, not [] — defensive even though
  // a finalized document always has >=1 line in practice (see
  // apps/web/src/lib/api-client.ts's getListField doc comment).
  const lines = doc.data.lines ?? [];

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>{document.DocumentNumber || "Draft sale"}</h1>
          <p className={layout.subtitle}>{DOCUMENT_TYPE_LABELS[document.DocumentType]}</p>
        </div>
        {document.Status === "DRAFT" ? (
          <Link to="/sales/new" search={{ resume: document.ID }} className={ui.btnPrimary}>
            Continue billing
          </Link>
        ) : (
          <div className={styles.headerActions}>
            <a href={`/api/v1/sales/documents/${document.ID}/print`} target="_blank" rel="noopener noreferrer" className={ui.btnSecondary}>
              Print / Download PDF
            </a>
            {(() => {
              const canShare = !!customer.data?.Phone;
              return (
                <button
                  type="button"
                  className={ui.btnSecondary}
                  disabled={!canShare || shareViaWhatsApp.isPending}
                  title={canShare ? undefined : "Add a phone number for this customer to share via WhatsApp"}
                  onClick={() =>
                    customer.data?.Phone &&
                    shareViaWhatsApp.mutate({
                      documentId: document.ID,
                      phone: customer.data.Phone,
                      message: `Hi ${customer.data.LegalName}, your ${DOCUMENT_TYPE_LABELS[document.DocumentType].toLowerCase()} ${document.DocumentNumber} for ${document.GrandTotalAmount ? formatMoney(document.GrandTotalAmount) : "—"} is ready. Thank you for your business!`,
                    })
                  }
                >
                  {shareViaWhatsApp.isPending ? "Preparing…" : "Share via WhatsApp"}
                </button>
              );
            })()}
          </div>
        )}
      </div>
      {shareViaWhatsApp.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)" }}>
          {shareViaWhatsApp.error instanceof ApiError ? shareViaWhatsApp.error.message : "Could not create a share link."}
        </p>
      ) : null}

      <div className={styles.grid}>
        <div className={layout.panel}>
          <div className={styles.metaRow}>
            <span>
              Customer: <strong>{customer.data?.LegalName ?? "—"}</strong>
            </span>
            <span>
              Status: <strong>{document.Status}</strong>
            </span>
            <span>
              Issue date: <strong>{new Date(document.IssueDate).toLocaleDateString()}</strong>
            </span>
            <span>
              Place of supply: <strong>{document.PlaceOfSupplyStateCode}</strong>
            </span>
          </div>

          {lines.length === 0 ? (
            <p className={layout.emptyState}>No items on this document.</p>
          ) : (
            <div className={ui.tableScroll}>
              <table className={ui.table}>
                <thead>
                  <tr>
                    <th scope="col">#</th>
                    <th scope="col">HSN/SAC</th>
                    <th scope="col">Qty</th>
                    <th scope="col">Rate</th>
                    <th scope="col">Total</th>
                  </tr>
                </thead>
                <tbody>
                  {lines.map((l) => (
                    <tr key={l.ID}>
                      <td className="num">{l.LineNumber}</td>
                      <td>{l.HSNSACCode}</td>
                      <td className="num">{l.Quantity}</td>
                      <td className="num">{formatMoney(l.UnitPrice)}</td>
                      <td className="num">{formatMoney(l.LineTotal)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className={styles.totalRow}>
            <span>Grand total</span>
            <span className="num">{document.GrandTotalAmount ? formatMoney(document.GrandTotalAmount) : "—"}</span>
          </div>
        </div>

        {document.Status === "FINALIZED" && EWB_ELIGIBLE_TYPES.has(document.DocumentType) ? (
          <EwayBillCard documentId={document.ID} />
        ) : null}
      </div>
    </div>
  );
}
