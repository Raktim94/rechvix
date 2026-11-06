import type { Payment, Sale } from "../db/repo";
import { livePayments, payloadCalc, saleTotalMinor } from "./sale";
import { parseMinor } from "./decimal";

export type Range = "today" | "7d" | "30d";
export const rangeStart = (range: Range, now: number): number => {
  const d = new Date(now); d.setHours(0, 0, 0, 0);
  return range === "today" ? d.getTime() : d.getTime() - (range === "7d" ? 6 : 29) * 86_400_000;
};

export interface DayPoint { dayStart: number; salesMinor: bigint }
export interface TopItem { name: string; qty: string; amountMinor: bigint }
export interface DashboardStats {
  grossSalesMinor: bigint;      // billed: the totals of invoices made in the range (not the same as money received)
  invoiceCount: number;
  averageBillMinor: bigint;
  collectedMinor: bigint;       // money actually received in the range (reversed payments excluded)
  dueMinor: bigint;             // billed and still unpaid, across ALL invoices
  overdueMinor: bigint;         // the part of due whose due date has passed
  unsynced: number;             // invoices on this phone that the server doesn't have yet
  trend: DayPoint[];
  top: TopItem[];
}

/** Dashboard numbers from this phone's own records. Each number says what it counts: billed sales, collections and what is still owed
 *  are different things and are never mixed. When connected, the server's organisation-wide receivable (other devices' invoices) is
 *  shown separately by the caller; these figures are this phone's. */
export function dashboardStats(sales: Sale[], paymentsBySale: Map<string, Payment[]>, unallocated: Payment[], range: Range, now: number): DashboardStats {
  const start = rangeStart(range, now);
  const live = sales.filter((s) => !s.cancelled);
  const inRange = live.filter((s) => s.created_at >= start);
  const gross = inRange.reduce((n, s) => n + saleTotalMinor(s), 0n);
  let collected = 0n;
  for (const ps of [...paymentsBySale.values(), unallocated]) for (const p of livePayments(ps)) if (p.paid_at >= start) collected += parseMinor(p.amount);
  let due = 0n, overdue = 0n;
  for (const s of live) {
    const total = saleTotalMinor(s);
    const ps = paymentsBySale.get(s.local_id) ?? [];
    const paid = ps.length === 0 && s.payload.paymentMethod ? total : livePayments(ps).reduce((n, p) => n + parseMinor(p.amount), 0n);
    const d = total - paid;
    if (d > 0n) { due += d; if (s.due_date != null && s.due_date < now) overdue += d; }
  }
  const credit = livePayments(unallocated).reduce((n, p) => n + parseMinor(p.amount), 0n);
  if (credit > 0n) { due = due > credit ? due - credit : 0n; overdue = overdue > due ? due : overdue; }

  const days = range === "today" ? 1 : range === "7d" ? 7 : 30;
  const trend: DayPoint[] = Array.from({ length: days }, (_, i) => ({ dayStart: start + i * 86_400_000, salesMinor: 0n }));
  const items = new Map<string, { qty: bigint; amount: bigint; label: string }>();
  for (const s of inRange) {
    const idx = Math.min(days - 1, Math.floor((s.created_at - start) / 86_400_000));
    if (idx >= 0) trend[idx]!.salesMinor += saleTotalMinor(s);
    const c = payloadCalc(s.payload);
    s.payload.lines.forEach((l, i) => {
      const key = l.name.trim().toLowerCase();
      const cur = items.get(key) ?? { qty: 0n, amount: 0n, label: l.name };
      cur.amount += c.lines[i]?.grossMinor ?? 0n;
      cur.qty += BigInt(Math.round(Number(l.qty) * 1000)); // display only: thousandths
      items.set(key, cur);
    });
  }
  const top = [...items.values()].sort((a, b) => (b.amount > a.amount ? 1 : b.amount < a.amount ? -1 : 0)).slice(0, 5)
    .map((x) => ({ name: x.label, qty: String(Number(x.qty) / 1000), amountMinor: x.amount }));
  return {
    grossSalesMinor: gross, invoiceCount: inRange.length, averageBillMinor: inRange.length ? gross / BigInt(inRange.length) : 0n,
    collectedMinor: collected, dueMinor: due, overdueMinor: overdue,
    unsynced: live.filter((s) => s.state === "pending" || s.state === "syncing" || s.state === "failed").length, trend, top,
  };
}
