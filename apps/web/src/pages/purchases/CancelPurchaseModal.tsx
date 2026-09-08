import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import modal from "../../components/Modal.module.css";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney, type Money } from "../../lib/money";

interface Payment {
  ID: string;
  Amount: Money;
}

/** POST /purchases/documents/{id}/cancel — the purchases-side mirror of
 * CancelDocumentModal.tsx (apps/web/src/pages/sales). A mis-entered
 * FINALIZED bill previously had no way to be voided at all on this
 * side. Warns rather than blocks when a payment is already on file —
 * purchases.Service.CancelDocument deliberately leaves recorded
 * payments untouched, same reasoning as the sales side. */
export function CancelPurchaseModal({
  open,
  onOpenChange,
  documentId,
  documentNumber,
  currencyCode,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  documentId: string;
  documentNumber: string;
  currencyCode: string;
}) {
  const queryClient = useQueryClient();

  const payments = useQuery({
    queryKey: ["purchase-doc-payments", documentId],
    queryFn: () => api.getListField<Payment>(`/accounting/purchase-documents/${documentId}/payments`, "payments"),
    enabled: open,
  });
  const totalPaid = (payments.data ?? []).reduce((sum, p) => sum + Number(p.Amount.amount), 0);

  const cancel = useMutation({
    mutationFn: () => api.post(`/purchases/documents/${documentId}/cancel`),
    onSuccess: () => {
      onOpenChange(false);
      void queryClient.invalidateQueries({ queryKey: ["purchase-document", documentId] });
      void queryClient.invalidateQueries({ queryKey: ["purchase-documents"] });
    },
  });

  if (!open) return null;

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label="Cancel purchase" onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>Cancel {documentNumber || "this purchase"}?</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <div className={modal.body}>
          <p>
            This voids the purchase: stock is taken back out, and the bill is reversed out of the books. The document stays on record as{" "}
            <strong>CANCELLED</strong> — it can't be undone.
          </p>
          {totalPaid > 0 ? (
            <p role="alert" style={{ color: "var(--color-warning, #b45309)" }}>
              {formatMoney({ amount: String(totalPaid), currency: currencyCode })} has already been paid against this bill. Cancelling does{" "}
              <strong>not</strong> refund or adjust that payment automatically — record a refund or apply it elsewhere yourself once this is
              cancelled.
            </p>
          ) : null}
          {cancel.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)" }}>
              {cancel.error instanceof ApiError ? cancel.error.message : "Could not cancel this document."}
            </p>
          ) : null}
        </div>
        <div className={modal.footer}>
          <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
            Keep purchase
          </button>
          <button type="button" className={ui.btnDanger} disabled={cancel.isPending} onClick={() => cancel.mutate()}>
            {cancel.isPending ? "Cancelling…" : "Cancel purchase"}
          </button>
        </div>
      </div>
    </div>
  );
}
