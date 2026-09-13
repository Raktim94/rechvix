import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api-client";
import type { Money } from "../lib/money";
import { formatMoney } from "../lib/money";
import { whatsAppShareUrl } from "../lib/whatsapp";
import layout from "../pages/DashboardPage.module.css";
import { WhatsAppIcon } from "./icons";
import ui from "./ui.module.css";

interface ReceivableRow {
  PartyID: string;
  PartyName: string;
  Phone: string;
  Total: Money;
  FirstReminderSentAt: string | null;
  LastReminderSentAt: string | null;
  ReminderCount: number;
}

function formatSentAt(iso: string | null): string {
  if (!iso) return "Not sent yet";
  return new Date(iso).toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "short" });
}

/** The "Receivables (who owes you)" panel: unlike ReportTable's generic,
 * export-only rendering of GET /reports/accounting/receivables (which
 * shows a raw party UUID, no phone, no way to act on a row), this hits
 * the richer /receivables/detailed endpoint and adds a one-click
 * WhatsApp reminder per customer plus a record of when the first
 * reminder was sent (internal/modules/reporting, migrations/0042). */
export function ReceivablesPanel() {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["receivables-detailed"],
    queryFn: () => api.getListField<ReceivableRow>("/reports/accounting/receivables/detailed", "parties"),
  });

  const sendReminder = useMutation({
    mutationFn: (partyId: string) => api.post(`/reports/accounting/receivables/${partyId}/reminders`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["receivables-detailed"] }),
  });

  if (query.isPending) {
    return <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />;
  }
  if (query.isError) {
    return (
      <p className={layout.errorState} role="alert">
        Couldn't load receivables.
      </p>
    );
  }
  if (query.data.length === 0) {
    return <p className={layout.emptyState}>Nobody owes you anything right now.</p>;
  }

  return (
    <div className={ui.tableScroll}>
      <table className={ui.table}>
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Phone</th>
            <th scope="col">Amount due</th>
            <th scope="col">Reminder</th>
            <th scope="col">First reminder sent</th>
          </tr>
        </thead>
        <tbody>
          {query.data.map((row) => {
            const message = `Reminder: You have an outstanding balance of ${formatMoney(row.Total)} with us. Please arrange payment at your earliest convenience. Thank you!`;
            const waUrl = row.Phone ? whatsAppShareUrl(row.Phone, message) : null;
            return (
              <tr key={row.PartyID}>
                <td>{row.PartyName || "—"}</td>
                <td>{row.Phone || "—"}</td>
                <td className="num">{formatMoney(row.Total)}</td>
                <td>
                  <button
                    type="button"
                    className={ui.btnSecondary}
                    disabled={!waUrl || sendReminder.isPending}
                    title={waUrl ? "Send a WhatsApp payment reminder" : "Add a phone number for this customer to send a reminder"}
                    onClick={() => {
                      if (!waUrl) return;
                      window.open(waUrl, "_blank", "noopener,noreferrer");
                      sendReminder.mutate(row.PartyID);
                    }}
                  >
                    <WhatsAppIcon /> Send reminder
                  </button>
                </td>
                <td>{formatSentAt(row.FirstReminderSentAt)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {sendReminder.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)" }}>
          {sendReminder.error instanceof ApiError ? sendReminder.error.message : "Could not record that reminder."}
        </p>
      ) : null}
    </div>
  );
}
