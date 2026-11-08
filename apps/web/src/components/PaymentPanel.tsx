import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type CSSProperties } from "react";
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
  ReversedAt?: string | null;
}

interface BankAccount {
  ID: string;
  Name: string;
  Kind: "BANK" | "CASH";
  IsActive: boolean;
}

const METHOD_LABELS: Record<string, string> = {
  CASH: "Cash",
  UPI: "UPI",
  CARD: "Card",
  BANK_TRANSFER: "Bank transfer",
  CHEQUE: "Cheque",
  OTHER: "Other",
};

const QUICK_PICK_ACTIVE_STYLE: CSSProperties = {
  background: "var(--color-accent)",
  color: "var(--color-on-accent)",
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
  // Which quick-pick button (if any) last set `amount` — purely a visual
  // "pressed" state so the cashier can see what they picked; typing in the
  // amount field by hand always wins and clears it back to unselected.
  const [quickPick, setQuickPick] = useState<"FULL" | 25 | 50 | 75 | null>(null);
  const [method, setMethod] = useState("CASH");
  const [reference, setReference] = useState("");
  // Empty string means "no bank_account_id" — RecordReceipt/RecordPayment
  // both default to the plain Cash ledger account in that case (their own
  // doc comments). Without ever sending a real bank_account_id here, a
  // payment recorded as "UPI" or "Bank transfer" was still posted to Cash
  // in the books regardless — the Method field was purely descriptive
  // metadata, not what actually decided which GL account was credited.
  const [bankAccountId, setBankAccountId] = useState("");

  const bankAccounts = useQuery({
    queryKey: ["bank-accounts"],
    queryFn: () => api.get<BankAccount[]>("/accounting/bank-accounts"),
  });
  const activeBankAccounts = (bankAccounts.data ?? []).filter((a) => a.IsActive && a.Kind === "BANK");

  const isReceive = direction === "RECEIVE";
  const listPath = isReceive ? `/accounting/sales-documents/${documentId}/receipts` : `/accounting/purchase-documents/${documentId}/payments`;
  const listField = isReceive ? "receipts" : "payments";
  const queryKey = [isReceive ? "sales-doc-receipts" : "purchase-doc-payments", documentId];

  const transactions = useQuery({
    queryKey,
    queryFn: () => api.getListField<Transaction>(listPath, listField),
  });

  const totalPaid = (transactions.data ?? []).filter((t) => !t.ReversedAt).reduce((sum, t) => sum + Math.round(Number(t.Amount.amount) * 100), 0) / 100; // whole paise/cents, so 0.1 + 0.2 cannot drift
  const grandTotalNumber = grandTotal ? Number(grandTotal.amount) : 0;
  const outstanding = Math.max(0, Math.round((grandTotalNumber - totalPaid) * 100) / 100);
  const currency = grandTotal?.currency ?? "INR";

  // Sets `amount` from a fraction of what's still outstanding — always a
  // clean, decimal.Decimal-parseable string (toFixed(2), never a raw float
  // or something with a stray "%"), which is what the backend's
  // RecordReceipt/RecordPayment actually need: their Amount field is a
  // decimal.Decimal, and an unparseable value there is exactly what turns
  // into a 400 "Could not parse the request body." on save.
  function pickQuickAmount(pick: "FULL" | 25 | 50 | 75) {
    const pct = pick === "FULL" ? 100 : pick;
    const value = (outstanding * pct) / 100;
    setAmount(value > 0 ? value.toFixed(2) : "");
    setQuickPick(pick);
  }

  // Receipts are never deleted: a mistake is reversed with a reason, and the row stays in the history.
  const [reversingId, setReversingId] = useState<string | null>(null);
  const [reverseReason, setReverseReason] = useState("");
  const reverse = useMutation({
    mutationFn: (id: string) => api.post(`/accounting/receipts/${id}/reverse`, { reason: reverseReason.trim() }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey });
      void queryClient.invalidateQueries({ queryKey: ["sales-document-preview", documentId] });
      void queryClient.invalidateQueries({ queryKey: ["party-ledger", partyId] });
      void queryClient.invalidateQueries({ queryKey: ["party-ageing", partyId] });
      setReversingId(null);
      setReverseReason("");
    },
  });

  const record = useMutation({
    mutationFn: () =>
      api.post(isReceive ? "/accounting/receipts" : "/accounting/payments", {
        party_id: partyId,
        [isReceive ? "sales_document_id" : "purchase_document_id"]: documentId,
        amount,
        method,
        reference_number: reference,
        bank_account_id: bankAccountId || undefined,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey });
      // The invoice on screen (and its print) must show the new installment straight away.
      void queryClient.invalidateQueries({ queryKey: ["sales-document-preview", documentId] });
      void queryClient.invalidateQueries({ queryKey: ["party-ledger", partyId] });
      void queryClient.invalidateQueries({ queryKey: ["party-ageing", partyId] });
      setAmount("");
      setQuickPick(null);
      setReference("");
      setBankAccountId("");
      setShowForm(false);
    },
  });

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>{isReceive ? "Payment received" : "Payment made"}</h2>
        <div className={ui.toolbarSpacer} />
        {!showForm && outstanding <= 0 && grandTotalNumber > 0 ? (
          <span className={ui.badge} data-tone="positive">
            Paid in full
          </span>
        ) : null}
        {!showForm && !(outstanding <= 0 && grandTotalNumber > 0) ? (
          <button type="button" id={`record-payment-${direction}`} className={ui.btnPrimary}
            onClick={() => {
              // Most payments settle the whole balance, so open with it filled in.
              if (outstanding > 0) pickQuickAmount("FULL");
              setShowForm(true);
            }}
          >
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
          {outstanding > 0 ? (
            <div className={ui.field} style={{ marginBottom: 12 }}>
              <label>Quick amount</label>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                <button
                  type="button"
                  className={ui.btnGhost}
                  style={quickPick === "FULL" ? QUICK_PICK_ACTIVE_STYLE : undefined}
                  onClick={() => pickQuickAmount("FULL")}
                >
                  Full payment
                </button>
                {([25, 50, 75] as const).map((pct) => (
                  <button
                    key={pct}
                    type="button"
                    className={ui.btnGhost}
                    style={quickPick === pct ? QUICK_PICK_ACTIVE_STYLE : undefined}
                    onClick={() => pickQuickAmount(pct)}
                  >
                    {pct}%
                  </button>
                ))}
                <button
                  type="button"
                  className={ui.btnGhost}
                  style={quickPick === null ? QUICK_PICK_ACTIVE_STYLE : undefined}
                  onClick={() => {
                    setQuickPick(null);
                    setAmount("");
                  }}
                >
                  Custom
                </button>
              </div>
            </div>
          ) : null}
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="payment-amount">Amount</label>
              <input
                id="payment-amount"
                className={ui.input}
                inputMode="decimal"
                value={amount}
                onChange={(e) => {
                  setAmount(e.target.value);
                  setQuickPick(null);
                }}
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
            {activeBankAccounts.length > 0 ? (
              <div className={ui.field}>
                <label htmlFor="payment-bank-account">{isReceive ? "Deposited to" : "Paid from"}</label>
                <select id="payment-bank-account" className={ui.select} value={bankAccountId} onChange={(e) => setBankAccountId(e.target.value)}>
                  <option value="">Cash</option>
                  {activeBankAccounts.map((a) => (
                    <option key={a.ID} value={a.ID}>
                      {a.Name}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button
              type="button"
              className={ui.btnSecondary}
              onClick={() => {
                setShowForm(false);
                setAmount("");
                setQuickPick(null);
              }}
            >
              Cancel
            </button>
            <button type="button" className={ui.btnPrimary} disabled={!amount || !(Number(amount) > 0) || record.isPending} onClick={() => record.mutate()}>
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
        <div className={ui.tableScroll} tabIndex={0}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Date</th>
                <th scope="col">Method</th>
                <th scope="col">Reference</th>
                <th scope="col">Amount</th>
                {isReceive ? <th scope="col">Action</th> : null}
              </tr>
            </thead>
            <tbody>
              {(transactions.data ?? []).map((t) => (
                <tr key={t.ID} style={t.ReversedAt ? { textDecoration: "line-through", opacity: 0.6 } : undefined}>
                  <td>{new Date((t.ReceivedAt ?? t.PaidAt) as string).toLocaleDateString()}</td>
                  <td>{METHOD_LABELS[t.Method] ?? t.Method}</td>
                  <td>{t.ReferenceNumber || "—"}</td>
                  <td className="num">{formatMoney(t.Amount)}</td>
                  {isReceive ? (
                    <td style={{ textDecoration: "none" }}>
                      {t.ReversedAt ? (
                        <span className={ui.badge} data-tone="negative">
                          Reversed
                        </span>
                      ) : reversingId === t.ID ? (
                        <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                          <input
                            aria-label="Reason for reversing this payment"
                            placeholder="Why is it being reversed?"
                            value={reverseReason}
                            onChange={(e) => setReverseReason(e.target.value)}
                          />
                          <button type="button" className={ui.btnPrimary} disabled={!reverseReason.trim() || reverse.isPending} onClick={() => reverse.mutate(t.ID)}>
                            {reverse.isPending ? "Reversing…" : "Confirm"}
                          </button>
                          <button type="button" className={ui.btnSecondary} onClick={() => { setReversingId(null); setReverseReason(""); }}>
                            Cancel
                          </button>
                          {reverse.isError ? (
                            <span role="alert" style={{ color: "var(--color-negative)" }}>
                              {reverse.error instanceof ApiError ? reverse.error.message : "Could not reverse this payment."}
                            </span>
                          ) : null}
                        </div>
                      ) : (
                        <button type="button" className={ui.btnGhost} onClick={() => { reverse.reset(); setReversingId(t.ID); }}>
                          Reverse
                        </button>
                      )}
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
