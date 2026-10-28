import { useMemo } from "react";
import ui from "../../components/ui.module.css";
import layout from "../DashboardPage.module.css";
import { ContactActions } from "./ContactActions";
import { leadScore, whenLabel, who, type Activity, type Opp, type Stage } from "./crm";
import styles from "./CrmExtras.module.css";
import { ScoreChip } from "./ScoreChip";

interface Item {
  opp: Opp;
  rank: number;
  reason: string;
  tone: "late" | "today" | "idea";
}

/** Who to speak to next, in the order a good telecaller would: late follow-ups, today's, deals past their close date, then hot leads nobody has scheduled. */
export function buildQueue(opps: Opp[], tasks: Activity[], stages: Stage[]): Item[] {
  const firstTask = new Map<string, Activity>();
  for (const t of [...tasks].sort((a, b) => (a.due_at ?? "9999").localeCompare(b.due_at ?? "9999"))) {
    if (!firstTask.has(t.opportunity_id)) firstTask.set(t.opportunity_id, t);
  }
  const items: Item[] = [];
  for (const o of opps) {
    if (o.stage_kind !== "OPEN") continue;
    const t = firstTask.get(o.id);
    const due = t ? whenLabel(t.due_at) : null;
    if (t && due?.late) items.push({ opp: o, rank: 0, reason: `${t.subject} · ${due.label}`, tone: "late" });
    else if (t && due?.label === "Today") items.push({ opp: o, rank: 1, reason: `${t.subject} · Today`, tone: "today" });
    else if (whenLabel(o.expected_close).late) items.push({ opp: o, rank: 2, reason: "Past its expected close date", tone: "late" });
    else if (!t && leadScore(o, stages)?.band === "HOT") items.push({ opp: o, rank: 3, reason: "Hot lead with no follow-up booked", tone: "idea" });
    else if (!t && !o.contact_phone && !o.contact_email) items.push({ opp: o, rank: 4, reason: "No way to reach them yet; add a phone or email", tone: "idea" });
  }
  return items.sort((a, b) => a.rank - b.rank || (leadScore(b.opp, stages)?.score ?? 0) - (leadScore(a.opp, stages)?.score ?? 0));
}

const GROUPS: { title: string; ranks: number[] }[] = [
  { title: "Overdue", ranks: [0] },
  { title: "Today", ranks: [1] },
  { title: "Needs attention", ranks: [2, 3, 4] },
];

export function MyDay({ opps, tasks, stages, onOpen }: { opps: Opp[]; tasks: Activity[]; stages: Stage[]; onOpen: (id: string) => void }) {
  const queue = useMemo(() => buildQueue(opps, tasks, stages), [opps, tasks, stages]);
  const next = queue.find((q) => q.opp.contact_phone) ?? queue[0];
  return (
    <section className={layout.panel} aria-labelledby="myday-heading">
      <div className={styles.dayHead}>
        <div>
          <h2 id="myday-heading">My day</h2>
          <p className={ui.muted} style={{ margin: 0 }}>
            {queue.length === 0 ? "You are all caught up." : `${queue.length} ${queue.length === 1 ? "lead needs" : "leads need"} you, most urgent first.`}
          </p>
        </div>
        {next ? (
          next.opp.contact_phone ? (
            <a className={ui.btnPrimary} href={`tel:${next.opp.contact_phone}`} onClick={() => onOpen(next.opp.id)}>
              Call next lead · {next.opp.contact_name || who(next.opp) || next.opp.title}
            </a>
          ) : (
            <button type="button" className={ui.btnPrimary} onClick={() => onOpen(next.opp.id)}>
              Open next lead · {who(next.opp) || next.opp.title}
            </button>
          )
        ) : null}
      </div>
      {queue.length === 0 ? (
        <p className={layout.emptyState}>Nothing is overdue or due today. Add a follow-up to a lead and it appears here on the day.</p>
      ) : (
        GROUPS.map((g) => {
          const rows = queue.filter((q) => g.ranks.includes(q.rank));
          if (rows.length === 0) return null;
          return (
            <div key={g.title}>
              <h3 className={styles.groupTitle}>
                {g.title} ({rows.length})
              </h3>
              <ul className={styles.queue}>
                {rows.map((q) => (
                  <li key={q.opp.id} className={styles.queueItem}>
                    <button type="button" className={styles.queueMain} onClick={() => onOpen(q.opp.id)}>
                      <strong>{q.opp.title}</strong>
                      <span className={ui.muted}>{[who(q.opp), q.opp.contact_name !== who(q.opp) ? q.opp.contact_name : "", q.opp.contact_phone].filter(Boolean).join(" · ")}</span>
                      <span className={styles.reason} data-tone={q.tone}>
                        {q.reason}
                      </span>
                    </button>
                    <div className={styles.queueSide}>
                      <ScoreChip opp={q.opp} stages={stages} />
                      <ContactActions opp={q.opp} />
                    </div>
                  </li>
                ))}
              </ul>
            </div>
          );
        })
      )}
    </section>
  );
}
