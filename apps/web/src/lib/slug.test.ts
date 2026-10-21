import assert from "node:assert/strict";
import { test } from "node:test";
import { slugCode } from "./slug.ts";

test("folds accented letters instead of dropping them", () => {
  assert.equal(slugCode("Almacén Principal", 16), "ALMACEN-PRINCIPA");
  assert.equal(slugCode("Sucursal Málaga", 16), "SUCURSAL-MALAGA");
  assert.equal(slugCode("Straße Müller", 24), "STRASSE-MULLER");
  assert.equal(slugCode("Łódź Centrum", 24), "LODZ-CENTRUM");
  assert.equal(slugCode("Café Crème", 24), "CAFE-CREME");
});

test("keeps the previous behaviour for plain ASCII", () => {
  assert.equal(slugCode("Main Warehouse", 16), "MAIN-WAREHOUSE");
  assert.equal(slugCode("  --Odd  name!! ", 16), "ODD-NAME");
  assert.equal(slugCode("A very long warehouse name indeed", 10), "A-VERY-LON");
});

test("returns an empty string when nothing is representable, so callers can fall back", () => {
  assert.equal(slugCode("गोदाम", 16), "");
  assert.equal(slugCode("", 16), "");
});
