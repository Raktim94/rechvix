import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";
import { api, ApiError } from "../lib/api-client";
import { formatMoney, type Money } from "../lib/money";

interface Transaction {
  ID: string;
  Amount: Money;
  Method: string;
  ReferenceNumber: string;
  ReceivedAt?: string;
  PaidAt?: string;
}

const METHOD_LABELS: Record<string, string> = {
  CASH: "Cash",
  UPI: "UPI",
  CARD: "Card",
  BANK_TRANSFER: "Bank transfer",
  CHEQUE: "Cheque",
  OTHER: "Other",
};

/** "How much of THIS invoice/bill has actually been paid" — direction
 * "RECEIVE" is SalesDetailPage (money coming in from a customer),
 * "PAY" is PurchasesPage's in-progress view (money going out to a
 * supplier). Both sides were previously only reachable from the
 * customer/supplier's own on-account balance on ContactDetailPage, with
 * no way to tie a payment to the specific document it was actually for
 * — accounting.Receipt/Payment always accepted a document id, nothing
 * in the UI ever sent one. */
export function PaymentPanel({
  documentId,
  partyId,
  grandTotal,
  direction,
}: {
  documentId: string;
  partyId: string;
  grandTotal: Money | null;
  direction: "RECEIVE" | "PAY";
}) {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [amount, setAmount] = useState("");
  const [method, setMethod] = useState("CASH");
  const [reference, setReference] = useState("");

  const isReceive = direction === "RECEIVE";
  const listPath = isReceive ? `/accounting/sales-documents/${documentId}/receipts` : `/accounting/purchase-documents/${documentId}/payments`;
  const listField = isReceive ? "receipts" : "payments";
  const queryKey = [isReceive ? "sales-doc-receipts" : "purchase-doc-payments", documentId];

  const transactions = useQuery({
    queryKey,
    queryFn: () => api.getListField<Transaction>(listPath, listField),
  });

  const totalPaid = (transactions.data ?? []).reduce((sum, t) => sum + Number(t.Amount.amount), 0);
  const grandTotalNumber = grandTotal ? Number(grandTotal.amount) : 0;
  const outstanding = Math.max(0, grandTotalNumber - totalPaid);
  const currency = grandTotal?.currency ?? "INR";

  const record = useMutation({
    mutationFn: () =>
      api.post(isReceive ? "/accounting/receipts" : "/accounting/payments", {
        party_id: partyId,
        [isReceive ? "sales_document_id" : "purchase_document_id"]: documentId,
        amount,
        method,
        reference_number: reference,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey });
      void queryClient.invalidateQueries({ queryKey: ["party-ledger", partyId] });
      void queryClient.invalidateQueries({ queryKey: ["party-ageing", partyId] });
      setAmount("");
      setReference("");
      setShowForm(false);
    },
  });

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>{isReceive ? "Payment received" : "Payment made"}</h2>
        <div className={ui.toolbarSpacer} />
        {!showForm ? (
          <button type="button" className={ui.btnPrimary} onClick={() => setShowForm(true)}>
            + Record {isReceive ? "payment received" : "payment made"}
          </button>
        ) : null}
      </div>

      {grandTotal ? (
        <div className={ui.formGrid} style={{ marginBottom: 16 }}>
          <div>
            <span className={ui.muted}>Total</span>
            <div className="num">{formatMoney(grandTotal)}</div>
          </div>
          <div>
            <span className={ui.muted}>Paid</span>
            <div className="num" style={{ color: "var(--color-positive)" }}>
              {formatMoney({ amount: String(totalPaid), currency })}
            </div>
          </div>
          <div>
            <span className={ui.muted}>Outstanding</span>
            <div className="num" style={{ color: outstanding > 0 ? "var(--color-negative)" : undefined }}>
              {formatMoney({ amount: String(outstanding), currency })}
            </div>
          </div>
        </div>
      ) : null}

      {showForm ? (
        <div style={{ marginBottom: 20 }}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="payment-amount">Amount</label>
              <input
                id="payment-amount"
                className={ui.input}
                inputMode="decimal"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder={outstanding > 0 ? outstanding.toFixed(2) : "0.00"}
              />
            </div>
            <div className={ui.field}>
              <label htmlFor="payment-method">Method</label>
              <select id="payment-method" className={ui.select} value={method} onChange={(e) => setMethod(e.target.value)}>
                {Object.entries(METHOD_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </div>
            <div className={ui.field}>
              <label htmlFor="payment-reference">Reference (optional)</label>
              <input id="payment-reference" className={ui.input} value={reference} onChange={(e) => setReference(e.target.value)} placeholder="UTR / cheque no." />
            </div>
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button type="button" className={ui.btnSecondary} onClick={() => setShowForm(false)}>
              Cancel
            </button>
            <button type="button" className={ui.btnPrimary} disabled={!amount || Number(amount) <= 0 || record.isPending} onClick={() => record.mutate()}>
              {record.isPending ? "Saving…" : "Save"}
            </button>
          </div>
          {record.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {record.error instanceof ApiError ? record.error.message : "Could not record this payment."}
            </p>
          ) : null}
        </div>
      ) : null}

      {transactions.isPending ? (
        <div className={layout.skeleton} style={{ height: 60 }} aria-hidden="true" />
      ) : (transactions.data ?? []).length === 0 ? (
        <p className={layout.emptyState}>No payments recorded against this {isReceive ? "invoice" : "bill"} yet.</p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Date</th>
                <th scope="col">Method</th>
                <th scope="col">Reference</th>
                <th scope="col">Amount</th>
              </tr>
            </thead>
            <tbody>
              {(transactions.data ?? []).map((t) => (
                <tr key={t.ID}>
                  <td>{new Date((t.ReceivedAt ?? t.PaidAt) as string).toLocaleDateString()}</td>
                  <td>{METHOD_LABELS[t.Method] ?? t.Method}</td>
                  <td>{t.ReferenceNumber || "—"}</td>
                  <td className="num">{formatMoney(t.Amount)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
