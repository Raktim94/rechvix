import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import modal from "../../components/Modal.module.css";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney, type Money } from "../../lib/money";
import type { SalesDocument } from "./types";

interface Receipt {
  ID: string;
  Amount: Money;
}

/** POST /sales/documents/{id}/cancel existed with zero frontend callers
 * — a mis-billed FINALIZED invoice had no way to be voided short of a
 * SALES_RETURN/CREDIT_NOTE, which is the wrong tool (those model a
 * customer genuinely returning goods or a price correction, not "this
 * invoice should never have existed"). Warns rather than blocks when a
 * receipt is already on file — sales.Service.CancelDocument deliberately
 * leaves recorded receipts untouched (refund/credit/apply-elsewhere is a
 * business call it has no basis to make), so the owner needs to see that
 * before confirming, not discover it after. */
export function CancelDocumentModal({
  open,
  onOpenChange,
  document,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  document: SalesDocument;
}) {
  const queryClient = useQueryClient();

  const receipts = useQuery({
    queryKey: ["sales-doc-receipts", document.ID],
    queryFn: () => api.getListField<Receipt>(`/accounting/sales-documents/${document.ID}/receipts`, "receipts"),
    enabled: open,
  });
  const totalReceived = (receipts.data ?? []).reduce((sum, r) => sum + Number(r.Amount.amount), 0);

  const cancel = useMutation({
    mutationFn: () => api.post<SalesDocument>(`/sales/documents/${document.ID}/cancel`),
    onSuccess: () => {
      onOpenChange(false);
      void queryClient.invalidateQueries({ queryKey: ["sales-document", document.ID] });
      void queryClient.invalidateQueries({ queryKey: ["sales-documents"] });
    },
  });

  if (!open) return null;

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label="Cancel invoice" onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>Cancel {document.DocumentNumber}?</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <div className={modal.body}>
          <p>
            This voids the invoice: stock is put back, and the sale is reversed out of the books. The document stays on record as{" "}
            <strong>CANCELLED</strong> — it can't be undone.
          </p>
          {totalReceived > 0 ? (
            <p role="alert" style={{ color: "var(--color-warning, #b45309)" }}>
              {formatMoney({ amount: String(totalReceived), currency: document.GrandTotalAmount?.currency ?? "INR" })} has already been received
              against this invoice. Cancelling does <strong>not</strong> refund or adjust that payment automatically — record a refund or apply it
              elsewhere yourself once this is cancelled.
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
            Keep invoice
          </button>
          <button type="button" className={ui.btnDanger} disabled={cancel.isPending} onClick={() => cancel.mutate()}>
            {cancel.isPending ? "Cancelling…" : "Cancel invoice"}
          </button>
        </div>
      </div>
    </div>
  );
}
