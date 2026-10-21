// Fails when a locale file is missing keys, has extra keys, has an empty value,
// or changes the {{placeholders}} of the English source string.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const dir = new URL("../src/i18n/locales/", import.meta.url).pathname;
const flatten = (o, p = "") =>
  Object.entries(o).flatMap(([k, v]) => (typeof v === "object" && v !== null ? flatten(v, `${p}${k}.`) : [[`${p}${k}`, v]]));
const load = (f) => new Map(flatten(JSON.parse(readFileSync(join(dir, f), "utf8"))));
const slots = (s) => [...String(s).matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort().join(",");

const en = load("en.json");
let problems = 0;
const fail = (m) => { console.error("  ✗ " + m); problems++; };
const files = readdirSync(dir).filter((f) => f.endsWith(".json") && f !== "en.json");

for (const f of files) {
  const loc = load(f);
  for (const [k, v] of en) {
    if (!loc.has(k)) fail(`${f}: missing ${k}`);
    else if (String(loc.get(k)).trim() === "") fail(`${f}: empty ${k}`);
    else if (slots(loc.get(k)) !== slots(v)) fail(`${f}: ${k} placeholders {${slots(loc.get(k))}} differ from en {${slots(v)}}`);
  }
  for (const k of loc.keys()) {
    if (en.has(k)) continue;
    // Some languages need plural categories English lacks (fr/it/pt "many", etc.):
    // allowed when English defines the same base key's _other form.
    const m = k.match(/^(.*)_(zero|one|two|few|many|other)$/);
    if (m && en.has(`${m[1]}_other`)) continue;
    fail(`${f}: extra key ${k}`);
  }
}
console.log(`${files.length} locales checked against en.json (${en.size} keys): ${problems ? problems + " problem(s)" : "ok"}`);
process.exit(problems ? 1 : 0);
