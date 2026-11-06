import assert from "node:assert/strict";
import { test } from "node:test";
import { DEFAULT_PROFILE } from "../src/db/repo";
import { gstSplit, invoiceHtml, type InvoiceData } from "../src/lib/invoice";

const base: InvoiceData = {
  number: "INV-0001", date: 0, business: { ...DEFAULT_PROFILE, name: "Demo <Traders> & Co", gstState: "20", gstin: "20ABCDE1234F1Z5" },
  customerName: "Gupta", lines: [{ variantId: "v", name: "Bolt", unitId: "", qty: "2", price: "25", discount: "0", gstRate: "18" }], paymentMethod: "CASH", currency: "INR",
};

test("invoice: totals, CGST+SGST split, and HTML is escaped", () => {
  const h = invoiceHtml(base);
  assert.match(h, /CGST @ 9%/); assert.match(h, /SGST @ 9%/); assert.doesNotMatch(h, /IGST/);
  assert.match(h, /Demo &lt;Traders&gt; &amp; Co/, "business name escaped");
  assert.match(h, /₹59\.00/, "50 + 9 tax");
});
test("invoice: customer in another state gets IGST", () => {
  assert.equal(gstSplit(base.business, "27ABCDE1234F1Z5"), "IGST");
  assert.equal(gstSplit(base.business, "20ABCDE1234F1Z5"), "CGST_SGST");
  assert.equal(gstSplit(base.business, undefined), "CGST_SGST");
  assert.match(invoiceHtml({ ...base, customerGstin: "27ABCDE1234F1Z5" }), /IGST @ 18%/);
});
test("invoice: server total wins once synced", () => {
  assert.match(invoiceHtml({ ...base, serverTotal: "1234.5" }), /₹1,234\.50/);
});
