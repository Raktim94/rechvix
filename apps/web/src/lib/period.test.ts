import assert from "node:assert/strict";
import { test } from "node:test";
import { isValidRange, periodFor, withPeriod } from "./period.ts";

const now = new Date(2026, 10, 5); // 5 Nov 2026

test("this month starts on the 1st", () => {
  assert.deepEqual(periodFor("month", now), { from: "2026-11-01", to: "2026-11-05" });
});
test("last 30 days is inclusive of today", () => {
  assert.deepEqual(periodFor("30d", now), { from: "2026-10-07", to: "2026-11-05" });
});
test("last month crosses a year boundary", () => {
  assert.deepEqual(periodFor("lastMonth", new Date(2027, 0, 10)), { from: "2026-12-01", to: "2026-12-31" });
});
test("quarter starts at the calendar quarter", () => {
  assert.deepEqual(periodFor("quarter", now), { from: "2026-10-01", to: "2026-11-05" });
});
test("financial year is April-March for India and January-December elsewhere", () => {
  assert.equal(periodFor("fy", now, "IN").from, "2026-04-01");
  assert.equal(periodFor("fy", new Date(2027, 1, 3), "IN").from, "2026-04-01");
  assert.equal(periodFor("fy", now, "ES").from, "2026-01-01");
});
test("withPeriod replaces existing bounds and keeps other params", () => {
  const out = withPeriod("/reports/x?format=json&from=2020-01-01", { from: "2026-11-01", to: "2026-11-05" });
  const q = new URLSearchParams(out.split("?")[1]);
  assert.equal(q.get("from"), "2026-11-01");
  assert.equal(q.get("to"), "2026-11-05");
  assert.equal(q.get("format"), "json");
});
test("isValidRange rejects reversed and incomplete ranges", () => {
  assert.equal(isValidRange("2026-11-05", "2026-11-01"), false);
  assert.equal(isValidRange("", "2026-11-01"), false);
  assert.equal(isValidRange("2026-11-01", "2026-11-05"), true);
});
