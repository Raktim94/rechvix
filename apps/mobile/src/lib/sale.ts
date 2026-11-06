import type { Payment, Sale, SalePayload , Product } from "../db/repo";
import { SCALE, dec, decToString, parseMinor, roundRatio } from "./decimal";
import { calcInvoice, fromSnapshot, isInterState, type InvoiceCalc } from "./tax";

/** The figures an invoice was issued with. New sales carry a stored snapshot; sales queued before pricing modes existed are recomputed
 *  under their original meaning (EXCLUSIVE), so they never change. */
export function payloadCalc(p: SalePayload): InvoiceCalc {
  if (p.calc) return fromSnapshot(p.calc);
  const inter = isInterState(p.sellerState, p.customerState);
  return calcInvoice(p.lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), p.pricingMode ?? "EXCLUSIVE", inter);
}

/** What the customer owes for this invoice: the server's figure once it has booked it, otherwise the local one. */
export function saleTotalMinor(s: Pick<Sale, "total" | "payload">): bigint {
  return s.total ? parseMinor(s.total) : payloadCalc(s.payload).totalMinor;
}

/** True when the server booked a total that differs from what was issued on the phone (should never happen; shown if it does). */
export function totalMismatch(s: Pick<Sale, "total" | "payload">): boolean {
  return !!s.total && s.payload.calc !== undefined && parseMinor(s.total) !== parseMinor(s.payload.calc.total);
}

export const livePayments = (ps: Payment[]): Payment[] => ps.filter((p) => p.reversed_at == null && p.state !== "void");

/** Money received against a sale. Sales made before payments were recorded separately used `payload.paymentMethod` to mean
 *  "settled in full when it syncs"; they have no payment rows and keep that meaning. */
export function salePaidMinor(s: Pick<Sale, "total" | "payload">, payments: Payment[]): bigint {
  if (payments.length === 0 && s.payload.paymentMethod) return saleTotalMinor(s);
  return livePayments(payments).reduce((n, p) => n + parseMinor(p.amount), 0n);
}

export type PayStatus = "PAID" | "PARTIAL" | "UNPAID";
export function payStatus(totalMinor: bigint, paidMinor: bigint): PayStatus {
  const due = totalMinor - paidMinor;
  return due <= 0n ? "PAID" : paidMinor > 0n ? "PARTIAL" : "UNPAID";
}

/** The unit price to show when billing in `mode`. A price stored GST-inclusive (INCL) is shown as-is in inclusive mode; a pre-tax price
 *  (server items) gets its GST added so the seller sees the final price. The reverse applies in exclusive mode. 2 dp when adding GST,
 *  4 dp when removing it (the exact value is kept: nothing is rounded away that the customer would notice). */
export function unitPriceFor(p: Pick<Product, "price" | "price_basis" | "gst_rate">, mode: "INCLUSIVE" | "EXCLUSIVE"): string {
  const price = dec(p.price);
  const R = dec(p.gst_rate);
  const basis = p.price_basis ?? "EXCL";
  if (price === 0n) return "0";
  if (R === 0n || (mode === "INCLUSIVE") === (basis === "INCL")) return decToString(price, 4);
  const to = (v: bigint, dp: number) => { const u = SCALE / 10n ** BigInt(dp); return roundRatio(v, u) * u; };
  if (mode === "INCLUSIVE") return decToString(to(roundRatio(price * (100n * SCALE + R), 100n * SCALE), 2), 2);
  return decToString(to(roundRatio(price * 100n * SCALE, 100n * SCALE + R), 4), 4);
}
