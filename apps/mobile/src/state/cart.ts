import { useSyncExternalStore } from "react";
import type { Db } from "../db/types";
import { kvGet, kvSet, type Product, type SaleLine, type SalePayload } from "../db/repo";
import { dec, decToString } from "../lib/decimal";
import { unitDecimals, validateQty, stepQty } from "../lib/qty";
import type { PricingMode } from "../lib/tax";

/** The bill being built. Lives outside any screen, so switching tabs or opening an editor never loses it, and it is saved to the
 *  database as it changes, so even a killed app resumes where the seller left off. */
export interface CartLine {
  key: string; variantId: string; name: string; unitId: string; unitLabel: string; hsn: string; custom: boolean;
  /** What the seller typed for the quantity (may be mid-edit, e.g. "2."). `qty` is only updated when the text is valid. */
  qtyText: string; qty: string; qtyError: string | null; qtyDecimals: number;
  price: string; discount: string; gstRate: string | null; stock: string | null;
}
export interface Cart {
  customerId: string | null; customerName: string;
  lines: CartLine[];
  mode: PricingMode;
  payMethod: string | null;       // null = pay later
  receivedText: string | null;    // null = the full amount; otherwise what was typed
  dueDays: number | null;         // for pay-later / part-paid bills
  note: string;
  /** Chosen once per bill: tapping "Save" twice can only ever save one invoice. */
  saveId: string;
  /** When set, saving replaces this unsynced invoice instead of creating a new one. */
  editing: { localId: string; number: string } | null;
  /** When set, this bill re-issues a cancelled invoice. */
  corrects: { number: string; owedBack: string } | null;
  /** When set, this bill was started from a quotation; saving it closes that quotation. */
  fromQuote?: { id: string; number: string } | null;
}

const uid = (): string => {
  const b = Array.from({ length: 16 }, () => Math.floor(Math.random() * 256));
  b[6] = (b[6]! & 0x0f) | 0x40; b[8] = (b[8]! & 0x3f) | 0x80;
  const h = b.map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
};

export const emptyCart = (mode: PricingMode): Cart => ({ customerId: null, customerName: "", lines: [], mode, payMethod: "CASH", receivedText: null, dueDays: null, note: "", saveId: uid(), editing: null, corrects: null });

let cart: Cart = emptyCart("INCLUSIVE");
const listeners = new Set<() => void>();
let persist: ((c: Cart) => void) | null = null;
let timer: ReturnType<typeof setTimeout> | null = null;

function set(next: Cart) {
  cart = next;
  listeners.forEach((l) => l());
  if (persist) { const p = persist; if (timer) clearTimeout(timer); timer = setTimeout(() => p(cart), 250); }
}
export const getCart = (): Cart => cart;
export const useCart = (): Cart => useSyncExternalStore((l) => { listeners.add(l); return () => { listeners.delete(l); }; }, getCart, getCart);

/** Start persisting the draft and restore a saved one (called once the database is open). */
export async function initCart(db: Db, defaultMode: PricingMode): Promise<void> {
  persist = (c) => { void kvSet(db, "cart_draft", JSON.stringify(c)); };
  const saved = await kvGet(db, "cart_draft");
  if (saved) { try { const c = JSON.parse(saved) as Cart; if (Array.isArray(c.lines)) { set({ ...emptyCart(defaultMode), ...c }); return; } } catch { /* ignore a corrupt draft */ } }
  set(emptyCart(defaultMode));
}

export const cartActions = {
  clear(mode: PricingMode) { set(emptyCart(mode)); },
  patch(p: Partial<Cart>) { set({ ...cart, ...p }); },
  setMode(mode: PricingMode) { set({ ...cart, mode }); },

  /** Tapping a product again adds one more (never a duplicate row). */
  addProduct(p: Product, mode: PricingMode, unitPrice: string) {
    const i = cart.lines.findIndex((l) => l.variantId === p.variant_id);
    if (i >= 0) { cartActions.step(cart.lines[i]!.key, 1); return; }
    const decimals = unitDecimals(p.unit_label, p.qty_decimals);
    const line: CartLine = {
      key: uid(), variantId: p.variant_id, name: p.name, unitId: p.unit_id, unitLabel: p.unit_label, hsn: p.hsn, custom: !!p.is_custom,
      qtyText: "1", qty: "1", qtyError: null, qtyDecimals: decimals, price: unitPrice, discount: "0", gstRate: p.gst_rate, stock: p.stock,
    };
    set({ ...cart, mode, lines: [...cart.lines, line] });
  },
  setQtyText(key: string, text: string) {
    set({ ...cart, lines: cart.lines.map((l) => {
      if (l.key !== key) return l;
      const v = validateQty(text, l.qtyDecimals, l.unitLabel);
      return v.ok ? { ...l, qtyText: text, qty: v.value, qtyError: null } : { ...l, qtyText: text, qtyError: v.message };
    }) });
  },
  step(key: string, delta: 1 | -1) {
    set({ ...cart, lines: cart.lines.map((l) => {
      if (l.key !== key) return l;
      const cur = validateQty(l.qtyText, l.qtyDecimals, l.unitLabel);
      const base = cur.ok ? cur.value : l.qty;
      const next = stepQty(base, delta, l.qtyDecimals);
      return { ...l, qty: next, qtyText: next, qtyError: null };
    }) });
  },
  remove(key: string) { set({ ...cart, lines: cart.lines.filter((l) => l.key !== key) }); },
  setPrice(key: string, price: string) { set({ ...cart, lines: cart.lines.map((l) => (l.key === key ? { ...l, price } : l)) }); },
  setDiscount(key: string, discount: string) { set({ ...cart, lines: cart.lines.map((l) => (l.key === key ? { ...l, discount } : l)) }); },

  /** Load an existing invoice back into the cart for editing / re-issuing. */
  load(payload: SalePayload, opts: { editing?: Cart["editing"]; corrects?: Cart["corrects"]; fromQuote?: Cart["fromQuote"]; stockOf?: (variantId: string) => string | null }) {
    const lines: CartLine[] = payload.lines.map((l: SaleLine) => {
      const decimals = unitDecimals(l.unitLabel);
      return { key: uid(), variantId: l.variantId, name: l.name, unitId: l.unitId, unitLabel: l.unitLabel ?? "pcs", hsn: l.hsn ?? "", custom: !!l.custom, qtyText: l.qty, qty: l.qty, qtyError: null, qtyDecimals: decimals, price: l.price, discount: l.discount, gstRate: l.gstRate ?? null, stock: opts.stockOf?.(l.variantId) ?? null };
    });
    set({ ...emptyCart(payload.pricingMode ?? "EXCLUSIVE"), customerId: payload.customerId, customerName: payload.customerName, lines, mode: payload.pricingMode ?? "EXCLUSIVE", payMethod: null, note: payload.note ?? "", editing: opts.editing ?? null, corrects: opts.corrects ?? null, fromQuote: opts.fromQuote ?? null });
  },
};

export const cartIsEmpty = (c: Cart): boolean => c.lines.length === 0;
export const cartHasErrors = (c: Cart): boolean => c.lines.some((l) => l.qtyError !== null || dec(l.price) === 0n);
export { decToString };
