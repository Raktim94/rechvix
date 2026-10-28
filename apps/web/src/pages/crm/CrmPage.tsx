import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useAuth } from "../../auth/AuthProvider";
import { ImportPanel } from "../../components/ImportPanel";
import { PlusIcon, SearchIcon } from "../../components/icons";
import ui from "../../components/ui.module.css";
import { api } from "../../lib/api-client";
import { formatMoney, formatMoneyWhole } from "../../lib/money";
import layout from "../DashboardPage.module.css";
import { ContactActions } from "./ContactActions";
import { formatTotals, matches, useCrmData, useMoveOpportunity, whenLabel, who, type Activity, type Opp, type Owner, type Stage } from "./crm";
import styles from "./CrmPage.module.css";
import { NewLeadDialog } from "./NewLeadDialog";
import { OpportunityDrawer } from "./OpportunityDrawer";

type View = "board" | "list";
const VIEW_KEY = "rechvix:crm-view";

function readView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === "list" ? "list" : "board";
  } catch {
    return "board";
  }
}

function ErrorBanner({ what, onRetry }: { what: string; onRetry: () => void }) {
  return (
    <div className={styles.errorBanner} role="alert">
      <span>{what} could not be loaded.</span>
      <button type="button" className={ui.btnSecondary} onClick={onRetry}>
        Try again
      </button>
    </div>
  );
}

function Summary({ opps, tasks }: { opps: Opp[]; tasks: Activity[] }) {
  const open = opps.filter((o) => o.stage_kind === "OPEN");
  const won = opps.filter((o) => o.stage_kind === "WON");
  const decided = won.length + opps.filter((o) => o.stage_kind === "LOST").length;
  const lateTasks = tasks.filter((t) => whenLabel(t.due_at).late).length;
  const lateClose = open.filter((o) => whenLabel(o.expected_close).late).length;
  return (
    <section className={styles.summary} aria-label="Pipeline summary">
      <div className={styles.summaryItem}>
        <span className={styles.summaryLabel}>Open pipeline</span>
        <span className={styles.summaryValue}>{formatTotals(open)}</span>
        <span className={styles.summaryHint}>
          {open.length} open {open.length === 1 ? "deal" : "deals"}
        </span>
      </div>
      <div className={styles.summaryItem}>
        <span className={styles.summaryLabel}>Won</span>
        <span className={styles.summaryValue}>{formatTotals(won)}</span>
        <span className={styles.summaryHint}>{decided > 0 ? `${Math.round((won.length / decided) * 100)}% of decided deals` : "No deals decided yet"}</span>
      </div>
      <div className={styles.summaryItem}>
        <span className={styles.summaryLabel}>Follow-ups</span>
        <span className={`${styles.summaryValue} ${lateTasks > 0 ? styles.late : ""}`}>{tasks.length}</span>
        <span className={`${styles.summaryHint} ${lateTasks > 0 ? styles.late : ""}`}>{lateTasks > 0 ? `${lateTasks} overdue` : "Nothing overdue"}</span>
      </div>
      <div className={styles.summaryItem}>
        <span className={styles.summaryLabel}>Past expected close</span>
        <span className={`${styles.summaryValue} ${lateClose > 0 ? styles.late : ""}`}>{lateClose}</span>
        <span className={styles.summaryHint}>{lateClose > 0 ? "Open deals to review" : "All on schedule"}</span>
      </div>
    </section>
  );
}

function DealCard({ opp, owner, selected, onOpen }: { opp: Opp; owner: string | undefined; selected: boolean; onOpen: () => void }) {
  const close = whenLabel(opp.expected_close);
  const late = close.late && opp.stage_kind === "OPEN";
  return (
    <li>
      <article
        className={styles.card}
        data-selected={selected}
        draggable
        onDragStart={(e) => {
          e.dataTransfer.setData("text/plain", opp.id);
          e.dataTransfer.effectAllowed = "move";
        }}
      >
        <button type="button" className={styles.cardOpen} onClick={onOpen} aria-label={`Open ${opp.title}`}>
          <span className={styles.cardTitle}>{opp.title}</span>
          <span className={styles.cardWho}>{who(opp)}</span>
        </button>
        <span className={styles.cardValue}>{formatMoneyWhole({ amount: opp.value, currency: opp.currency })}</span>
        <span className={styles.cardMeta}>
          {opp.expected_close ? <span className={late ? styles.late : undefined}>{late ? close.label : `Close ${close.label}`}</span> : null}
          {opp.open_tasks > 0 ? (
            <span>
              {opp.open_tasks} {opp.open_tasks === 1 ? "task" : "tasks"}
            </span>
          ) : null}
          <span>{owner ?? "Unassigned"}</span>
        </span>
        <ContactActions opp={opp} />
      </article>
    </li>
  );
}

function Board({ stages, opps, owners, selected, onOpen, onMove }: { stages: Stage[]; opps: Opp[]; owners: Map<string, string>; selected: string | null; onOpen: (id: string) => void; onMove: (id: string, stageId: string) => void }) {
  const [over, setOver] = useState<string | null>(null);
  return (
    <div className={styles.board} style={{ gridTemplateColumns: stages.map((s) => (s.kind === "OPEN" ? "minmax(190px, 1fr)" : "minmax(140px, 0.7fr)")).join(" ") }} tabIndex={0} role="group" aria-label="Pipeline board">
      {stages.map((st) => {
        const items = opps.filter((o) => o.stage_id === st.id);
        return (
          <section
            key={st.id}
            className={styles.column}
            data-over={over === st.id}
            aria-label={`${st.name}, ${items.length} ${items.length === 1 ? "deal" : "deals"}`}
            onDragOver={(e) => {
              e.preventDefault();
              e.dataTransfer.dropEffect = "move";
              setOver(st.id);
            }}
            onDragLeave={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(null);
            }}
            onDrop={(e) => {
              e.preventDefault();
              setOver(null);
              const id = e.dataTransfer.getData("text/plain");
              const dragged = opps.find((o) => o.id === id);
              if (dragged && dragged.stage_id !== st.id) onMove(id, st.id);
            }}
          >
            <div className={styles.columnHead}>
              <h3 className={styles.columnTitle} data-kind={st.kind}>
                <span>{st.name}</span>
                <span>{items.length}</span>
              </h3>
              <span className={styles.columnTotal}>{items.length > 0 ? formatTotals(items) : "—"}</span>
            </div>
            {items.length === 0 ? (
              <p className={styles.empty}>Drop a deal here</p>
            ) : (
              <ul className={styles.cards}>
                {items.map((o) => (
                  <DealCard key={o.id} opp={o} owner={o.owner_user_id ? owners.get(o.owner_user_id) : undefined} selected={selected === o.id} onOpen={() => onOpen(o.id)} />
                ))}
              </ul>
            )}
          </section>
        );
      })}
    </div>
  );
}

function ListView({ stages, opps, owners, onOpen }: { stages: Stage[]; opps: Opp[]; owners: Map<string, string>; onOpen: (id: string) => void }) {
  const stageName = new Map(stages.map((s) => [s.id, s.name]));
  return (
    <div className={ui.tableScroll} tabIndex={0}>
      <table className={ui.table}>
        <thead>
          <tr>
            <th scope="col">Deal</th>
            <th scope="col">Stage</th>
            <th scope="col" style={{ textAlign: "right" }}>
              Value
            </th>
            <th scope="col">Expected close</th>
            <th scope="col">Owner</th>
            <th scope="col">Contact</th>
          </tr>
        </thead>
        <tbody>
          {opps.map((o) => {
            const close = whenLabel(o.expected_close);
            return (
              <tr key={o.id}>
                <td>
                  <button type="button" className={ui.linkRowButton} onClick={() => onOpen(o.id)}>
                    <strong>{o.title}</strong>
                    <br />
                    <span className={ui.muted}>{who(o)}</span>
                  </button>
                </td>
                <td>
                  <span className={ui.badge} data-tone={o.stage_kind === "WON" ? "positive" : o.stage_kind === "LOST" ? "negative" : "neutral"}>
                    {stageName.get(o.stage_id) ?? "—"}
                  </span>
                </td>
                <td className="num" style={{ textAlign: "right" }}>
                  {formatMoney({ amount: o.value, currency: o.currency })}
                </td>
                <td className={close.late && o.stage_kind === "OPEN" ? styles.late : undefined}>{close.label || "—"}</td>
                <td>{(o.owner_user_id && owners.get(o.owner_user_id)) || <span className={ui.muted}>Unassigned</span>}</td>
                <td>
                  <ContactActions opp={o} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export function CrmPage() {
  const { session } = useAuth();
  const qc = useQueryClient();
  const { stages, opps, tasks, owners } = useCrmData();
  const move = useMoveOpportunity(stages.data);
  const complete = useMutation({
    mutationFn: (t: Activity) => api.post(`/crm/activities/${t.id}/done`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["crm"] }),
  });
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);
  const [view, setViewState] = useState<View>(readView);
  const [query, setQuery] = useState("");
  const [ownerFilter, setOwnerFilter] = useState("all");

  const setView = (v: View) => {
    setViewState(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch {
      // Remembering the view is a convenience; the page works without it.
    }
  };

  const ownerNames = useMemo(() => new Map((owners.data ?? []).map((o: Owner) => [o.id, o.name])), [owners.data]);
  const all = useMemo(() => opps.data ?? [], [opps.data]);
  const filtered = useMemo(
    () =>
      all.filter((o) => {
        if (!matches(o, query)) return false;
        if (ownerFilter === "all") return true;
        if (ownerFilter === "none") return !o.owner_user_id;
        if (ownerFilter === "me") return o.owner_user_id === session?.userId;
        return o.owner_user_id === ownerFilter;
      }),
    [all, query, ownerFilter, session?.userId],
  );
  const filtering = query.trim() !== "" || ownerFilter !== "all";
  const current = all.find((o) => o.id === selected);
  const sortedTasks = [...(tasks.data ?? [])].sort((a, b) => (a.due_at ?? "9999").localeCompare(b.due_at ?? "9999"));

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>CRM</h1>
          <p className={layout.subtitle}>Leads and deals, from first contact to a customer. Values keep their own currency.</p>
        </div>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <button type="button" className={ui.btnSecondary} aria-expanded={importing} onClick={() => setImporting((v) => !v)}>
            Import leads
          </button>
          <button type="button" className={ui.btnPrimary} onClick={() => setCreating(true)}>
            <PlusIcon /> New lead
          </button>
        </div>
      </div>

      {importing ? (
        <ImportPanel
          title="Import leads from a file"
          path="/crm/opportunities/import"
          mapColumns
          defaultOpen
          columns={["company_name", "contact_name", "contact_phone", "contact_email", "value", "source", "stage", "owner", "expected_close", "notes"]}
          sampleRows={[{ company_name: "Asha General Stores", contact_name: "Asha Verma", contact_phone: "9876543210", contact_email: "asha@example.com", value: "25000", source: "Website", stage: "New", owner: "", expected_close: "2026-12-31", notes: "Wants a quote for billing software" }]}
          onImported={() => void qc.invalidateQueries({ queryKey: ["crm"] })}
        />
      ) : null}

      {opps.isPending || tasks.isPending ? (
        <div className={styles.skeletonRow} aria-hidden="true">
          {Array.from({ length: 4 }, (_, i) => (
            <div key={i} className={layout.skeleton} />
          ))}
        </div>
      ) : opps.isError || tasks.isError ? null : (
        <Summary opps={all} tasks={tasks.data ?? []} />
      )}

      <div className={styles.toolbar} role="search">
        <div className={`${styles.search} ${ui.field}`} style={{ position: "relative" }}>
          <label htmlFor="crm-search" className="srOnly">
            Search leads
          </label>
          <input id="crm-search" type="search" className={ui.input} style={{ paddingLeft: 34 }} placeholder="Search deal, company, contact, phone" value={query} onChange={(e) => setQuery(e.target.value)} />
          <span aria-hidden="true" style={{ position: "absolute", left: 10, top: 9, color: "var(--color-text-muted)" }}>
            <SearchIcon />
          </span>
        </div>
        <label className="srOnly" htmlFor="crm-owner">
          Owner
        </label>
        <select id="crm-owner" className={ui.select} style={{ width: "auto" }} value={ownerFilter} onChange={(e) => setOwnerFilter(e.target.value)}>
          <option value="all">All owners</option>
          <option value="me">My deals</option>
          <option value="none">Unassigned</option>
          {(owners.data ?? []).map((o) => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
        </select>
        <div className={styles.segmented} role="group" aria-label="View">
          <button type="button" aria-pressed={view === "board"} onClick={() => setView("board")}>
            Board
          </button>
          <button type="button" aria-pressed={view === "list"} onClick={() => setView("list")}>
            List
          </button>
        </div>
        {opps.data ? (
          <span className={styles.count} aria-live="polite">
            {filtering ? `${filtered.length} of ${all.length}` : all.length} {all.length === 1 ? "deal" : "deals"}
          </span>
        ) : null}
      </div>

      {opps.isError || stages.isError ? (
        <ErrorBanner what="The pipeline" onRetry={() => void Promise.all([opps.refetch(), stages.refetch()])} />
      ) : !opps.data || !stages.data ? (
        <div className={layout.skeleton} style={{ height: 260 }} aria-hidden="true" />
      ) : all.length === 0 ? (
        <div className={layout.emptyState}>
          <p style={{ marginBottom: 12 }}>No leads yet. Add the first one, or import a whole list from a spreadsheet, and move each across the board as the conversation moves on.</p>
          <button type="button" className={ui.btnPrimary} onClick={() => setCreating(true)}>
            Add a lead
          </button>
        </div>
      ) : filtered.length === 0 ? (
        <div className={layout.emptyState}>
          <p style={{ marginBottom: 12 }}>No deals match this search.</p>
          <button
            type="button"
            className={ui.btnSecondary}
            onClick={() => {
              setQuery("");
              setOwnerFilter("all");
            }}
          >
            Clear filters
          </button>
        </div>
      ) : view === "board" ? (
        <Board stages={stages.data} opps={filtered} owners={ownerNames} selected={selected} onOpen={setSelected} onMove={(id, stageId) => move.mutate({ id, stageId })} />
      ) : (
        <ListView stages={stages.data} opps={filtered} owners={ownerNames} onOpen={setSelected} />
      )}
      {move.isError ? (
        <p role="alert" className={styles.errorBanner}>
          That deal could not be moved, so it stayed where it was.
        </p>
      ) : null}

      <section className={layout.panel} aria-labelledby="crm-tasks-heading">
        <h2 id="crm-tasks-heading">Follow-ups to do</h2>
        {tasks.isError ? (
          <ErrorBanner what="Follow-ups" onRetry={() => void tasks.refetch()} />
        ) : tasks.isPending ? (
          <div className={layout.skeleton} style={{ height: 80 }} aria-hidden="true" />
        ) : sortedTasks.length === 0 ? (
          <p className={styles.sub}>Nothing waiting. Schedule a task on a deal and it shows up here, oldest first.</p>
        ) : (
          <ul className={styles.followUps}>
            {sortedTasks.map((t) => {
              const w = whenLabel(t.due_at);
              return (
                <li key={t.id}>
                  <input type="checkbox" aria-label={`Mark "${t.subject}" done`} checked={false} disabled={complete.isPending} onChange={() => complete.mutate(t)} />
                  <span className={styles.followUpText}>
                    <button type="button" onClick={() => setSelected(t.opportunity_id)}>
                      {t.subject}
                    </button>
                    <span className={styles.sub}>{t.opportunity_title}</span>
                  </span>
                  {w.label ? <span className={`${styles.sub} ${w.late ? styles.late : ""}`}>{w.label}</span> : null}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <OpportunityDrawer opp={current} stages={stages.data ?? []} owners={owners.data} onClose={() => setSelected(null)} />
      <NewLeadDialog
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(o) => {
          setCreating(false);
          setSelected(o.id);
        }}
      />
    </div>
  );
}
