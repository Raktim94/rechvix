import { parseDec, decToString, dec, SCALE } from "./decimal";

/** Units that are sold whole. Anything else (kg, litre, metre...) may be split. A product's own setting wins when it has one. */
const WHOLE_UNITS = new Set([
  "pcs", "pc", "piece", "pieces", "nos", "no", "unit", "units", "each", "ea", "box", "boxes", "pack", "packs", "pkt", "set", "sets",
  "pair", "pairs", "bag", "bags", "bottle", "bottles", "carton", "cartons", "can", "cans", "tin", "tins", "doz", "dozen", "jar", "jars",
]);

/** How many decimal places a quantity in this unit may have. */
export function unitDecimals(unitLabel: string | null | undefined, override?: number | null): number {
  if (override != null && override >= 0) return Math.min(override, 4);
  return WHOLE_UNITS.has((unitLabel ?? "pcs").trim().toLowerCase()) ? 0 : 3;
}

export const MAX_QTY = 1_000_000;

export type QtyCheck = { ok: true; value: string } | { ok: false; message: string };

/** Checks what the seller typed. Never changes a valid value: "2.50" stays as typed (the stored value is normalised separately). */
export function validateQty(input: string, decimals: number, unitLabel = "pcs"): QtyCheck {
  const s = input.trim();
  if (s === "") return { ok: false, message: "Enter a quantity" };
  if (!/^\d*\.?\d*$/.test(s) || s === ".") return { ok: false, message: "Use numbers only, like 2 or 2.5" };
  const [, frac = ""] = s.split(".");
  if (decimals === 0 && frac.length > 0 && /[1-9]/.test(frac)) return { ok: false, message: `${unitLabel} are sold whole: use 1, 2, 3…` };
  if (frac.length > decimals && /[1-9]/.test(frac.slice(decimals))) {
    return { ok: false, message: decimals === 0 ? `${unitLabel} are sold whole: use 1, 2, 3…` : `At most ${decimals} decimal place${decimals === 1 ? "" : "s"} for ${unitLabel}` };
  }
  const d = parseDec(s);
  if (d === null) return { ok: false, message: "Use numbers only, like 2 or 2.5" };
  if (d === 0n) return { ok: false, message: "Quantity must be more than 0" };
  if (d > BigInt(MAX_QTY) * SCALE) return { ok: false, message: "That quantity is too large" };
  return { ok: true, value: decToString(d, Math.max(decimals, 0)) };
}

/** +/- buttons. Whole units step by 1; measured units step by 1 too (the seller can type a fraction). Never below the smallest step. */
export function stepQty(qty: string, delta: 1 | -1, decimals: number): string {
  const cur = dec(qty);
  const next = cur + BigInt(delta) * SCALE;
  if (next <= 0n) return decToString(SCALE / 10n ** BigInt(decimals), decimals); // smallest sensible quantity (1 or 0.001)
  return decToString(next, decimals);
}
