import assert from "node:assert/strict";
import { test } from "node:test";
import { Api, ApiError, NetworkError } from "../src/api/client";
import { insertSale, getSale, setOrg, countByState, type Org, type SalePayload } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import type { Db } from "../src/db/types";
import { SyncEngine } from "../src/sync/engine";
import { FakeServer } from "./fakeServer";
import { memoryDb } from "./nodedb";

const org: Org = { organisationId: "o", legalEntityId: "le", legalName: "Demo", country: "IN", currency: "INR", gstStateCode: "20", branchId: "b", warehouseId: "w", priceListId: "p", walkInPartyId: "walk" };
const sale = (n: string): SalePayload => ({ customerId: "c", customerName: "C", paymentMethod: "CASH", lines: [{ variantId: "v", name: n, unitId: "u", qty: "1", price: "100", discount: "0" }] });

/** Engine under test with pull stubbed out (pull has its own test against the real server). */
async function setup() {
  const db = await (async () => { const d = memoryDb(); await migrate(d); await setOrg(d, org); return d; })();
  const srv = new FakeServer();
  const eng = new SyncEngine(srv as unknown as Api, db, () => 1_000);
  (eng as unknown as { pull: () => Promise<boolean> }).pull = async () => true;
  return { db, srv, eng };
}

test("migrations are idempotent", async () => {
  const db: Db = memoryDb();
  await migrate(db); await migrate(db);
  assert.equal((await db.first<{ user_version: number }>("PRAGMA user_version"))!.user_version, 3);
});

test("offline: sales stay queued in order and nothing is lost", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "a", sale("A"), 1); await insertSale(db, "b", sale("B"), 2);
  const orig = srv.post.bind(srv);
  srv.post = async () => { throw new NetworkError(); };
  const r = await eng.syncNow();
  assert.equal(r.offline, true);
  assert.equal((await countByState(db)).pending, 2);
  srv.post = orig; // network back
  const r2 = await eng.syncNow();
  assert.equal(r2.pushed, 2);
  assert.equal((await getSale(db, "a"))!.doc_number, "INV/000001", "oldest sale gets the first number");
  assert.equal((await getSale(db, "b"))!.doc_number, "INV/000002");
  assert.equal(srv.finalizeCount, 2);
});

test("a refused sale is marked failed with the server's message and does not block the next one", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "bad", sale("X"), 1); await insertSale(db, "good", sale("Y"), 2);
  srv.reject = { path: /\/finalize$/, status: 409, code: "INSUFFICIENT_STOCK", message: "Not enough stock for X." };
  const origPost = srv.post.bind(srv);
  let n = 0;
  srv.post = async (p, b) => { if (/\/finalize$/.test(p) && n++ > 0) srv.reject = null; return origPost(p, b); };
  const r = await eng.syncNow();
  assert.equal(r.failed, 1); assert.equal(r.pushed, 1);
  const bad = (await getSale(db, "bad"))!;
  assert.equal(bad.state, "failed"); assert.match(bad.error!, /Not enough stock/);
  assert.equal((await getSale(db, "good"))!.state, "synced");
});

test("session expired (401): stops, keeps everything queued, asks for sign-in", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "a", sale("A"), 1);
  srv.reject = { path: /.*/, status: 401, code: "UNAUTHENTICATED", message: "Sign in" };
  const r = await eng.syncNow();
  assert.equal(r.authRequired, true);
  assert.equal((await getSale(db, "a"))!.state, "pending");
});

test("server errors retry, then give up after 5 attempts", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "a", sale("A"), 1);
  srv.reject = { path: /.*/, status: 500, code: "INTERNAL", message: "boom" };
  for (let i = 0; i < 4; i++) { await eng.syncNow(); assert.equal((await getSale(db, "a"))!.state, "pending"); }
  await eng.syncNow();
  assert.equal((await getSale(db, "a"))!.state, "failed");
});

test("a crash mid-sync (row stuck in 'syncing') resumes without duplicating", async () => {
  const { db, srv, eng } = await setup();
  await insertSale(db, "a", sale("A"), 1);
  srv.crashAt = "after-finalize";
  await eng.syncNow();                       // dies after the server finalised
  await db.run("UPDATE sales SET state='syncing' WHERE local_id='a'"); // simulate app killed
  await eng.syncNow();
  assert.equal(srv.finalizeCount, 1); assert.equal(srv.receipts.length, 1);
  assert.equal((await getSale(db, "a"))!.state, "synced");
});

test("concurrent syncNow calls share one run", async () => {
  const { db, eng, srv } = await setup();
  await insertSale(db, "a", sale("A"), 1);
  const [x, y] = await Promise.all([eng.syncNow(), eng.syncNow()]);
  assert.strictEqual(x, y);
  assert.equal(srv.finalizeCount, 1);
});

test("Api maps network failure, 401 and error envelopes", async () => {
  let unauthorized = 0, cookie = "";
  const mk = (f: typeof fetch) => new Api({ baseUrl: () => "http://x", cookie: () => cookie || null, onCookie: (c) => (cookie = c), onUnauthorized: () => unauthorized++, fetchImpl: f });
  await assert.rejects(mk((async () => { throw new TypeError("net"); }) as typeof fetch).get("/x"), NetworkError);
  const err = await mk((async () => new Response(JSON.stringify({ error: { code: "NOPE", message: "Nope." } }), { status: 401 })) as typeof fetch).get("/x").catch((e: unknown) => e);
  assert.ok(err instanceof ApiError); assert.equal((err as ApiError).code, "NOPE"); assert.equal(unauthorized, 1);
  await mk((async () => new Response("{}", { status: 200, headers: { "set-cookie": "bp_session=abc123; Path=/; HttpOnly; Secure" } })) as typeof fetch).post("/auth/login", {});
  assert.equal(cookie, "bp_session=abc123");
});
