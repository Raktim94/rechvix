import { formatLocale } from "../i18n";

/**
 * Money — mirrors internal/platform/money.Money's JSON shape exactly
 * ({amount: string, currency: string}, e.g. {"amount":"1234.50","currency":"INR"}).
 *
 * The amount stays a STRING all the way from the Go backend through this
 * type to the DOM. It is only ever converted to a JS `number` for display
 * formatting (Intl.NumberFormat) or for a chart's approximate visual
 * scale — never for anything that claims to be an exact figure, since a
 * JS float cannot losslessly represent an arbitrary-precision decimal.
 * The backend is the sole source of truth for calculation; this app never
 * recomputes a total from Money values, only displays what it was given
 * (docs/architecture.md §5 — "frontend can preview... but server
 * recalculates and validates" for anything mutating; for read-only
 * display like this, the rule is even simpler: never touch the math).
 */
export interface Money {
  amount: string;
  currency: string;
}

const formatterCache = new Map<string, Intl.NumberFormat>();

// The locale follows the UI language and the active company's country (see
// i18n/index.ts), so digits, grouping, symbol position and the number of
// decimals (JPY has none, KWD has three) all come from Intl, not from here.
function formatterFor(currency: string): Intl.NumberFormat {
  const locale = formatLocale();
  const key = `${locale}|${currency}`;
  let f = formatterCache.get(key);
  if (!f) {
    try {
      f = new Intl.NumberFormat(locale, { style: "currency", currency, currencyDisplay: "symbol" });
    } catch {
      // Unknown currency code: show the code rather than throw during render.
      f = new Intl.NumberFormat(locale, { style: "currency", currency: "XXX", currencyDisplay: "code" });
    }
    formatterCache.set(key, f);
  }
  return f;
}

/** Formats a Money value for display, e.g. "₹1,23,456.50" (en-IN), "1.234,50 €" (es-ES). */
export function formatMoney(m: Money): string {
  const n = Number(m.amount);
  if (Number.isNaN(n)) return `${m.amount} ${m.currency}`;
  return formatterFor(m.currency).format(n);
}

/** True if the amount is exactly zero (string comparison-safe for "0", "0.00", "-0.00"). */
export function isZeroMoney(m: Money): boolean {
  return Number(m.amount) === 0;
}

/** True if the amount is negative. */
export function isNegativeMoney(m: Money): boolean {
  return Number(m.amount) < 0;
}

/** Rough numeric value for chart axes/scales only — never for a displayed total. */
export function moneyToApproxNumber(m: Money): number {
  return Number(m.amount);
}
