import assert from "node:assert/strict";
import test from "node:test";
import { csvCell, leadScore, leadsToCsv, stageProbability } from "./crmLogic.ts";

const stages = [
  { id: "n", name: "New", sort_order: 1, kind: "OPEN" },
  { id: "c", name: "Contacted", sort_order: 2, kind: "OPEN" },
  { id: "p", name: "Proposal", sort_order: 3, kind: "OPEN" },
  { id: "w", name: "Won", sort_order: 5, kind: "WON" },
];
const opp = (o: object) => ({ id: "1", title: "T", value: "0", currency: "INR", stage_id: "n", stage_kind: "OPEN", open_tasks: 0, ...o }) as never;

test("a bare lead is COLD and a complete advanced one is HOT", () => {
  assert.equal(leadScore(opp({}), stages)?.band, "COLD");
  const hot = leadScore(opp({ contact_phone: "9", contact_email: "a@b", value: "500", expected_close: "2999-01-01", open_tasks: 1, stage_id: "p" }), stages);
  assert.equal(hot?.score, 100);
  assert.equal(hot?.band, "HOT");
});

test("closed deals are not scored and a past close date earns nothing", () => {
  assert.equal(leadScore(opp({ stage_kind: "WON", stage_id: "w" }), stages), null);
  const past = leadScore(opp({ expected_close: "2000-01-01" }), stages);
  assert.equal(past?.factors.find((f) => f.label.startsWith("Close date"))?.points, 0);
});

test("forecast probability rises across open stages", () => {
  assert.equal(stageProbability(opp({ stage_id: "n" }), stages), 0.1);
  assert.ok(Math.abs(stageProbability(opp({ stage_id: "p" }), stages) - 0.8) < 1e-9);
  assert.equal(stageProbability(opp({ stage_kind: "WON" }), stages), 1);
});

test("CSV cells neutralise spreadsheet formulas and quote awkward text", () => {
  assert.equal(csvCell("=HYPERLINK(\"x\")"), "\"'=HYPERLINK(\"\"x\"\")\"");
  assert.equal(csvCell("a,b"), "\"a,b\"");
  assert.equal(csvCell(undefined), "");
  assert.match(leadsToCsv([opp({ title: "@bad" })], stages, new Map()), /'@bad/);
});
