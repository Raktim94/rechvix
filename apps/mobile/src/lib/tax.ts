import { num, round2 } from "./money";

export interface TaxLine { qty: string; price: string; discount: string; gstRate?: string | null }
export interface Totals {
  subtotal: number;
  tax: number;
  total: number;
  /** Tax grouped by rate, for the invoice breakdown. */
  byRate: { rate: number; base: number; tax: number }[];
}

/** Convert a tax-inclusive unit price back to its taxable value. */
export function exclusiveUnitPrice(gross: string | number, rate: string | number | null | undefined): number {
  const amount = num(gross);
  const percent = num(rate);
  return percent > 0 ? round2(amount * 100 / (100 + percent)) : round2(amount);
}

/** Local estimate with prices exclusive of tax. The server recomputes using the same convention on sync. */
export function calcTotals(lines: TaxLine[]): Totals {
  const groups = new Map<number, { base: number; tax: number }>();
  let subtotal = 0, tax = 0;
  for (const l of lines) {
    const base = round2(num(l.qty) * num(l.price) - num(l.discount));
    const rate = num(l.gstRate);
    const t = round2((base * rate) / 100);
    subtotal += base; tax += t;
    const g = groups.get(rate) ?? { base: 0, tax: 0 };
    g.base = round2(g.base + base); g.tax = round2(g.tax + t);
    groups.set(rate, g);
  }
  return {
    subtotal: round2(subtotal), tax: round2(tax), total: round2(subtotal + tax),
    byRate: [...groups.entries()].sort((a, b) => a[0] - b[0]).map(([rate, g]) => ({ rate, ...g })),
  };
}

export const GST_RATES = ["0", "5", "12", "18", "28"] as const;

export function nextNumber(prefix: string, counter: number): string {
  return `${prefix}${String(counter).padStart(4, "0")}`;
}
