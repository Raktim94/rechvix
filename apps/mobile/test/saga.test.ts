import assert from "node:assert/strict";
import { test } from "node:test";
import { NetworkError } from "../src/api/client";
import type { Org, Progress, SalePayload } from "../src/db/repo";
import { runSale } from "../src/sync/saga";
import { FakeServer } from "./fakeServer";

const org: Org = { organisationId: "o", legalEntityId: "le", legalName: "Demo", country: "IN", currency: "INR", gstStateCode: "20", branchId: "b", warehouseId: "w", priceListId: "p", walkInPartyId: "walk" };
const payload = (pay: string | null = "CASH"): SalePayload => ({
  customerId: "cust", customerName: "Gupta", paymentMethod: pay,
  lines: [
    { variantId: "v1", name: "Bolt", unitId: "u", qty: "2", price: "25", discount: "0" },
    { variantId: "v2", name: "Wire", unitId: "u", qty: "1", price: "1400", discount: "0" },
    { variantId: "v3", name: "Nut", unitId: "u", qty: "10", price: "3", discount: "0" },
  ],
});

/** Drives runSale like the engine does: keeps saved progress between attempts, retries after a dropped connection. */
async function runUntilDone(srv: FakeServer, pl: SalePayload) {
  let progress: Progress = {};
  const save = async (p: Progress) => { progress = { ...p }; };
  for (let attempt = 0; attempt < 10; attempt++) {
    try { return await runSale(srv, org, "L1", pl, progress, save); } catch (e) { if (!(e instanceof NetworkError)) throw e; }
  }
  throw new Error("never finished");
}

test("happy path: one invoice, three lines, finalised once, receipted in full at the server's total", async () => {
  const srv = new FakeServer();
  const out = await runUntilDone(srv, payload());
  assert.equal(srv.docs.size, 1);
  assert.equal(srv.docs.get("doc-1")!.lines.length, 3);
  assert.equal(srv.finalizeCount, 1);
  assert.equal(out.docNumber, "INV/000001");
  assert.equal(out.total, "1746.40"); // (50+1400+30) * 1.18 — tax added by the server
  assert.deepEqual(srv.receipts, [{ doc: "doc-1", amount: "1746.40", method: "CASH" }]);
});

test("pay later: no receipt is posted", async () => {
  const srv = new FakeServer();
  await runUntilDone(srv, payload(null));
  assert.equal(srv.receipts.length, 0);
  assert.equal(srv.finalizeCount, 1);
});

// Whatever instant the connection dies — even after the server already did the work — a retry must not duplicate anything.
for (const crash of ["after-line-1", "after-line-2", "after-line-3", "after-finalize", "after-receipt"]) {
  test(`connection lost ${crash}: retry completes with no duplicate lines, invoice or receipt`, async () => {
    const srv = new FakeServer();
    srv.crashAt = crash;
    await runUntilDone(srv, payload());
    assert.equal(srv.docs.size, 1, "one document");
    assert.equal(srv.docs.get("doc-1")!.lines.length, 3, "each line exactly once");
    assert.equal(srv.finalizeCount, 1, "ledger posted exactly once");
    assert.equal(srv.receipts.length, 1, "paid exactly once");
  });
}

test("connection lost right after the draft was created but before the answer arrived: the draft is tagged so it can be found", async () => {
  const srv = new FakeServer();
  srv.crashAt = "after-create";
  await runUntilDone(srv, payload());
  // The phone never learned the first draft's id, so a second draft is made; only the second is ever finalised.
  assert.equal(srv.finalizeCount, 1);
  assert.equal(srv.receipts.length, 1);
  const stray = [...srv.docs.values()].filter((d) => d.Status === "DRAFT");
  assert.equal(stray.length, 1);
  assert.equal(stray[0]!.ref, "mobile:L1", "stray draft carries the device reference");
  assert.equal(srv.receipts[0]!.doc, [...srv.docs.values()].find((d) => d.Status === "FINALIZED")!.ID);
});

test("resuming a sale whose invoice was finalised elsewhere never finalises twice", async () => {
  const srv = new FakeServer();
  const first = await runUntilDone(srv, payload(null));
  assert.equal(first.progress.finalized, true);
  // Same saved progress replayed from scratch (e.g. progress row lost): doc id kept, status says FINALIZED.
  const again = await runSale(srv, org, "L1", payload(null), { docId: "doc-1" }, async () => {});
  assert.equal(again.docNumber, "INV/000001");
  assert.equal(srv.finalizeCount, 1);
});
