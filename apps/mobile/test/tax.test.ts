import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { minorToString, parseDec, parseMinor, toMinor } from "../src/lib/decimal";
import { calcInvoice, calcLine, fromSnapshot, isInterState, snapshot, stateFromGstin } from "../src/lib/tax";

const S = minorToString;

// ------------------------------------------------------------------ the examples from the product owner's request
test("inclusive ₹105 at 5%, qty 1 -> base 100, tax 5, total 105 (GST is NOT added again)", () => {
  const c = calcInvoice([{ qty: "1", price: "105", discount: "0", gstRate: "5" }], "INCLUSIVE");
  assert.equal(S(c.taxableMinor), "100.00"); assert.equal(S(c.taxMinor), "5.00"); assert.equal(S(c.totalMinor), "105.00");
  assert.deepEqual(c.components.map((x) => [x.label, S(x.minor)]), [["CGST", "2.50"], ["SGST", "2.50"]]);
});
test("inclusive ₹105 at 5%, qty 2 -> gross 210, not taxed again", () => {
  const c = calcInvoice([{ qty: "2", price: "105", discount: "0", gstRate: "5" }], "INCLUSIVE");
  assert.equal(S(c.totalMinor), "210.00"); assert.equal(S(c.taxableMinor), "200.00"); assert.equal(S(c.taxMinor), "10.00");
});
test("inclusive ₹118 at 18% -> base 100, tax 18, total 118", () => {
  const c = calcInvoice([{ qty: "1", price: "118", discount: "0", gstRate: "18" }], "INCLUSIVE");
  assert.equal(S(c.taxableMinor), "100.00"); assert.equal(S(c.taxMinor), "18.00"); assert.equal(S(c.totalMinor), "118.00");
});
test("zero-rated item is unchanged in both modes", () => {
  for (const mode of ["INCLUSIVE", "EXCLUSIVE"] as const) {
    const c = calcInvoice([{ qty: "3", price: "40", discount: "0", gstRate: "0" }, { qty: "1", price: "7", discount: "0", gstRate: null }], mode);
    assert.equal(S(c.totalMinor), "127.00"); assert.equal(S(c.taxMinor), "0.00"); assert.equal(c.components.length, 0);
  }
});
test("mixed-rate cart: rate groups and tax components add up exactly to the payable total", () => {
  const lines = [
    { qty: "1", price: "105", discount: "0", gstRate: "5" }, { qty: "3", price: "19.99", discount: "0.50", gstRate: "18" },
    { qty: "2", price: "56.5", discount: "0", gstRate: "12" }, { qty: "4", price: "10", discount: "0", gstRate: "0" }, { qty: "1", price: "1280.01", discount: "10", gstRate: "28" },
  ];
  for (const inter of [false, true]) {
    const c = calcInvoice(lines, "INCLUSIVE", inter);
    assert.equal(c.byRate.reduce((n, g) => n + g.grossMinor, 0n), c.totalMinor, "rate groups sum to the total");
    assert.equal(c.byRate.reduce((n, g) => n + g.taxMinor, 0n), c.taxMinor, "rate-group tax sums to the invoice tax");
    assert.equal(c.components.reduce((n, x) => n + x.minor, 0n), c.taxMinor, "components sum to the invoice tax");
    assert.equal(c.taxableMinor + c.taxMinor, c.totalMinor, "taxable + tax = payable");
    assert.equal(c.lines.reduce((n, l) => n + l.grossMinor, 0n), c.totalMinor);
  }
});
test("inclusive: the payable total is exactly the sum of the entered line amounts, whatever the rates", () => {
  const c = calcInvoice([{ qty: "2.5", price: "33.33", discount: "0", gstRate: "28" }, { qty: "0.75", price: "149", discount: "0", gstRate: "5" }], "INCLUSIVE");
  assert.equal(S(c.totalMinor), "195.08");
  assert.equal(S(c.lines[0]!.grossMinor), "83.33"); assert.equal(S(c.lines[1]!.grossMinor), "111.75");
});
test("discount comes off the customer-facing amount (inclusive: off the GST-inclusive amount)", () => {
  const c = calcLine({ qty: "3", price: "19.99", discount: "0.50", gstRate: "18" }, "INCLUSIVE", false);
  assert.equal(S(c.grossMinor), "59.47"); assert.equal(S(c.taxMinor), "9.08"); assert.equal(S(c.taxableMinor), "50.39");
});
test("exclusive mode adds GST on top (the pre-existing meaning, kept for old queued sales)", () => {
  const c = calcInvoice([{ qty: "1", price: "100", discount: "0", gstRate: "5" }], "EXCLUSIVE");
  assert.equal(S(c.taxableMinor), "100.00"); assert.equal(S(c.taxMinor), "5.00"); assert.equal(S(c.totalMinor), "105.00");
});
test("inter-state uses one IGST at the full rate; same-state splits CGST + SGST", () => {
  const intra = calcInvoice([{ qty: "1", price: "118", discount: "0", gstRate: "18" }], "INCLUSIVE", false);
  const inter = calcInvoice([{ qty: "1", price: "118", discount: "0", gstRate: "18" }], "INCLUSIVE", true);
  assert.deepEqual(intra.components.map((x) => x.label), ["CGST", "SGST"]); assert.deepEqual(inter.components.map((x) => x.label), ["IGST"]);
  assert.equal(S(inter.totalMinor), "118.00");
});
test("an unknown customer state is never guessed: treated as the seller's state", () => {
  assert.equal(isInterState("27", null), false); assert.equal(isInterState("27", undefined), false); assert.equal(isInterState(null, "29"), false);
  assert.equal(isInterState("27", "29"), true); assert.equal(isInterState("27", "27"), false);
  assert.equal(stateFromGstin("27ABCDE1234F1Z5"), "27"); assert.equal(stateFromGstin(""), null); assert.equal(stateFromGstin("27ABC"), null);
});
test("bad input is flagged and never produces negative money", () => {
  assert.equal(calcLine({ qty: "1", price: "10", discount: "50", gstRate: "5" }, "INCLUSIVE", false).invalid, true);
  assert.equal(calcLine({ qty: "1", price: "10", discount: "50", gstRate: "5" }, "INCLUSIVE", false).grossMinor, 0n);
  assert.equal(calcLine({ qty: "abc", price: "10", discount: "0", gstRate: "5" }, "INCLUSIVE", false).invalid, true);
});
test("decimal parsing is exact (no floating point)", () => {
  assert.equal(parseMinor("0.1") + parseMinor("0.2"), parseMinor("0.3"));
  assert.equal(S(toMinor(parseDec("1.005")!)), "1.01", "half-up, like the server");
  assert.equal(parseDec("1.12345678901"), null, "more than 10 decimals is refused");
  assert.equal(parseDec("-1"), null); assert.equal(parseDec("1e3"), null); assert.equal(parseDec(""), null);
});
test("a stored snapshot reproduces the same invoice", () => {
  const c = calcInvoice([{ qty: "2", price: "105", discount: "0", gstRate: "5" }, { qty: "1", price: "59.47", discount: "0", gstRate: "18" }], "INCLUSIVE", false);
  const r = fromSnapshot(JSON.parse(JSON.stringify(snapshot(c))));
  assert.equal(r.totalMinor, c.totalMinor); assert.equal(r.taxMinor, c.taxMinor); assert.deepEqual(r.components, c.components); assert.deepEqual(r.byRate, c.byRate);
});

// ------------------------------------------------------------------ parity with the REAL server
// test/fixtures/server-parity.json was captured by creating each scenario on a running Rechvix server (INCLUSIVE and EXCLUSIVE
// documents, same-state and inter-state) and reading back its own invoice preview. The phone must reproduce every figure.
interface Scenario {
  name: string; mode: "INCLUSIVE" | "EXCLUSIVE"; placeOfSupplyState: string | null; supplierState: string;
  lines: { qty: string; price: string; discount: string; rate: string }[];
  server: { lines: { taxable: string; tax: string; total: string }[]; taxes: { label: string; amount: string }[]; subtotal: string; grandTotal: string };
}
const scenarios = JSON.parse(readFileSync(new URL("./fixtures/server-parity.json", import.meta.url), "utf8")) as Scenario[];
for (const sc of scenarios) {
  test(`matches the server: ${sc.name}`, () => {
    const inter = sc.placeOfSupplyState !== null && sc.placeOfSupplyState !== sc.supplierState;
    const c = calcInvoice(sc.lines.map((l) => ({ qty: l.qty, price: l.price, discount: l.discount, gstRate: l.rate })), sc.mode, inter);
    assert.equal(S(c.totalMinor), sc.server.grandTotal, "grand total");
    assert.equal(S(c.taxableMinor), sc.server.subtotal, "taxable subtotal");
    sc.server.lines.forEach((sl, i) => {
      assert.equal(S(c.lines[i]!.taxableMinor), sl.taxable, `line ${i + 1} taxable`);
      assert.equal(S(c.lines[i]!.taxMinor), S(parseMinor(sl.tax)), `line ${i + 1} tax`);
      // The server's invoice preview prints a line's amount as the GST-inclusive figure for INCLUSIVE documents but the pre-tax
      // figure for EXCLUSIVE ones (tax is shown separately), so compare like with like. The invoice grand total (asserted above)
      // is the sum of the gross lines in both modes.
      assert.equal(S(sc.mode === "INCLUSIVE" ? c.lines[i]!.grossMinor : c.lines[i]!.taxableMinor), sl.total, `line ${i + 1} amount`);
    });
    assert.deepEqual(c.components.map((x) => ({ label: x.label, amount: S(x.minor) })), sc.server.taxes, "tax components");
  });
}
test("fixtures cover inclusive, exclusive, same-state and inter-state", () => {
  assert.ok(scenarios.length >= 12);
  assert.ok(scenarios.some((s) => s.mode === "INCLUSIVE") && scenarios.some((s) => s.mode === "EXCLUSIVE"));
  assert.ok(scenarios.some((s) => s.placeOfSupplyState && s.placeOfSupplyState !== s.supplierState) && scenarios.some((s) => !s.placeOfSupplyState));
});
