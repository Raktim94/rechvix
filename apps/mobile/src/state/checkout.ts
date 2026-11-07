import {
  getParty, getProfile, insertPayments, insertSale, kvGet, paymentsForSale, takeInvoiceNumber, updateSaleContent, DEFAULT_PROFILE,
  type SaleLine, type SalePayload,
} from "../db/repo";
import type { Db } from "../db/types";
import { dec, minorToString, parseMinor, toMinor } from "../lib/decimal";
import { calcInvoice, isInterState, snapshot, stateFromGstin } from "../lib/tax";
import type { Cart } from "./cart";

export interface CheckoutResult { localId: string; number: string; totalMinor: bigint; receivedMinor: bigint }
export class CheckoutError extends Error {}

/** The figures for the cart as it stands. Pure: the screen shows these, and saving stores exactly these. */
export function priceCart(cart: Cart, sellerState: string | null, customerState: string | null) {
  const inter = isInterState(sellerState, customerState);
  return calcInvoice(cart.lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), cart.mode, inter);
}

/** What the customer pays now. null text = the whole amount (when a method is chosen); "Pay later" (no method) = nothing. */
export function receivedFor(cart: Cart, totalMinor: bigint): bigint {
  if (!cart.payMethod) return 0n;
  if (cart.receivedText == null) return totalMinor;       // untouched = the whole amount
  if (cart.receivedText.trim() === "") return 0n;         // the seller cleared the box: nothing received yet, never "everything"
  const d = dec(cart.receivedText);
  const m = toMinor(d);
  return m > totalMinor ? totalMinor : m;
}

/** Saves the bill: the invoice, its stored figures and the money received, all in one go. Calling it twice with the same cart can only
 *  ever make one invoice, because the cart carries its own `saveId`. */
export async function checkout(db: Db, cart: Cart, ids: { uid: () => string; now: number }): Promise<CheckoutResult> {
  if (cart.lines.length === 0) throw new CheckoutError("Add at least one item.");
  if (cart.lines.some((l) => l.qtyError)) throw new CheckoutError("Fix the quantity first.");
  if (cart.lines.some((l) => dec(l.price) === 0n)) throw new CheckoutError("An item has no price.");
  if (!cart.customerId) throw new CheckoutError("Choose a customer (or Walk-in).");

  const profile = (await getProfile(db)) ?? DEFAULT_PROFILE;
  const party = await getParty(db, cart.customerId);
  const sellerState = profile.gstState || null;
  const customerState = stateFromGstin(party?.gstin) ?? null;
  const calc = priceCart(cart, sellerState, customerState);
  if (calc.lines.some((l) => l.invalid)) throw new CheckoutError("Check the amounts: a discount can't be more than the item's price.");

  // Replaying the same save (double tap, retry after a crash) finds the invoice already there.
  const dup = await db.first<{ local_id: string; local_number: string | null }>("SELECT local_id, local_number FROM sales WHERE local_id=?", [cart.saveId]);
  if (dup && !cart.editing) {
    const have = (await paymentsForSale(db, dup.local_id)).filter((p) => p.reversed_at == null && p.state !== "void").reduce((n, p) => n + parseMinor(p.amount), 0n);
    return { localId: dup.local_id, number: dup.local_number ?? "", totalMinor: calc.totalMinor, receivedMinor: have };
  }

  const lines: SaleLine[] = cart.lines.map((l) => ({
    variantId: l.variantId, name: l.name, unitId: l.unitId, qty: l.qty, price: l.price, discount: l.discount || "0",
    gstRate: l.gstRate, unitLabel: l.unitLabel, hsn: l.hsn, ...(l.custom ? { custom: true } : {}),
  }));
  const received = receivedFor(cart, calc.totalMinor);
  const dueDate = received < calc.totalMinor && cart.dueDays != null ? ids.now + cart.dueDays * 86_400_000 : null;
  const payload: SalePayload = {
    customerId: cart.customerId, customerName: cart.customerName, lines, paymentMethod: null, pricingMode: cart.mode,
    sellerState, customerState, calc: snapshot(calc), dueDate, note: cart.note || undefined, corrects: cart.corrects?.number,
  };

  if (cart.editing) {
    await updateSaleContent(db, cart.editing.localId, payload, "Edited before syncing", ids.now, calc.totalMinor);
    // What was already received on this invoice (not what the cart's payment fields say, which are hidden while editing).
    const have = (await paymentsForSale(db, cart.editing.localId)).filter((p) => p.reversed_at == null && p.state !== "void").reduce((n, p) => n + parseMinor(p.amount), 0n);
    return { localId: cart.editing.localId, number: cart.editing.number, totalMinor: calc.totalMinor, receivedMinor: have };
  }

  const number = await takeInvoiceNumber(db, profile.invoicePrefix);
  await insertSale(db, cart.saveId, payload, ids.now, number);
  if (received > 0n) {
    await insertPayments(db, {
      groupId: ids.uid(), partyId: cart.customerId, method: cart.payMethod!, reference: "", note: "Received at billing", paidAt: ids.now,
      parts: [{ id: ids.uid(), saleLocalId: cart.saveId, amount: minorToString(received) }],
    }, ids.now);
  }
  return { localId: cart.saveId, number, totalMinor: calc.totalMinor, receivedMinor: received };
}
export { parseMinor, kvGet };
