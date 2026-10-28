import { useMemo } from "react";
import { formatMoneyWhole } from "../../lib/money";
import ui from "../../components/ui.module.css";
import layout from "../DashboardPage.module.css";
import { formatTotals, stageProbability, totalsByCurrency, type Opp, type Owner, type Stage } from "./crm";
import styles from "./CrmExtras.module.css";

const DAY = 86_400_000;

function weighted(opps: Opp[], stages: Stage[]): string {
  const sums = new Map<string, number>();
  for (const o of opps) {
    if (o.stage_kind !== "OPEN") continue;
    sums.set(o.currency, (sums.get(o.currency) ?? 0) + (Number(o.value) || 0) * stageProbability(o, stages));
  }
  const parts = [...sums.entries()].sort((a, b) => b[1] - a[1]).map(([currency, amount]) => formatMoneyWhole({ amount: String(Math.round(amount)), currency }));
  return parts.length ? parts.join(" + ") : "—";
}

/** The reports a sales manager asks for, drawn from the leads already loaded: funnel, forecast, sources, team and a 14-day trend. */
export function CrmInsights({ opps, stages, owners }: { opps: Opp[]; stages: Stage[]; owners: Owner[] }) {
  const ordered = useMemo(() => [...stages].sort((a, b) => a.sort_order - b.sort_order), [stages]);
  const open = opps.filter((o) => o.stage_kind === "OPEN");
  const won = opps.filter((o) => o.stage_kind === "WON");
  const lost = opps.filter((o) => o.stage_kind === "LOST");
  const decided = won.length + lost.length;

  const maxCount = Math.max(1, ...ordered.map((s) => opps.filter((o) => o.stage_id === s.id).length));

  const sources = useMemo(() => {
    const m = new Map<string, { n: number; won: number; value: number }>();
    for (const o of opps) {
      const k = o.source?.trim() || "Unknown";
      const r = m.get(k) ?? { n: 0, won: 0, value: 0 };
      r.n++;
      if (o.stage_kind === "WON") r.won++;
      r.value += Number(o.value) || 0;
      m.set(k, r);
    }
    return [...m.entries()].sort((a, b) => b[1].n - a[1].n);
  }, [opps]);

  const team = useMemo(() => {
    const names = new Map(owners.map((o) => [o.id, o.name]));
    const m = new Map<string, Opp[]>();
    for (const o of opps) {
      const k = o.owner_user_id ? (names.get(o.owner_user_id) ?? "Former user") : "Unassigned";
      m.set(k, [...(m.get(k) ?? []), o]);
    }
    return [...m.entries()].sort((a, b) => b[1].length - a[1].length);
  }, [opps, owners]);

  const trend = useMemo(() => {
    const today = new Date();
    const start = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime();
    const days = Array.from({ length: 14 }, (_, i) => ({ t: start - (13 - i) * DAY, n: 0 }));
    for (const o of opps) {
      if (!o.created_at) continue;
      const t = new Date(o.created_at).getTime();
      const d = days.find((x) => t >= x.t && t < x.t + DAY);
      if (d) d.n++;
    }
    return days;
  }, [opps]);
  const trendMax = Math.max(1, ...trend.map((d) => d.n));
  const added = trend.reduce((t, d) => t + d.n, 0);

  if (opps.length === 0) return <p className={layout.emptyState}>Reports appear here once you have leads.</p>;

  return (
    <div style={{ display: "grid", gap: "var(--space-4)" }}>
      <section className={styles.kpis} aria-label="Key figures">
        <div className={styles.kpi}>
          <span className="srOnly">Open pipeline</span>
          <span aria-hidden="true" style={{ fontSize: "var(--text-xs)", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.06em", color: "var(--color-text-muted)" }}>Open pipeline</span>
          <strong style={{ fontSize: "var(--text-xl)" }}>{formatTotals(open)}</strong>
          <span className={ui.muted}>{open.length} open</span>
        </div>
        <div className={styles.kpi}>
          <span aria-hidden="true" style={{ fontSize: "var(--text-xs)", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.06em", color: "var(--color-text-muted)" }}>Weighted forecast</span>
          <strong style={{ fontSize: "var(--text-xl)" }}>{weighted(opps, stages)}</strong>
          <span className={ui.muted}>Open value × chance of winning by stage</span>
        </div>
        <div className={styles.kpi}>
          <span aria-hidden="true" style={{ fontSize: "var(--text-xs)", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.06em", color: "var(--color-text-muted)" }}>Win rate</span>
          <strong style={{ fontSize: "var(--text-xl)" }}>{decided > 0 ? `${Math.round((won.length / decided) * 100)}%` : "—"}</strong>
          <span className={ui.muted}>{won.length} won, {lost.length} lost</span>
        </div>
        <div className={styles.kpi}>
          <span aria-hidden="true" style={{ fontSize: "var(--text-xs)", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.06em", color: "var(--color-text-muted)" }}>New in 14 days</span>
          <strong style={{ fontSize: "var(--text-xl)" }}>{added}</strong>
          <span className={ui.muted}>Won value {formatTotals(won)}</span>
        </div>
      </section>

      <div className={styles.insightGrid}>
        <section className={layout.panel} aria-labelledby="funnel-h">
          <h2 id="funnel-h">Funnel</h2>
          <ul className={styles.bars}>
            {ordered.map((s) => {
              const items = opps.filter((o) => o.stage_id === s.id);
              return (
                <li key={s.id} className={styles.barRow}>
                  <span>{s.name}</span>
                  <span className={styles.barTrack} aria-hidden="true">
                    <span className={styles.barFill} data-kind={s.kind} style={{ display: "block", width: `${(items.length / maxCount) * 100}%` }} />
                  </span>
                  <span className={styles.barNum}>
                    {items.length} · {formatTotals(items)}
                  </span>
                </li>
              );
            })}
          </ul>
        </section>

        <section className={layout.panel} aria-labelledby="trend-h">
          <h2 id="trend-h">New leads, last 14 days</h2>
          <div className={styles.trend} role="img" aria-label={`New leads per day, last 14 days: ${trend.map((d) => d.n).join(", ")}`}>
            {trend.map((d) => (
              <div key={d.t} className={styles.trendCol}>
                {d.n > 0 ? <span>{d.n}</span> : null}
                <span className={styles.trendBar} style={{ height: `${(d.n / trendMax) * 80}px` }} />
                <span>{new Date(d.t).getDate()}</span>
              </div>
            ))}
          </div>
        </section>

        <section className={layout.panel} aria-labelledby="src-h">
          <h2 id="src-h">Where leads come from</h2>
          <div className={ui.tableScroll} tabIndex={0}>
            <table className={`${ui.table} ${styles.sources}`}>
              <thead>
                <tr>
                  <th scope="col">Source</th>
                  <th scope="col" className="num">Leads</th>
                  <th scope="col" className="num">Won</th>
                  <th scope="col" className="num">Conversion</th>
                </tr>
              </thead>
              <tbody>
                {sources.map(([k, r]) => (
                  <tr key={k}>
                    <td>{k}</td>
                    <td className="num">{r.n}</td>
                    <td className="num">{r.won}</td>
                    <td className="num">{Math.round((r.won / r.n) * 100)}%</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className={layout.panel} aria-labelledby="team-h">
          <h2 id="team-h">By owner</h2>
          <div className={ui.tableScroll} tabIndex={0}>
            <table className={`${ui.table} ${styles.sources}`}>
              <thead>
                <tr>
                  <th scope="col">Owner</th>
                  <th scope="col" className="num">Open</th>
                  <th scope="col" className="num">Won</th>
                  <th scope="col" className="num">Open value</th>
                </tr>
              </thead>
              <tbody>
                {team.map(([name, list]) => (
                  <tr key={name}>
                    <td>{name}</td>
                    <td className="num">{list.filter((o) => o.stage_kind === "OPEN").length}</td>
                    <td className="num">{list.filter((o) => o.stage_kind === "WON").length}</td>
                    <td className="num">{totalsByCurrency(list.filter((o) => o.stage_kind === "OPEN")).length ? formatTotals(list.filter((o) => o.stage_kind === "OPEN")) : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
  );
}
