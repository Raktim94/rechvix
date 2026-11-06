/** Customer balances and how a payment is shared across invoices. Pure functions over BigInt minor units (paise/cents). */
export interface LedgerInvoice { id: string; number: string; issuedAt: number; totalMinor: bigint; paidMinor: bigint; dueAt?: number | null; cancelled?: boolean }

export const dueOf = (i: LedgerInvoice): bigint => (i.cancelled ? 0n : i.totalMinor - i.paidMinor > 0n ? i.totalMinor - i.paidMinor : 0n);

export interface Allocation { invoiceId: string; amountMinor: bigint }
export interface AllocationResult { allocations: Allocation[]; creditMinor: bigint }

/** Shares a payment over invoices. Default: oldest invoice first. Pass `chosen` to pick exact amounts. An invoice is never paid beyond
 *  what it owes; whatever is left over is kept as customer credit (not silently dropped, not pushed onto an invoice that doesn't owe it). */
export function allocatePayment(amountMinor: bigint, invoices: LedgerInvoice[], chosen?: Allocation[]): AllocationResult {
  if (amountMinor <= 0n) throw new Error("The payment amount must be more than 0.");
  const byId = new Map(invoices.map((i) => [i.id, i]));
  const allocations: Allocation[] = [];
  let left = amountMinor;
  if (chosen) {
    for (const c of chosen) {
      const inv = byId.get(c.invoiceId);
      if (!inv) throw new Error("That invoice is not among this customer's invoices.");
      if (c.amountMinor <= 0n) continue;
      if (c.amountMinor > dueOf(inv)) throw new Error(`${inv.number} only has ${dueOf(inv)} (in minor units) due.`);
      if (c.amountMinor > left) throw new Error("The amounts given to invoices add up to more than the payment.");
      allocations.push({ invoiceId: c.invoiceId, amountMinor: c.amountMinor });
      left -= c.amountMinor;
    }
    return { allocations, creditMinor: left };
  }
  for (const inv of [...invoices].filter((i) => dueOf(i) > 0n).sort((a, b) => a.issuedAt - b.issuedAt || a.number.localeCompare(b.number))) {
    if (left === 0n) break;
    const take = left < dueOf(inv) ? left : dueOf(inv);
    allocations.push({ invoiceId: inv.id, amountMinor: take });
    left -= take;
  }
  return { allocations, creditMinor: left };
}

export interface Balance { owedMinor: bigint; creditMinor: bigint; overdueMinor: bigint; invoicesDue: number }

/** What the customer owes (or is owed back). `unallocatedCreditMinor` is money received but not tied to an invoice. */
export function customerBalance(invoices: LedgerInvoice[], unallocatedCreditMinor: bigint, now: number): Balance {
  let due = 0n, overdue = 0n, n = 0;
  for (const i of invoices) {
    const d = dueOf(i);
    if (d === 0n) continue;
    due += d; n++;
    if (i.dueAt != null && i.dueAt < now) overdue += d;
  }
  const net = due - unallocatedCreditMinor;
  return { owedMinor: net > 0n ? net : 0n, creditMinor: net < 0n ? -net : 0n, overdueMinor: overdue > net ? (net > 0n ? net : 0n) : overdue, invoicesDue: n };
}

export type DueState = "settled" | "due" | "overdue";
export function dueState(b: Balance): DueState {
  return b.owedMinor === 0n ? "settled" : b.overdueMinor > 0n ? "overdue" : "due";
}

/** Whole days past the due date (0 when not yet due). */
export const daysOverdue = (dueAt: number | null | undefined, now: number): number => (dueAt == null || dueAt >= now ? 0 : Math.floor((now - dueAt) / 86_400_000));

export type AgeBucket = "current" | "1-30" | "31-60" | "61-90" | "90+";
export function ageBucket(dueAt: number | null | undefined, now: number): AgeBucket {
  const d = daysOverdue(dueAt, now);
  return d === 0 ? "current" : d <= 30 ? "1-30" : d <= 60 ? "31-60" : d <= 90 ? "61-90" : "90+";
}

/** A polite, factual reminder. Opening it for sharing is all the phone can do: it cannot know whether it was delivered or read. */
export function reminderText(opts: { customer: string; business: string; owedText: string; invoiceNumbers: string[]; dueText?: string }): string {
  const refs = opts.invoiceNumbers.length ? ` for ${opts.invoiceNumbers.slice(0, 3).join(", ")}${opts.invoiceNumbers.length > 3 ? ` and ${opts.invoiceNumbers.length - 3} more` : ""}` : "";
  return `Hello ${opts.customer}, this is a friendly reminder from ${opts.business}: ${opts.owedText} is pending${refs}${opts.dueText ? ` (${opts.dueText})` : ""}. Please let us know once it is paid. Thank you!`;
}
