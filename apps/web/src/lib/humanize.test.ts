import assert from "node:assert/strict";
import { test } from "node:test";
import { humanize } from "./humanize.ts";

test("turns server enums into sentence case", () => {
  assert.equal(humanize("PURCHASE_INVOICE"), "Purchase invoice");
  assert.equal(humanize("FINALIZED"), "Finalized");
  assert.equal(humanize("AWAITING_PORTAL_COMPLETION"), "Awaiting portal completion");
});
test("tolerates empty and already-readable input", () => {
  assert.equal(humanize(""), "");
  assert.equal(humanize(undefined), "");
  assert.equal(humanize("Cash"), "Cash");
});
