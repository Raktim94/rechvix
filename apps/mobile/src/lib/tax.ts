import { dec, minorToString, parseMinor, mulDec, roundRatio, SCALE, toMinor, type Dec } from "./decimal";

/** How the entered unit price relates to GST.
 *  INCLUSIVE: the price is what the customer pays, GST included ("sticker price"). Tax is taken OUT of it: GST = gross - gross/(1+rate).
 *  EXCLUSIVE: the price is before GST; tax is added on top. This is also what every sale made before this option existed used. */
export type PricingMode = "INCLUSIVE" | "EXCLUSIVE";

export interface TaxLineIn { qty: string; price: string; discount: string; gstRate?: string | null }
export interface TaxLineOut {
  rate: string;
  taxableMinor: bigint;
  taxMinor: bigint;
  grossMinor: bigint;
  cgstMinor: bigint;
  sgstMinor: bigint;
  igstMinor: bigint;
  /** true when quantity, price or discount could not be read as a non-negative number */
  invalid: boolean;
}
export interface TaxComponent { label: "CGST" | "SGST" | "IGST"; minor: bigint }
export interface RateGroup { rate: string; taxableMinor: bigint; taxMinor: bigint; grossMinor: bigint }
export interface InvoiceCalc {
  mode: PricingMode;
  interState: boolean;
  lines: TaxLineOut[];
  taxableMinor: bigint;
  taxMinor: bigint;
  /** What the customer pays. In INCLUSIVE mode this is exactly the sum of the entered line amounts. */
  totalMinor: bigint;
  components: TaxComponent[];
  byRate: RateGroup[];
}

/** Rounds each tax component half-up to whole minor units on its own, then derives the rest, exactly as the Rechvix server does
 *  (internal/modules/gstindia/engine.go), so an invoice made on the phone and the one the server books cannot disagree:
 *    - line amount G = qty x unit price - line discount. The discount comes off the amount the customer sees: the GST-inclusive
 *      amount in INCLUSIVE mode, the pre-tax amount in EXCLUSIVE mode.
 *    - INCLUSIVE: taxable value = G / (1 + rate/100); each tax component = taxable x its rate, rounded; taxable shown = G - tax.
 *    - EXCLUSIVE: taxable = G; each component = G x its rate, rounded; gross = taxable + tax.
 *    - Same state: CGST and SGST are each half the rate. Different state: one IGST at the full rate. */
export function calcLine(l: TaxLineIn, mode: PricingMode, interState: boolean): TaxLineOut {
  const q = dec(l.qty), p = dec(l.price), disc = dec(l.discount);
  const rateStr = l.gstRate && l.gstRate !== "" ? l.gstRate : "0";
  const bad = [l.qty, l.price, l.discount || "0"].some((v) => v !== "" && v != null && !/^\d*\.?\d*$/.test(v.trim()));
  const R: Dec = dec(rateStr);
  let G = mulDec(q, p) - disc;
  const invalid = bad || G < 0n;
  if (G < 0n) G = 0n;
  const noTax = R === 0n;

  // value (in minor units) of ONE component, from the exact rational n / d, where n and d are chosen per mode and split below.
  const n = G * R;
  let half = 0n, igst = 0n;
  if (!noTax) {
    if (mode === "INCLUSIVE") {
      half = roundRatio(n * 100n, SCALE * (2n * (100n * SCALE + R)));
      igst = roundRatio(n * 100n, SCALE * (100n * SCALE + R));
    } else {
      half = roundRatio(n * 100n, 200n * SCALE * SCALE);
      igst = roundRatio(n * 100n, 100n * SCALE * SCALE);
    }
  }
  const cgst = interState ? 0n : half, sgst = interState ? 0n : half, ig = interState ? igst : 0n;
  const tax = cgst + sgst + ig;
  let taxable: bigint, gross: bigint;
  if (mode === "INCLUSIVE") {
    gross = toMinor(G);
    taxable = gross - tax;
  } else {
    taxable = toMinor(G);
    gross = taxable + tax;
  }
  return { rate: decToRate(R), taxableMinor: taxable, taxMinor: tax, grossMinor: gross, cgstMinor: cgst, sgstMinor: sgst, igstMinor: ig, invalid };
}

const decToRate = (r: Dec): string => {
  const whole = r / SCALE, frac = String(r % SCALE).padStart(10, "0").replace(/0+$/, "");
  return frac ? `${whole}.${frac}` : String(whole);
};

export function calcInvoice(lines: TaxLineIn[], mode: PricingMode, interState = false): InvoiceCalc {
  const out = lines.map((l) => calcLine(l, mode, interState));
  let taxable = 0n, tax = 0n, total = 0n, cgst = 0n, sgst = 0n, igst = 0n;
  const groups = new Map<string, RateGroup>();
  for (const l of out) {
    taxable += l.taxableMinor; tax += l.taxMinor; total += l.grossMinor; cgst += l.cgstMinor; sgst += l.sgstMinor; igst += l.igstMinor;
    const g = groups.get(l.rate) ?? { rate: l.rate, taxableMinor: 0n, taxMinor: 0n, grossMinor: 0n };
    g.taxableMinor += l.taxableMinor; g.taxMinor += l.taxMinor; g.grossMinor += l.grossMinor;
    groups.set(l.rate, g);
  }
  const components: TaxComponent[] = interState
    ? (igst > 0n ? [{ label: "IGST", minor: igst }] : [])
    : (cgst + sgst > 0n ? [{ label: "CGST", minor: cgst }, { label: "SGST", minor: sgst }] : []);
  return {
    mode, interState, lines: out, taxableMinor: taxable, taxMinor: tax, totalMinor: total, components,
    byRate: [...groups.values()].sort((a, b) => Number(dec(a.rate) - dec(b.rate))),
  };
}

// ---------------------------------------------------------------- a stored copy of the figures, so an invoice never changes

/** Saved with the sale. Old invoices are rendered from this, not recomputed, so they stay exactly as issued even if rules change later. */
export interface CalcSnapshot {
  mode: PricingMode;
  interState: boolean;
  taxable: string;
  tax: string;
  total: string;
  components: { label: "CGST" | "SGST" | "IGST"; amount: string }[];
  lines: { rate: string; taxable: string; tax: string; gross: string }[];
  byRate: { rate: string; taxable: string; tax: string; gross: string }[];
}

export function snapshot(c: InvoiceCalc): CalcSnapshot {
  const s = (m: bigint) => minorToString(m);
  return {
    mode: c.mode, interState: c.interState, taxable: s(c.taxableMinor), tax: s(c.taxMinor), total: s(c.totalMinor),
    components: c.components.map((x) => ({ label: x.label, amount: s(x.minor) })),
    lines: c.lines.map((l) => ({ rate: l.rate, taxable: s(l.taxableMinor), tax: s(l.taxMinor), gross: s(l.grossMinor) })),
    byRate: c.byRate.map((g) => ({ rate: g.rate, taxable: s(g.taxableMinor), tax: s(g.taxMinor), gross: s(g.grossMinor) })),
  };
}

/** Read a stored snapshot back as an InvoiceCalc. */
export function fromSnapshot(sn: CalcSnapshot): InvoiceCalc {
  const m = (x: string) => parseMinor(x);
  return {
    mode: sn.mode, interState: sn.interState, taxableMinor: m(sn.taxable), taxMinor: m(sn.tax), totalMinor: m(sn.total),
    components: sn.components.map((x) => ({ label: x.label, minor: m(x.amount) })),
    lines: sn.lines.map((l) => ({ rate: l.rate, taxableMinor: m(l.taxable), taxMinor: m(l.tax), grossMinor: m(l.gross), cgstMinor: 0n, sgstMinor: 0n, igstMinor: 0n, invalid: false })),
    byRate: sn.byRate.map((g) => ({ rate: g.rate, taxableMinor: m(g.taxable), taxMinor: m(g.tax), grossMinor: m(g.gross) })),
  };
}

export const GST_RATES = ["0", "5", "12", "18", "28"] as const;

/** First two digits of a GSTIN are the state code. Anything else (blank, malformed) = unknown: never guessed. */
export function stateFromGstin(gstin: string | null | undefined): string | null {
  const m = /^(\d{2})[0-9A-Z]{13}$/.exec((gstin ?? "").trim().toUpperCase());
  return m ? m[1]! : null;
}

/** Different state only when BOTH states are known and differ. Unknown customer state is treated as the seller's own state
 *  (the server's behaviour too), and the invoice says so instead of pretending to know. */
export function isInterState(sellerState: string | null | undefined, customerState: string | null | undefined): boolean {
  return !!sellerState && !!customerState && sellerState !== customerState;
}

export function nextNumber(prefix: string, counter: number): string {
  return `${prefix}${String(counter).padStart(4, "0")}`;
}
