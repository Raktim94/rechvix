import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

interface Schedule {
  id: string;
  source_document_id: string;
  source_number: string;
  customer: string;
  frequency: string;
  interval_count: number;
  next_run_date: string;
  end_date?: string;
  auto_finalize: boolean;
  email_to?: string;
  active: boolean;
  run_count: number;
}

const unit: Record<string, string> = { WEEKLY: "week", MONTHLY: "month", QUARTERLY: "quarter", YEARLY: "year" };

function todayISO() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** "Repeat this invoice": clones a finalized tax invoice on a rhythm, as a draft or finalized (and optionally emailed). */
export function RepeatInvoiceForm({ documentId }: { documentId: string }) {
  const qc = useQueryClient();
  const [frequency, setFrequency] = useState("MONTHLY");
  const [every, setEvery] = useState(1);
  const [first, setFirst] = useState(todayISO());
  const [end, setEnd] = useState("");
  const [auto, setAuto] = useState(false);
  const [email, setEmail] = useState("");
  const create = useMutation({
    mutationFn: () => api.post<Schedule>(`/sales/documents/${documentId}/recurring`, { frequency, interval_count: every, first_run_date: first, end_date: end, auto_finalize: auto, email_to: email }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["recurring"] }),
  });
  return (
    <section className={layout.panel} aria-labelledby="repeat-heading">
      <h2 id="repeat-heading">Repeat this invoice</h2>
      <p className={ui.muted}>Creates a copy on a schedule. Leave "finalize automatically" off to review each copy as a draft first.</p>
      <form onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="rec-freq">Repeat</label>
            <select id="rec-freq" className={ui.input} value={frequency} onChange={(e) => setFrequency(e.target.value)}>
              <option value="WEEKLY">Weekly</option><option value="MONTHLY">Monthly</option><option value="QUARTERLY">Quarterly</option><option value="YEARLY">Yearly</option>
            </select>
          </div>
          <div className={ui.field}>
            <label htmlFor="rec-every">Every</label>
            <input id="rec-every" type="number" min={1} max={24} className={ui.input} value={every} onChange={(e) => setEvery(Number(e.target.value))} />
          </div>
          <div className={ui.field}>
            <label htmlFor="rec-first">First copy on</label>
            <input id="rec-first" type="date" min={todayISO()} required className={ui.input} value={first} onChange={(e) => setFirst(e.target.value)} />
          </div>
          <div className={ui.field}>
            <label htmlFor="rec-end">Stop after (optional)</label>
            <input id="rec-end" type="date" className={ui.input} value={end} onChange={(e) => setEnd(e.target.value)} />
          </div>
          <div className={ui.field}>
            <label htmlFor="rec-email">Email each invoice to (optional)</label>
            <input id="rec-email" type="email" className={ui.input} value={email} disabled={!auto} onChange={(e) => setEmail(e.target.value)} />
          </div>
        </div>
        <label style={{ display: "flex", gap: 8, alignItems: "center", margin: "8px 0" }}>
          <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} /> Finalize each copy automatically (posts stock and accounts)
        </label>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
          <button type="submit" className={ui.btnPrimary} disabled={create.isPending}>{create.isPending ? "Saving…" : "Start repeating"}</button>
          {create.isSuccess ? <span role="status">Scheduled.</span> : null}
        </div>
        {create.isError ? <p role="alert">{create.error instanceof ApiError ? create.error.message : "Could not schedule this invoice."}</p> : null}
      </form>
    </section>
  );
}

/** All recurring schedules, with pause, resume and delete. */
export function RecurringList() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["recurring"], queryFn: () => api.getListField<Schedule>("/recurring", "schedules") });
  const refresh = () => void qc.invalidateQueries({ queryKey: ["recurring"] });
  const toggle = useMutation({ mutationFn: (s: Schedule) => api.post(`/recurring/${s.id}/${s.active ? "pause" : "resume"}`), onSuccess: refresh });
  const remove = useMutation({ mutationFn: (id: string) => api.delete(`/recurring/${id}`), onSuccess: refresh });
  if (!q.data || q.data.length === 0) return null;
  return (
    <section className={layout.panel} aria-labelledby="recurring-heading">
      <h2 id="recurring-heading">Recurring invoices</h2>
      <div className={ui.tableScroll} tabIndex={0}>
        <table className={ui.table}>
          <caption className="sr-only">Recurring invoice schedules</caption>
          <thead><tr><th scope="col">Customer</th><th scope="col">Based on</th><th scope="col">Repeats</th><th scope="col">Next copy</th><th scope="col">Made</th><th scope="col">Mode</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {q.data.map((s) => (
              <tr key={s.id}>
                <td>{s.customer}</td>
                <td>{s.source_number}</td>
                <td>every {s.interval_count > 1 ? `${s.interval_count} ` : ""}{unit[s.frequency] ?? s.frequency}{s.interval_count > 1 ? "s" : ""}</td>
                <td>{s.active ? s.next_run_date : "Paused"}{s.end_date ? ` (until ${s.end_date})` : ""}</td>
                <td>{s.run_count}</td>
                <td>{s.auto_finalize ? "Finalized" : "Draft"}{s.email_to ? ` · emailed to ${s.email_to}` : ""}</td>
                <td>
                  <button type="button" className={ui.btnGhost} onClick={() => toggle.mutate(s)}>{s.active ? "Pause" : "Resume"}</button>{" "}
                  <button type="button" className={ui.btnGhost} onClick={() => remove.mutate(s.id)}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
