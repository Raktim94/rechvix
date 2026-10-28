import { leadScore, type Opp, type Stage } from "./crm";
import styles from "./CrmExtras.module.css";

/** HOT / WARM / COLD with the score behind it; the tooltip lists every point so nobody has to trust a black box. */
export function ScoreChip({ opp, stages }: { opp: Opp; stages: Stage[] }) {
  const s = leadScore(opp, stages);
  if (!s) return null;
  const why = s.factors.map((f) => `${f.points}/${f.max} ${f.label}`).join("\n");
  return (
    <span className={styles.chip} data-band={s.band} title={`Lead score ${s.score}/100\n${why}`}>
      {s.band} · {s.score}
    </span>
  );
}
