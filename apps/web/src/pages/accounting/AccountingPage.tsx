import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { ReportTable } from "../../components/ReportTable";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import layout from "../DashboardPage.module.css";

interface Account {
  ID: string;
  Code: string;
  Name: string;
  AccountType: string;
  IsActive: boolean;
}

interface FiscalPeriod {
  ID: string;
  StartDate: string;
  EndDate: string;
  Label: string;
  IsLocked: boolean;
  LockedAt: string | null;
}

function todayIsoDate(): string {
  return new Date().toISOString().slice(0, 10);
}
function firstOfMonthIsoDate(): string {
  const d = new Date();
  return new Date(d.getFullYear(), d.getMonth(), 1).toISOString().slice(0, 10);
}

/** GET/POST /accounting/fiscal-periods and the lock/unlock endpoints
 * existed with zero frontend callers — books could never actually be
 * closed. Locking a period is self-service (accounting.post, the same
 * permission that posts any journal); unlocking one already-locked
 * requires accounting.override_locked_period on the backend (Service.
 * SetPeriodLock) — this panel doesn't duplicate that check, it just
 * shows the server's own rejection if the current user doesn't hold it. */
function FiscalPeriodsPanel() {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [startDate, setStartDate] = useState(firstOfMonthIsoDate);
  const [endDate, setEndDate] = useState(todayIsoDate);
  const [label, setLabel] = useState("");

  const periods = useQuery({
    queryKey: ["fiscal-periods"],
    queryFn: () => api.get<FiscalPeriod[]>("/accounting/fiscal-periods"),
  });

  const create = useMutation({
    mutationFn: () =>
      api.post("/accounting/fiscal-periods", {
        start_date: new Date(startDate).toISOString(),
        end_date: new Date(endDate).toISOString(),
        label,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["fiscal-periods"] });
      setLabel("");
      setShowForm(false);
    },
  });

  const setLock = useMutation({
    mutationFn: (vars: { id: string; locked: boolean }) => api.post(`/accounting/fiscal-periods/${vars.id}/${vars.locked ? "lock" : "unlock"}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["fiscal-periods"] }),
  });

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>Fiscal periods</h2>
        <div className={ui.toolbarSpacer} />
        {!showForm ? (
          <button type="button" className={ui.btnPrimary} onClick={() => setShowForm(true)}>
            + New period
          </button>
        ) : null}
      </div>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Locking a period stops new entries from being posted or backdated into it — the usual month/year-end close.
      </p>

      {showForm ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (label.trim() && startDate && endDate) create.mutate();
          }}
          style={{ marginBottom: 20 }}
        >
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="fp-label">Label</label>
              <input id="fp-label" className={ui.input} value={label} onChange={(e) => setLabel(e.target.value)} placeholder="e.g. September 2026" required />
            </div>
            <div className={ui.field}>
              <label htmlFor="fp-start">Start date</label>
              <input id="fp-start" type="date" className={ui.input} value={startDate} onChange={(e) => setStartDate(e.target.value)} />
            </div>
            <div className={ui.field}>
              <label htmlFor="fp-end">End date</label>
              <input id="fp-end" type="date" className={ui.input} value={endDate} onChange={(e) => setEndDate(e.target.value)} />
            </div>
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button type="button" className={ui.btnSecondary} onClick={() => setShowForm(false)}>
              Cancel
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={!label.trim() || create.isPending}>
              {create.isPending ? "Adding…" : "Add period"}
            </button>
          </div>
          {create.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {create.error instanceof ApiError ? create.error.message : "Could not create this fiscal period."}
            </p>
          ) : null}
        </form>
      ) : null}

      {periods.isPending ? (
        <div className={layout.skeleton} style={{ height: 80 }} aria-hidden="true" />
      ) : (periods.data ?? []).length === 0 ? (
        <p className={layout.emptyState}>No fiscal periods yet — without one, books can never be locked closed.</p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Label</th>
                <th scope="col">Start</th>
                <th scope="col">End</th>
                <th scope="col">Status</th>
                <th scope="col" />
              </tr>
            </thead>
            <tbody>
              {(periods.data ?? []).map((p) => (
                <tr key={p.ID}>
                  <td>{p.Label}</td>
                  <td>{new Date(p.StartDate).toLocaleDateString()}</td>
                  <td>{new Date(p.EndDate).toLocaleDateString()}</td>
                  <td>
                    <span className={ui.badge} data-tone={p.IsLocked ? "warning" : "positive"}>
                      {p.IsLocked ? "Locked" : "Open"}
                    </span>
                  </td>
                  <td>
                    <button
                      type="button"
                      className={p.IsLocked ? ui.btnSecondary : ui.btnGhost}
                      disabled={setLock.isPending}
                      onClick={() => setLock.mutate({ id: p.ID, locked: !p.IsLocked })}
                    >
                      {p.IsLocked ? "Unlock" : "Lock"}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {setLock.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {setLock.error instanceof ApiError ? setLock.error.message : "Could not change this period's lock."}
        </p>
      ) : null}
    </div>
  );
}

export function AccountingPage() {
  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => api.get<Account[]>("/accounting/accounts"),
  });

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Accounting</h1>
          <p className={layout.subtitle}>Chart of accounts, trial balance, and who owes what.</p>
        </div>
        <Link to="/expenses" className={ui.btnPrimary}>
          + Record expense
        </Link>
      </div>

      <div className={layout.panel}>
        <h2>Trial balance</h2>
        <ReportTable path="/reports/accounting/trial-balance?format=json" />
      </div>

      <div className={layout.panel}>
        <h2>Receivables (who owes you)</h2>
        <ReportTable path="/reports/accounting/receivables?format=json" emptyLabel="Nobody owes you anything right now." />
      </div>

      <div className={layout.panel}>
        <h2>Payables (what you owe)</h2>
        <ReportTable path="/reports/accounting/payables?format=json" emptyLabel="You don't owe any suppliers right now." />
      </div>

      <div className={layout.panel}>
        <h2>Chart of accounts</h2>
        {accounts.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load the chart of accounts.
          </p>
        ) : accounts.isPending ? (
          <div className={layout.skeleton} style={{ height: 200 }} aria-hidden="true" />
        ) : accounts.data.length === 0 ? (
          <p className={layout.emptyState}>No accounts set up yet.</p>
        ) : (
          <div className={ui.tableScroll}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Code</th>
                  <th scope="col">Name</th>
                  <th scope="col">Type</th>
                </tr>
              </thead>
              <tbody>
                {accounts.data.map((a) => (
                  <tr key={a.ID}>
                    <td className="num">{a.Code}</td>
                    <td>{a.Name}</td>
                    <td>{a.AccountType}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <FiscalPeriodsPanel />
    </div>
  );
}
