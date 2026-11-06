import assert from "node:assert/strict";
import { test } from "node:test";
import { bumpQty, isValidQty, lineAmount, subtotal } from "../src/lib/money";
import { normalizeServer, probeServer } from "../src/lib/server";

test("normalizeServer: https by default, http only for private addresses, trailing slash dropped", () => {
  assert.equal(normalizeServer("shop.example.com"), "https://shop.example.com");
  assert.equal(normalizeServer(" shop.example.com/ "), "https://shop.example.com");
  assert.equal(normalizeServer("192.168.1.40:8080"), "http://192.168.1.40:8080");
  assert.equal(normalizeServer("10.0.2.2:8099"), "http://10.0.2.2:8099");
  assert.equal(normalizeServer("172.20.1.5"), "http://172.20.1.5");
  assert.equal(normalizeServer("172.40.1.5"), "https://172.40.1.5");
  assert.equal(normalizeServer("mybox.local"), "http://mybox.local");
  assert.equal(normalizeServer("http://x.test"), "http://x.test");
});

test("money: totals, quantities", () => {
  assert.equal(lineAmount({ qty: "3", price: "19.99", discount: "0" }), 59.97);
  assert.equal(subtotal([{ qty: "2", price: "25", discount: "0" }, { qty: "1", price: "1400", discount: "50" }]), 1400);
  assert.ok(isValidQty("1")); assert.ok(isValidQty("0.5")); assert.ok(!isValidQty("0")); assert.ok(!isValidQty("-1")); assert.ok(!isValidQty("1.23456")); assert.ok(!isValidQty(""));
  assert.equal(bumpQty("1", 1), "2"); assert.equal(bumpQty("1", -5), "0");
});

test("probeServer: ok, wrong status, unreachable", async () => {
  assert.equal(await probeServer("http://x", (async () => new Response("{}", { status: 200 })) as typeof fetch), null);
  assert.match((await probeServer("http://x", (async () => new Response("", { status: 404 })) as typeof fetch))!, /not like Rechvix/);
  assert.match((await probeServer("http://x", (async () => { throw new TypeError("n"); }) as typeof fetch))!, /Can't reach/);
});
