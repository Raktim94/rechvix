import type { Sale } from "../db/repo";
import { num } from "./money";
import { calcTotals } from "./tax";

export function saleAmount(sale: Sale): number {
  return sale.total != null ? num(sale.total) : calcTotals(sale.payload.lines).total;
}

export function paidAmount(sale: Sale): number {
  if (sale.payload.paymentMethod) return saleAmount(sale);
  return (sale.payload.payments ?? []).reduce((sum, payment) => sum + num(payment.amount), 0);
}

export function outstandingAmount(sale: Sale): number {
  return Math.max(0, Math.round((saleAmount(sale) - paidAmount(sale) + Number.EPSILON) * 100) / 100);
}
