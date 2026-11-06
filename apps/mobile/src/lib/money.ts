import { minorToNumber } from "./decimal";

/** Offline estimates only. The server stays the source of truth: tax, rounding and the grand total are
 *  computed there when the sale is finalised, so everything here is labelled "before tax" in the UI. */
export function num(v: string | number | null | undefined): number {
  const n = typeof v === "number" ? v : parseFloat(v ?? "");
  return Number.isFinite(n) ? n : 0;
}

export const round2 = (n: number): number => Math.round((n + Number.EPSILON) * 100) / 100;

export function formatMoney(n: number, currency = "INR"): string {
  try {
    return new Intl.NumberFormat(currency === "INR" ? "en-IN" : undefined, { style: "currency", currency, maximumFractionDigits: 2 }).format(n);
  } catch {
    return `${currency} ${n.toFixed(2)}`;
  }
}

export interface PricedLine { qty: string; price: string; discount: string }

export const lineAmount = (l: PricedLine): number => round2(num(l.qty) * num(l.price) - num(l.discount));
export const subtotal = (lines: PricedLine[]): number => round2(lines.reduce((s, l) => s + lineAmount(l), 0));

/** The server takes quantities as decimal strings. */
export const isValidQty = (s: string): boolean => /^\d+(\.\d{1,4})?$/.test(s) && num(s) > 0;

export function bumpQty(qty: string, delta: number): string {
  const next = round2(num(qty) + delta);
  return String(next < 0 ? 0 : next);
}


// ---------------------------------------------------------------- minor-unit (BigInt) display helpers
/** Money held as whole minor units (paise/cents), formatted for display only. All arithmetic stays in BigInt. */
export function formatMinor(m: bigint, currency = "INR"): string {
  return formatMoney(minorToNumber(m), currency);
}
