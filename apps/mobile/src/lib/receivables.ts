import type { Payment, Sale } from "../db/repo";
import { parseMinor } from "./decimal";
import { customerBalance, dueOf, type Balance, type LedgerInvoice } from "./ledger";
import { livePayments, saleTotalMinor } from "./sale";

export interface CustomerLedger { partyId: string; name: string; invoices: LedgerInvoice[]; creditMinor: bigint; balance: Balance; lastBilledAt: number }

/** One entry per customer: their open invoices, money received but not tied to an invoice, and the resulting balance. */
export function buildLedgers(sales: Sale[], payments: Payment[], now: number, credits: Map<string, bigint> = new Map()): CustomerLedger[] {
  const bySale = new Map<string, Payment[]>();
  const loose = new Map<string, Payment[]>();
  for (const p of payments) {
    if (p.sale_local_id) bySale.set(p.sale_local_id, [...(bySale.get(p.sale_local_id) ?? []), p]);
    else if (!p.server_doc_id) loose.set(p.party_id, [...(loose.get(p.party_id) ?? []), p]);
  }
  const parties = new Map<string, CustomerLedger>();
  for (const s of sales) {
    const id = s.payload.customerId;
    const gross = saleTotalMinor(s);
    const credit = credits.get(s.local_id) ?? 0n;
    const total = gross > credit ? gross - credit : 0n;   // what is still billable after credit notes
    const ps = bySale.get(s.local_id) ?? [];
    const paid = ps.length === 0 && s.payload.paymentMethod ? total : livePayments(ps).reduce((n, p) => n + parseMinor(p.amount), 0n);
    const inv: LedgerInvoice = { id: s.local_id, number: s.doc_number ?? s.local_number ?? s.local_id.slice(0, 8), issuedAt: s.created_at, totalMinor: total, paidMinor: paid, dueAt: s.due_date, cancelled: !!s.cancelled };
    const cur = parties.get(id) ?? { partyId: id, name: s.payload.customerName, invoices: [], creditMinor: 0n, balance: { owedMinor: 0n, creditMinor: 0n, overdueMinor: 0n, invoicesDue: 0 }, lastBilledAt: 0 };
    // Money received beyond the net bill is owed back to the customer; it shows as their credit, never as a payment.
    if (paid > total) cur.creditMinor += paid - total;
    cur.invoices.push(inv); cur.lastBilledAt = Math.max(cur.lastBilledAt, s.created_at);
    parties.set(id, cur);
  }
  for (const [pid, ps] of loose) {
    const credit = livePayments(ps).reduce((n, p) => n + parseMinor(p.amount), 0n);
    const cur = parties.get(pid);
    if (cur) cur.creditMinor += credit;
  }
  for (const l of parties.values()) l.balance = customerBalance(l.invoices, l.creditMinor, now);
  return [...parties.values()];
}

export const owing = (ls: CustomerLedger[]): CustomerLedger[] =>
  ls.filter((l) => l.balance.owedMinor > 0n).sort((a, b) => (b.balance.overdueMinor > 0n ? 1 : 0) - (a.balance.overdueMinor > 0n ? 1 : 0) || (b.balance.owedMinor > a.balance.owedMinor ? 1 : b.balance.owedMinor < a.balance.owedMinor ? -1 : 0));
export { dueOf };
