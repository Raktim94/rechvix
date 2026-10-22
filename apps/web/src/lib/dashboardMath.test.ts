import assert from "node:assert/strict";
import { test } from "node:test";
import { buildSeries, formatPercent, isoDay, percentChange, sum } from "./dashboardMath.ts";

test("buildSeries fills days without sales with zero, oldest first", () => {
  const end = new Date(2026, 9, 22);
  const series = buildSeries(new Map([["2026-10-21", 50], ["2026-10-22", 70]]), end, 3);
  assert.deepEqual(series, [
    { date: "2026-10-20", total: 0 },
    { date: "2026-10-21", total: 50 },
    { date: "2026-10-22", total: 70 },
  ]);
  assert.equal(sum(series), 120);
});

test("buildSeries crosses a month boundary", () => {
  const series = buildSeries(new Map(), new Date(2026, 10, 1), 3);
  assert.deepEqual(series.map((p) => p.date), ["2026-10-30", "2026-10-31", "2026-11-01"]);
});

test("percentChange is null without a previous period to compare with", () => {
  assert.equal(percentChange(100, 0), null);
  assert.equal(percentChange(Number.NaN, 5), null);
  assert.equal(percentChange(150, 100), 50);
  assert.equal(percentChange(50, 100), -50);
});

test("formatPercent keeps one decimal only for small figures", () => {
  assert.equal(formatPercent(12.4), "12%");
  assert.equal(formatPercent(-3.26), "3.3%");
  assert.equal(formatPercent(5), "5%");
});

test("isoDay is zero-padded local time", () => {
  assert.equal(isoDay(new Date(2026, 0, 5)), "2026-01-05");
});
