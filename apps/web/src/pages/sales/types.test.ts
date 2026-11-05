import assert from "node:assert/strict";
import { test } from "node:test";
import { paymentState, type SalesDocument } from "./types.ts";

const base = {
  DocumentType: "TAX_INVOICE",
  Status: "FINALIZED",
  GrandTotalAmount: { amount: "1000", currency: "INR" },
  PaidAmount: "0",
  DueDate: null,
} as unknown as SalesDocument;
const today = new Date("2026-11-05T10:00:00Z");

test("unpaid, part paid and paid", () => {
  assert.equal(paymentState(base, today), "UNPAID");
  assert.equal(paymentState({ ...base, PaidAmount: "400" }, today), "PARTIAL");
  assert.equal(paymentState({ ...base, PaidAmount: "1000" }, today), "PAID");
});

test("overdue only when a due date has passed and money is still owed", () => {
  assert.equal(paymentState({ ...base, DueDate: "2026-11-01T00:00:00Z" }, today), "OVERDUE");
  assert.equal(paymentState({ ...base, DueDate: "2026-11-30T00:00:00Z" }, today), "UNPAID");
  assert.equal(paymentState({ ...base, DueDate: "2026-11-01T00:00:00Z", PaidAmount: "1000" }, today), "PAID");
});

test("documents nobody owes money on have no payment state", () => {
  assert.equal(paymentState({ ...base, Status: "DRAFT" }, today), null);
  assert.equal(paymentState({ ...base, DocumentType: "QUOTATION" }, today), null);
  assert.equal(paymentState({ ...base, DocumentType: "CREDIT_NOTE" }, today), null);
});
