import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { EwayBillCard } from "../../components/EwayBillCard";
import ui from "../../components/ui.module.css";
import { api } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import layout from "../DashboardPage.module.css";
import styles from "./SalesDetailPage.module.css";
import { DOCUMENT_TYPE_LABELS, EWB_ELIGIBLE_TYPES, type SalesDocument, type SalesDocumentLine } from "./types";

/** A WhatsApp "click to chat" deep link (`wa.me`) — no WhatsApp Business
 * API credential needed, works for any customer with a saved phone
 * number. Assumes an Indian 10-digit mobile number when the customer's
 * on-file number carries no country code, since that's what every
 * contact created via ContactsPage/BillingPage looks like today. WhatsApp
 * has no URL-scheme way to pre-attach the invoice PDF to the draft
 * message, so the message points the customer at what to expect and
 * leaves attaching the already-downloadable PDF (the button right next to
 * this one) to the person sending it. */
function whatsAppShareUrl(phone: string, message: string): string | null {
  const digits = phone.replace(/\D/g, "");
  if (!digits) return null;
  const withCountryCode = digits.length === 10 ? `91${digits}` : digits;
  return `https://wa.me/${withCountryCode}?text=${encodeURIComponent(message)}`;
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
              const shareUrl = customer.data?.Phone
                ? whatsAppShareUrl(
                    customer.data.Phone,
                    `Hi ${customer.data.LegalName}, your ${DOCUMENT_TYPE_LABELS[document.DocumentType].toLowerCase()} ${document.DocumentNumber} for ${document.GrandTotalAmount ? formatMoney(document.GrandTotalAmount) : "—"} is ready. Thank you for your business!`,
                  )
                : null;
              return (
                <a
                  href={shareUrl ?? undefined}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={ui.btnSecondary}
                  aria-disabled={!shareUrl}
                  style={shareUrl ? undefined : { opacity: 0.5, cursor: "not-allowed" }}
                  title={shareUrl ? undefined : "Add a phone number for this customer to share via WhatsApp"}
                  onClick={(e) => {
                    if (!shareUrl) e.preventDefault();
                  }}
                >
                  Share via WhatsApp
                </a>
              );
            })()}
          </div>
        )}
      </div>

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
