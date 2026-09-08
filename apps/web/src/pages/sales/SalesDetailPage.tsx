import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { EwayBillCard } from "../../components/EwayBillCard";
import { WhatsAppIcon } from "../../components/icons";
import { PaymentPanel } from "../../components/PaymentPanel";
import { PrintTemplateMenu } from "../../components/PrintTemplateMenu";
import { ShareLinksPanel } from "../../components/ShareLinksPanel";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import { useShareSalesDocumentOnWhatsApp } from "../../lib/whatsapp";
import layout from "../DashboardPage.module.css";
import styles from "./SalesDetailPage.module.css";
import { DOCUMENT_TYPE_LABELS, EWB_ELIGIBLE_TYPES, PAYABLE_TYPES, type SalesDocument, type SalesDocumentLine } from "./types";

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
  const shareViaWhatsApp = useShareSalesDocumentOnWhatsApp();

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
            <PrintTemplateMenu documentId={document.ID} />
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
                  {shareViaWhatsApp.isPending ? (
                    "Preparing…"
                  ) : (
                    <>
                      <WhatsAppIcon /> Share via WhatsApp
                    </>
                  )}
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
      {document.Status !== "DRAFT" ? <ShareLinksPanel documentType="sales_document" documentId={document.ID} /> : null}

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

      {document.Status === "FINALIZED" && PAYABLE_TYPES.has(document.DocumentType) ? (
        <PaymentPanel documentId={document.ID} partyId={document.CustomerPartyID} grandTotal={document.GrandTotalAmount} direction="RECEIVE" />
      ) : null}
    </div>
  );
}
