import assert from "node:assert/strict";
import { test } from "node:test";
import { DEFAULT_PROFILE, type SaleLine } from "../src/db/repo";
import { formatMinor } from "../src/lib/money";
import { TEMPLATES, invoiceHtml, paymentSummary, sampleInvoice, type InvoiceData, type InvoiceTemplate } from "../src/lib/invoice";
import { calcInvoice } from "../src/lib/tax";

const business = { ...DEFAULT_PROFILE, name: "Demo <Traders> & Co", gstin: "20ABCDE1234F1Z5", gstState: "20", address: "12 Main Road\nRanchi", phone: "9100000000" };
const lines: SaleLine[] = [
  { variantId: "a", name: "Steel Bolt M8", unitId: "", qty: "2", price: "105", discount: "0", gstRate: "5", hsn: "7318" },
  { variantId: "b", name: "Copper Wire", unitId: "", qty: "1", price: "1180", discount: "80", gstRate: "18", hsn: "7408" },
];
const mk = (mode: "INCLUSIVE" | "EXCLUSIVE", inter = false, paid = 0n): InvoiceData => {
  const calc = calcInvoice(lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.gstRate })), mode, inter);
  return { number: "INV-0007", date: Date.UTC(2026, 10, 6), business, customerName: "Gupta Works", customerGstin: "20AAAAA0000A1Z5", lines, calc, currency: "INR", payment: paymentSummary(calc.totalMinor, paid) };
};
const keys: InvoiceTemplate[] = ["classic", "modern", "thermal"];

test("there are at least three selectable designs", () => assert.deepEqual(TEMPLATES.map((t) => t.key), keys));

for (const t of keys) {
  test(`${t}: every design shows the same correct figures, tax split, payment status and escapes text`, () => {
    const d = mk("INCLUSIVE", false, 100000n);
    const h = invoiceHtml(d, t);
    assert.ok(h.includes(formatMinor(d.calc.totalMinor, "INR")), `total ${formatMinor(d.calc.totalMinor, "INR")} present`);
    assert.ok(h.includes(formatMinor(d.payment.paidMinor, "INR")) && h.includes(formatMinor(d.payment.dueMinor, "INR")), "paid and due present");
    assert.match(h, /CGST/); assert.match(h, /SGST/); assert.doesNotMatch(h, /IGST/);
    assert.match(h, /PART PAID/); assert.match(h, /Balance due/);
    assert.match(h, /Demo &lt;Traders&gt; &amp; Co/, "business name escaped");
    assert.match(h, /Steel Bolt M8/); assert.match(h, /Copper Wire/); assert.match(h, /GSTIN 20ABCDE1234F1Z5/);
    assert.doesNotMatch(h, /<Traders>/);
  });
  test(`${t}: inter-state shows IGST only; unknown customer state is stated, never guessed`, () => {
    const inter = invoiceHtml(mk("INCLUSIVE", true), t);
    assert.match(inter, /IGST/); assert.doesNotMatch(inter, /CGST|SGST/);
    const unknown = invoiceHtml({ ...mk("INCLUSIVE"), placeOfSupply: undefined }, t);
    assert.match(unknown, /not recorded/);
  });
  test(`${t}: paid, unpaid and cancelled states`, () => {
    const d = mk("INCLUSIVE");
    assert.match(invoiceHtml({ ...d, payment: paymentSummary(d.calc.totalMinor, d.calc.totalMinor) }, t), /PAID/);
    assert.match(invoiceHtml(d, t), /UNPAID/);
    assert.match(invoiceHtml({ ...d, cancelled: true, corrects: "INV-0003" }, t), /CANCELLED/i);
    assert.match(invoiceHtml({ ...d, corrects: "INV-0003" }, t), /INV-0003/);
  });
}
test("inclusive invoices say the rates include GST; exclusive ones don't claim it", () => {
  for (const t of ["classic", "modern", "thermal"] as const) {
    assert.match(invoiceHtml(mk("INCLUSIVE"), t), /include|incl\./i);
    assert.doesNotMatch(invoiceHtml(mk("EXCLUSIVE"), t), /include GST|incl\. GST/i);
  }
});
test("changing the design never changes a figure", () => {
  const d = mk("INCLUSIVE");
  const figures = [d.calc.totalMinor, d.calc.taxableMinor, ...d.calc.components.map((c) => c.minor), ...d.calc.lines.map((l) => l.grossMinor)].map((m) => formatMinor(m, "INR"));
  for (const t of keys) { const h = invoiceHtml(d, t); for (const f of figures) assert.ok(h.includes(f), `${t} shows ${f}`); }
});
test("the sample used for design previews is realistic and balanced", () => {
  const s = sampleInvoice(business, "INR");
  assert.equal(s.calc.taxableMinor + s.calc.taxMinor, s.calc.totalMinor);
  assert.equal(s.payment.paidMinor + s.payment.dueMinor, s.calc.totalMinor);
  for (const t of keys) assert.ok(invoiceHtml(s, t).length > 500);
});
