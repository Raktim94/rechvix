import { dec } from "./decimal";

/** The low-stock level used when an item has none of its own. */
export const DEFAULT_LOW_AT = "5";
export type StockState = "untracked" | "out" | "low" | "ok";

/** Where an item stands: not tracked (blank stock), out (zero or below), low (at or below its level), or fine. Exact decimals. */
export function stockState(stock: string | null | undefined, lowAt?: string | null): StockState {
  if (stock == null || stock.trim() === "") return "untracked";
  const have = dec(stock);
  if (have <= 0n) return "out";
  return have <= dec(lowAt && lowAt.trim() !== "" ? lowAt : DEFAULT_LOW_AT) ? "low" : "ok";
}

/** What would be left after selling `qty` of an item with `stock`, and where that leaves it. null when the item is not tracked. */
export function afterSale(stock: string | null | undefined, qty: string, lowAt?: string | null): { left: bigint; state: StockState } | null {
  if (stock == null || stock.trim() === "") return null;
  const left = dec(stock) - dec(qty);
  const level = dec(lowAt && lowAt.trim() !== "" ? lowAt : DEFAULT_LOW_AT);
  return { left, state: left <= 0n ? "out" : left <= level ? "low" : "ok" };
}
