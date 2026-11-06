import assert from "node:assert/strict";
import { test } from "node:test";
import { allocatePayment, ageBucket, customerBalance, dueOf, dueState, reminderText, type LedgerInvoice } from "../src/lib/ledger";
import { buildLedgers, owing } from "../src/lib/receivables";
import { calcInvoice as ci, snapshot as snap } from "../src/lib/tax";

const inv = (id: string, n: number, at: number, total: number, paid = 0, dueAt: number | null = null): LedgerInvoice => ({ id, number: `INV-${n}`, issuedAt: at, totalMinor: BigInt(total * 100), paidMinor: BigInt(paid * 100), dueAt });

test("a payment fills the oldest invoice first, then the next; leftover becomes credit", () => {
  const r = allocatePayment(1500_00n, [inv("b", 2, 200, 1000), inv("a", 1, 100, 800), inv("c", 3, 300, 400)]);
  assert.deepEqual(r.allocations.map((x) => [x.invoiceId, x.amountMinor]), [["a", 800_00n], ["b", 700_00n]]);
  assert.equal(r.creditMinor, 0n);
  const big = allocatePayment(5000_00n, [inv("a", 1, 100, 800)]);
  assert.equal(big.creditMinor, 4200_00n, "never push more onto an invoice than it owes");
});
test("partial payment leaves the remaining due; settled/cancelled invoices are skipped", () => {
  const r = allocatePayment(300_00n, [inv("a", 1, 100, 800, 800), { ...inv("x", 2, 150, 500), cancelled: true }, inv("c", 3, 200, 1000, 400)]);
  assert.deepEqual(r.allocations.map((x) => [x.invoiceId, x.amountMinor]), [["c", 300_00n]]);
  assert.equal(dueOf(inv("c", 3, 200, 1000, 700)), 300_00n);
});
test("chosen allocation is validated", () => {
  const invs = [inv("a", 1, 100, 800), inv("b", 2, 200, 500)];
  const ok = allocatePayment(600_00n, invs, [{ invoiceId: "b", amountMinor: 500_00n }, { invoiceId: "a", amountMinor: 100_00n }]);
  assert.equal(ok.creditMinor, 0n);
  assert.throws(() => allocatePayment(600_00n, invs, [{ invoiceId: "b", amountMinor: 600_00n }]), /only has/);
  assert.throws(() => allocatePayment(100_00n, invs, [{ invoiceId: "a", amountMinor: 200_00n }]), /more than the payment/);
  assert.throws(() => allocatePayment(100_00n, invs, [{ invoiceId: "zzz", amountMinor: 1_00n }]), /not among/);
  assert.throws(() => allocatePayment(0n, invs), /more than 0/);
});
test("customer balance: owed, credit, overdue", () => {
  const now = 1000 * 86_400_000;
  const invs = [inv("a", 1, 1, 800, 0, now - 10 * 86_400_000), inv("b", 2, 2, 500, 200, now + 5 * 86_400_000)];
  const b = customerBalance(invs, 0n, now);
  assert.equal(b.owedMinor, 1100_00n); assert.equal(b.overdueMinor, 800_00n); assert.equal(b.invoicesDue, 2); assert.equal(dueState(b), "overdue");
  const credit = customerBalance(invs, 2000_00n, now);
  assert.equal(credit.owedMinor, 0n); assert.equal(credit.creditMinor, 900_00n); assert.equal(dueState(credit), "settled");
  assert.equal(dueState(customerBalance([inv("c", 3, 1, 100, 0, now + 1)], 0n, now)), "due");
});
test("ageing buckets", () => {
  const now = 200 * 86_400_000; const d = (days: number) => now - days * 86_400_000;
  assert.deepEqual([null, d(-3), d(5), d(45), d(75), d(120)].map((x) => ageBucket(x as number | null, now)), ["current", "current", "1-30", "31-60", "61-90", "90+"]);
});
test("a reminder is worded as a reminder, not as proof of delivery", () => {
  const t = reminderText({ customer: "Aarav", business: "Sharma Hardware", owedText: "₹1,200.00", invoiceNumbers: ["INV-1", "INV-2", "INV-3", "INV-4"] });
  assert.match(t, /friendly reminder/); assert.match(t, /INV-1, INV-2, INV-3 and 1 more/);
});

test("buildLedgers: partial payment, credit and cancelled invoices roll up per customer", () => {
  const mk = (id: string, cust: string, price: string, t: number, cancelled = 0): any => {
    const lines = [{ variantId: "v", name: "x", unitId: "", qty: "1", price, discount: "0", gstRate: "0" }];
    return { local_id: id, state: "pending", payload: { customerId: cust, customerName: cust, lines, paymentMethod: null, pricingMode: "INCLUSIVE", calc: snap(ci(lines, "INCLUSIVE")) }, progress: {}, total: null, doc_number: null, local_number: id, created_at: t, due_date: null, cancelled };
  };
  const pay = (id: string, sale: string | null, party: string, amount: string): any => ({ id, party_id: party, sale_local_id: sale, server_doc_id: null, amount, state: "pending", reversed_at: null });
  const ls = buildLedgers([mk("a", "A", "100", 1), mk("b", "A", "50", 2), mk("c", "B", "70", 3, 1)], [pay("p1", "a", "A", "40"), pay("p2", null, "A", "10")], 10);
  const a = ls.find((l) => l.partyId === "A")!;
  assert.equal(a.balance.owedMinor, 10000n - 4000n + 5000n - 1000n);
  assert.equal(owing(ls).length, 1);
});
