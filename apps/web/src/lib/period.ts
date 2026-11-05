/** Report periods. Dates are plain local calendar days ("YYYY-MM-DD"), the
 * format every /reports/* endpoint takes for `from` / `to`. */
export type PeriodKey = "30d" | "month" | "lastMonth" | "quarter" | "fy" | "custom";

export interface Period {
  from: string;
  to: string;
}

function iso(y: number, m: number, d: number): string {
  // Date(y, m, d) normalises overflow (m = -1 → Dec of previous year), then we format locally.
  const dt = new Date(y, m, d);
  const mm = String(dt.getMonth() + 1).padStart(2, "0");
  const dd = String(dt.getDate()).padStart(2, "0");
  return `${dt.getFullYear()}-${mm}-${dd}`;
}

/** The first month of the financial year: April in India, January elsewhere. */
export function fyStartMonth(countryCode: string | undefined): number {
  return countryCode === "IN" ? 3 : 0;
}

export function periodFor(key: Exclude<PeriodKey, "custom">, now: Date, countryCode?: string): Period {
  const y = now.getFullYear();
  const m = now.getMonth();
  const d = now.getDate();
  switch (key) {
    case "30d":
      return { from: iso(y, m, d - 29), to: iso(y, m, d) };
    case "month":
      return { from: iso(y, m, 1), to: iso(y, m, d) };
    case "lastMonth":
      return { from: iso(y, m - 1, 1), to: iso(y, m, 0) };
    case "quarter": {
      const qStart = m - (m % 3);
      return { from: iso(y, qStart, 1), to: iso(y, m, d) };
    }
    case "fy": {
      const start = fyStartMonth(countryCode);
      const fyYear = m >= start ? y : y - 1;
      return { from: iso(fyYear, start, 1), to: iso(y, m, d) };
    }
  }
}

/** Adds from/to to a report path, replacing any already present. */
export function withPeriod(path: string, period: Period | null): string {
  if (!period) return path;
  const [base, query = ""] = path.split("?");
  const params = new URLSearchParams(query);
  params.set("from", period.from);
  params.set("to", period.to);
  return `${base}?${params.toString()}`;
}

/** A custom range is usable only when both days are set and in order. */
export function isValidRange(from: string, to: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(from) && /^\d{4}-\d{2}-\d{2}$/.test(to) && from <= to;
}
