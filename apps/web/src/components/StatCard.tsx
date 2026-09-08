import type { ReactNode } from "react";
import styles from "./StatCard.module.css";

type Polarity = "neutral" | "positive" | "negative" | "warning";

export function StatCard({
  label,
  value,
  polarity = "neutral",
  icon,
}: {
  label: string;
  value: string;
  polarity?: Polarity;
  icon?: ReactNode;
}) {
  return (
    <div className={styles.card} data-polarity={polarity}>
      {icon ? (
        <span className={styles.icon} aria-hidden="true">
          {icon}
        </span>
      ) : null}
      <span className={styles.body}>
        <span className={styles.label}>{label}</span>
        <span className={styles.value} data-polarity={polarity}>
          {value}
        </span>
      </span>
    </div>
  );
}
