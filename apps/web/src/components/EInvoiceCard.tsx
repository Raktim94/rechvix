import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ui from "./ui.module.css";
import { api, ApiError } from "../lib/api-client";
import styles from "./EwayBillCard.module.css";

type EInvoiceStatus = "DRAFT" | "QUEUED" | "SUBMITTING" | "GENERATED" | "FAILED_RETRYABLE" | "FAILED_FINAL" | "CANCEL_PENDING" | "CANCELLED";

interface EInvoiceRecord {
  Status: EInvoiceStatus;
  IRN: string | null;
  AckNumber: string | null;
  AckDate: string | null;
  ErrorMessage: string | null;
}

interface StatusResponse {
  record: EInvoiceRecord | null;
}

/** GET /sales/documents/{id}/einvoice existed with zero frontend
 * callers — IRN generation itself is fully automatic (sales.
 * FinalizeDocument enqueues einvoice.generate, apps/worker's outbox
 * poller does the rest, internal/modules/einvoice/app.Service's own doc
 * comment), so this card is read-only for everything except one case:
 * a FAILED_FINAL record is deliberately never retried by the outbox
 * (that's what "final" means — see RetryDocument's doc comment), which
 * used to be a permanent dead end even after fixing the actual problem
 * (e.g. adding the legal entity's missing GSTIN in Settings). The Retry
 * button below is the only mutation this card has. */
export function EInvoiceCard({ documentId }: { documentId: string }) {
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ["einvoice-status", documentId],
    queryFn: () => api.get<StatusResponse>(`/sales/documents/${documentId}/einvoice`),
    // A QUEUED/SUBMITTING record is mid-flight on the outbox worker —
    // poll briefly so a page left open catches the GENERATED/failed
    // outcome without a manual refresh.
    refetchInterval: (query) => {
      const s = query.state.data?.record?.Status;
      return s === "QUEUED" || s === "SUBMITTING" ? 4000 : false;
    },
  });

  const retry = useMutation({
    mutationFn: () => api.post<StatusResponse>(`/sales/documents/${documentId}/einvoice/retry`),
    onSuccess: (data) => {
      queryClient.setQueryData(["einvoice-status", documentId], data);
    },
    // A retry that fails again (still no GSTIN, provider rejected it
    // once more, ...) comes back as a non-2xx — the record was still
    // updated server-side with the new failure though, so refetch
    // rather than leaving the stale pre-retry error message showing.
    onError: () => {
      void queryClient.invalidateQueries({ queryKey: ["einvoice-status", documentId] });
    },
  });

  if (status.isPending) {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Invoice</h2>
        </div>
        <p className={styles.explainer}>Checking e-Invoice status…</p>
      </div>
    );
  }

  if (status.isError) {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Invoice</h2>
        </div>
        <p className={styles.errorText} role="alert">
          Couldn't check the e-Invoice status for this sale.
        </p>
      </div>
    );
  }

  const record = status.data.record;

  if (!record) {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Invoice</h2>
          <span className={ui.badge} data-tone="neutral">
            Not generated
          </span>
        </div>
        <p className={styles.explainer}>No e-Invoice has been generated for this document yet.</p>
      </div>
    );
  }

  if (record.Status === "GENERATED") {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Invoice</h2>
          <span className={ui.badge} data-tone="positive">
            Generated
          </span>
        </div>
        <p className={styles.explainer}>
          IRN <strong>{record.IRN}</strong> has been issued for this invoice.
        </p>
        {record.AckNumber ? (
          <p className={styles.detail}>
            Ack no. {record.AckNumber}
            {record.AckDate ? ` · ${new Date(record.AckDate).toLocaleString()}` : ""}
          </p>
        ) : null}
      </div>
    );
  }

  if (record.Status === "QUEUED" || record.Status === "SUBMITTING") {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Invoice</h2>
          <span className={ui.badge} data-tone="warning">
            Generating…
          </span>
        </div>
        <p className={styles.explainer}>We're submitting this invoice to the government e-Invoice system automatically. This usually takes a few seconds.</p>
      </div>
    );
  }

  if (record.Status === "FAILED_RETRYABLE" || record.Status === "FAILED_FINAL") {
    const isFinal = record.Status === "FAILED_FINAL";
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Invoice</h2>
          <span className={ui.badge} data-tone={isFinal ? "negative" : "warning"}>
            {isFinal ? "Failed" : "Retrying"}
          </span>
        </div>
        <p className={styles.explainer}>
          {isFinal
            ? "e-Invoice generation failed and won't be retried automatically."
            : "e-Invoice generation failed and will be retried automatically."}
        </p>
        {record.ErrorMessage ? <p className={styles.detail}>{record.ErrorMessage}</p> : null}
        <div className={ui.formActions}>
          <button type="button" className={ui.btnSecondary} disabled={retry.isPending} onClick={() => retry.mutate()}>
            {retry.isPending ? "Retrying…" : isFinal ? "Fixed it — retry now" : "Retry now"}
          </button>
        </div>
        {retry.isError ? (
          <p className={styles.errorText} role="alert">
            {retry.error instanceof ApiError ? retry.error.message : "Retry failed."}
          </p>
        ) : null}
      </div>
    );
  }

  // CANCEL_PENDING / CANCELLED — no frontend action generates these yet
  // (there is no IRN-cancellation flow), shown for completeness only.
  return (
    <div className={styles.card}>
      <div className={styles.header}>
        <h2>e-Invoice</h2>
        <span className={ui.badge} data-tone="neutral">
          {record.Status === "CANCELLED" ? "Cancelled" : "Cancelling"}
        </span>
      </div>
      {record.IRN ? (
        <p className={styles.explainer}>
          IRN <strong>{record.IRN}</strong> {record.Status === "CANCELLED" ? "has been cancelled." : "is being cancelled."}
        </p>
      ) : null}
    </div>
  );
}
