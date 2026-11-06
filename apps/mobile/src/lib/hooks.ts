import { useEffect, useState } from "react";
import { useApp } from "../state/app";

/** Re-runs a local-database query whenever local data changes (a sale saved, a sync finished). */
export function useLocal<T>(fn: (db: NonNullable<ReturnType<typeof useApp>["db"]>) => Promise<T>, initial: T, deps: unknown[] = []): T {
  const { db, version } = useApp();
  const [v, setV] = useState<T>(initial);
  useEffect(() => {
    let live = true;
    if (db) void fn(db).then((r) => { if (live) setV(r); });
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [db, version, ...deps]);
  return v;
}

/** The current time, refreshed every 30 s, so screens that depend on "now" (overdue, today) stay right without reading the clock during render. */
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 30_000); return () => clearInterval(t); }, []);
  return now;
}

/** The clock for event handlers (saving a payment, logging a reminder). */
export const nowMs = (): number => Date.now();

export function ago(ms: number | null, now = Date.now()): string {
  if (!ms) return "never";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}
