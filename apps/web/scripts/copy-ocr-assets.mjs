// Copies the tesseract.js worker, WASM core and English language data from
// node_modules into public/ocr so the OCR runs entirely from this origin.
// The server's CSP is `default-src 'self'`, so the library's default CDN
// downloads are blocked; serving the files ourselves is what makes scanning work.
import { copyFileSync, existsSync, mkdirSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const out = join(root, "public", "ocr");
const core = join(root, "node_modules", "tesseract.js-core");
const lang = join(root, "node_modules", "@tesseract.js-data", "eng", "4.0.0_best_int", "eng.traineddata.gz");
const worker = join(root, "node_modules", "tesseract.js", "dist", "worker.min.js");

mkdirSync(out, { recursive: true });
copyFileSync(worker, join(out, "worker.min.js"));
copyFileSync(lang, join(out, "eng.traineddata.gz"));
// The worker importScripts the self-contained "<variant>.wasm.js" builds; only the
// LSTM ones are needed (the engine runs in LSTM mode).
for (const f of readdirSync(core)) {
  if (/^tesseract-core.*lstm\.wasm\.js$/.test(f)) copyFileSync(join(core, f), join(out, f));
}
if (!existsSync(join(out, "eng.traineddata.gz"))) throw new Error("OCR language data missing");
