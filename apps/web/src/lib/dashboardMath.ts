/**
 * Pure helpers for the dashboard's period comparison. Amounts here are display
 * approximations (a trend and a percentage), never a figure anything is posted from.
 */

export interface DayPoint {
  date: string;
  total: number;
}

/** YYYY-MM-DD in local time, the key the sales-by-day report uses. */
export function isoDay(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

/** `days` consecutive days ending at `end` (inclusive); days without sales count as zero. */
export function buildSeries(totals: Map<string, number>, end: Date, days: number): DayPoint[] {
  const out: DayPoint[] = [];
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(end.getFullYear(), end.getMonth(), end.getDate() - i);
    const date = isoDay(d);
    out.push({ date, total: totals.get(date) ?? 0 });
  }
  return out;
}

export function sum(points: DayPoint[]): number {
  return points.reduce((acc, p) => acc + p.total, 0);
}

/** Percent change from `previous` to `current`; null when there is nothing to compare against. */
export function percentChange(current: number, previous: number): number | null {
  if (!Number.isFinite(current) || !Number.isFinite(previous) || previous <= 0) return null;
  return ((current - previous) / previous) * 100;
}

/** "12%" / "0.4%" — one decimal only when the figure is under 10, so 12.4 reads "12%". */
export function formatPercent(p: number): string {
  const abs = Math.abs(p);
  return `${abs < 10 ? abs.toFixed(1).replace(/\.0$/, "") : Math.round(abs)}%`;
}
