import assert from "node:assert/strict";
import { test } from "node:test";
import { createItem, getItem, lowStockItems, searchProducts, setLowAt } from "../src/db/repo";
import { migrate } from "../src/db/schema";
import { afterSale, stockState } from "../src/lib/stock";
import { memoryDb } from "./nodedb";

test("stock state: not tracked, out, low at the default 5, low at an item's own level", () => {
  assert.equal(stockState(null), "untracked"); assert.equal(stockState(""), "untracked");
  assert.equal(stockState("0"), "out"); assert.equal(stockState("-2"), "out");
  assert.equal(stockState("5"), "low"); assert.equal(stockState("6"), "ok");
  assert.equal(stockState("20", "25"), "low"); assert.equal(stockState("26", "25"), "ok");
  assert.equal(stockState("0.5", "0.5"), "low", "fractions are exact");
  assert.equal(stockState("3", "0"), "ok", "a level of 0 means only warn when out");
});

test("after a sale: what is left and whether that is worth a warning", () => {
  assert.equal(afterSale(null, "2"), null);
  assert.equal(afterSale("10", "2")!.state, "ok");
  assert.equal(afterSale("10", "5")!.state, "low");
  assert.equal(afterSale("10", "10")!.state, "out");
  assert.equal(afterSale("30", "6", "25")!.state, "low");
});

test("a per-item level drives the low-stock list, survives the item being replaced, and is cleared by a blank value", async () => {
  const db = memoryDb(); await migrate(db);
  await createItem(db, "a", { name: "Bolt", sku: "BOLT", barcode: "", hsn: "", price: "10", gstRate: "", stock: "20", unitLabel: "pcs" }, 1);
  await createItem(db, "b", { name: "Nut", sku: "NUT", barcode: "", hsn: "", price: "5", gstRate: "", stock: "20", unitLabel: "pcs" }, 1);
  await createItem(db, "c", { name: "Gone", sku: "GONE", barcode: "", hsn: "", price: "5", gstRate: "", stock: "0", unitLabel: "pcs" }, 1);
  await createItem(db, "d", { name: "Service", sku: "SVC", barcode: "", hsn: "", price: "5", gstRate: "", stock: "", unitLabel: "pcs" }, 1);
  assert.deepEqual((await lowStockItems(db)).map((p) => p.sku), ["GONE"]);
  await setLowAt(db, "BOLT", "25");
  assert.deepEqual((await lowStockItems(db)).map((p) => p.sku), ["GONE", "BOLT"]);
  assert.equal((await getItem(db, "a"))!.low_at, "25");
  assert.equal((await searchProducts(db, "Bolt"))[0]!.low_at, "25");
  // The server's copy of the item replaces the phone's (new id, same SKU): the level stays.
  await db.run("UPDATE products SET variant_id='server-1' WHERE variant_id='a'");
  assert.equal((await getItem(db, "server-1"))!.low_at, "25");
  await setLowAt(db, "BOLT", "  ");
  assert.equal((await getItem(db, "server-1"))!.low_at ?? null, null);
  assert.deepEqual((await lowStockItems(db)).map((p) => p.sku), ["GONE"]);
});
