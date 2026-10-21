import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, ApiError } from "../lib/api-client";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

interface Settings {
  enabled: boolean;
  days_after_due: number;
  repeat_every_days: number;
  max_reminders: number;
  subject: string;
  message: string;
  email_configured: boolean;
  public_base_url_set: boolean;
}
interface LogRow { document_id: string; document_number: string; sent_to: string; reminder_number: number; outstanding: string; queued_at: string }

/** Automatic overdue-invoice reminders by email. Each one carries a secure link, so opens are recorded on the invoice. */
export function RemindersPanel() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["reminders"], queryFn: () => api.get<Settings>("/reminders/settings") });
  const log = useQuery({ queryKey: ["reminders", "log"], queryFn: () => api.getListField<LogRow>("/reminders/log", "reminders") });
  const [form, setForm] = useState<Settings | null>(null);
  useEffect(() => {
    if (settings.data) setForm(settings.data);
  }, [settings.data]);
  const save = useMutation({
    mutationFn: (f: Settings) => api.put<Settings>("/reminders/settings", { enabled: f.enabled, days_after_due: f.days_after_due, repeat_every_days: f.repeat_every_days, max_reminders: f.max_reminders, subject: f.subject, message: f.message }),
    onSuccess: (d) => {
      qc.setQueryData(["reminders"], d);
      void qc.invalidateQueries({ queryKey: ["reminders", "log"] });
    },
  });
  if (!form || !settings.data) return null;
  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setForm({ ...form, [k]: v });
  return (
    <section className={layout.panel} aria-labelledby="reminders-heading">
      <h2 id="reminders-heading">Overdue reminders</h2>
      <p className={ui.muted}>Emails customers whose invoices are past due. Each email links to the invoice, and you can see on the invoice when the customer opened it.</p>
      {!settings.data.email_configured ? <p role="status">Email is not set up on this server. An administrator sets SMTP_HOST (and SMTP_FROM) first.</p> : null}
      {settings.data.email_configured && !settings.data.public_base_url_set ? <p role="status">An administrator must set PUBLIC_BASE_URL so the links in reminders point to this server.</p> : null}
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(form); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="rem-days">Send after the due date by (days)</label>
            <input id="rem-days" type="number" min={0} max={365} className={ui.input} value={form.days_after_due} onChange={(e) => set("days_after_due", Number(e.target.value))} />
          </div>
          <div className={ui.field}>
            <label htmlFor="rem-repeat">Repeat every (days)</label>
            <input id="rem-repeat" type="number" min={1} max={365} className={ui.input} value={form.repeat_every_days} onChange={(e) => set("repeat_every_days", Number(e.target.value))} />
          </div>
          <div className={ui.field}>
            <label htmlFor="rem-max">At most (reminders per invoice)</label>
            <input id="rem-max" type="number" min={1} max={20} className={ui.input} value={form.max_reminders} onChange={(e) => set("max_reminders", Number(e.target.value))} />
          </div>
          <div className={ui.field}>
            <label htmlFor="rem-subject">Subject</label>
            <input id="rem-subject" className={ui.input} value={form.subject} maxLength={200} onChange={(e) => set("subject", e.target.value)} />
          </div>
        </div>
        <div className={ui.field}>
          <label htmlFor="rem-message">Message</label>
          <textarea id="rem-message" className={ui.input} rows={3} value={form.message} maxLength={2000} onChange={(e) => set("message", e.target.value)} />
          <p className={ui.muted}>You can use {"{{number}}"}, {"{{amount}}"}, {"{{due_date}}"} and {"{{customer}}"}.</p>
        </div>
        <label style={{ display: "flex", gap: 8, alignItems: "center", margin: "8px 0" }}>
          <input type="checkbox" checked={form.enabled} onChange={(e) => set("enabled", e.target.checked)} /> Send reminders automatically
        </label>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
          <button type="submit" className={ui.btnPrimary} disabled={save.isPending}>{save.isPending ? "Saving…" : "Save"}</button>
          {save.isSuccess ? <span role="status">Saved.</span> : null}
        </div>
        {save.isError ? <p role="alert">{save.error instanceof ApiError ? save.error.message : "Could not save."}</p> : null}
      </form>
      {log.data && log.data.length > 0 ? (
        <div className={ui.tableScroll} tabIndex={0}>
          <table className={ui.table}>
            <caption className="sr-only">Reminders sent</caption>
            <thead><tr><th scope="col">Invoice</th><th scope="col">Sent to</th><th scope="col">#</th><th scope="col">Outstanding</th><th scope="col">When</th></tr></thead>
            <tbody>{log.data.map((r, i) => <tr key={`${r.document_id}-${i}`}><td>{r.document_number}</td><td>{r.sent_to}</td><td>{r.reminder_number}</td><td>{r.outstanding}</td><td>{new Date(r.queued_at).toLocaleString()}</td></tr>)}</tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}
