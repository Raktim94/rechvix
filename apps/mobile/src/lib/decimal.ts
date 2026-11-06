/** Exact decimal arithmetic for money. No floating point anywhere in the sale, tax or payment path.
 *
 *  A `Dec` is a BigInt holding the value x 10^10. Ten decimal places is enough to hold quantity (up to 4 places) x price (up to 4 places)
 *  exactly. Final money amounts are whole minor units (paise/cents) as BigInt, produced by rounding half-up exactly once, like the server. */
export type Dec = bigint;
export const SCALE_DIGITS = 10;
export const SCALE: bigint = 10n ** 10n;

/** Parses "12", "12.5", ".5", "0.75". Rejects negatives, exponents, junk and more than 10 decimal places (returns null). */
export function parseDec(v: string | number | null | undefined): Dec | null {
  if (v === null || v === undefined) return null;
  const s = (typeof v === "number" ? (Number.isFinite(v) ? String(v) : "") : v).trim();
  const m = /^(\d*)(?:\.(\d*))?$/.exec(s);
  if (!m || (m[1] === "" && (m[2] ?? "") === "")) return null;
  const frac = m[2] ?? "";
  if (frac.length > SCALE_DIGITS) return null;
  return BigInt(m[1] || "0") * SCALE + BigInt(frac.padEnd(SCALE_DIGITS, "0") || "0");
}

export const dec = (v: string | number | null | undefined): Dec => parseDec(v) ?? 0n;

/** a x b, kept at 10 decimal places (exact whenever the true result has at most 10). */
export const mulDec = (a: Dec, b: Dec): Dec => (a * b) / SCALE;

/** round half up of the exact fraction num/den (both positive) to a whole number. */
export const roundRatio = (num: bigint, den: bigint): bigint => (2n * num + den) / (2n * den);

/** A Dec rounded half-up to whole minor units (2 decimal places). */
export const toMinor = (d: Dec): bigint => roundRatio(d * 100n, SCALE);

export const minorToDec = (m: bigint): Dec => (m * SCALE) / 100n;

/** 10500n -> "105.00". Handles negatives (refunds). */
export function minorToString(m: bigint): string {
  const neg = m < 0n;
  const a = neg ? -m : m;
  return `${neg ? "-" : ""}${a / 100n}.${String(a % 100n).padStart(2, "0")}`;
}

export function parseMinor(s: string | null | undefined): bigint {
  const d = parseDec(s);
  return d === null ? 0n : toMinor(d);
}

/** For display only: minor units to a JS number (safe below ~9 x 10^13 currency units). */
export const minorToNumber = (m: bigint): number => Number(m) / 100;

/** Trims a Dec to a plain string with at most `maxDp` decimals and no trailing zeros ("2.50" -> "2.5", "3.000" -> "3"). */
export function decToString(d: Dec, maxDp = 4): string {
  const neg = d < 0n;
  const a = neg ? -d : d;
  const whole = a / SCALE;
  let frac = String(a % SCALE).padStart(SCALE_DIGITS, "0").slice(0, maxDp).replace(/0+$/, "");
  return `${neg ? "-" : ""}${whole}${frac ? "." + frac : ""}`;
}
