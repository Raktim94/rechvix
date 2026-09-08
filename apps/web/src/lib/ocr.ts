/** Two interchangeable OCR backends for the "scan a distributor bill"
 * feature (PurchasesPage) — the user picks which one in Settings.
 *
 * "offline": tesseract.js, a WASM OCR engine that actually runs the
 * recognition in this browser tab, not on a remote server — consistent
 * with this app's "no external connection required to run your
 * business" principle. Its worker/core/traineddata files are still
 * fetched from a CDN on first use (bundling ~15MB of language data into
 * the app itself was judged not worth it here) — a real, one-time
 * network dependency, just not a per-scan one, and not an OCR *service*
 * call: nothing about the recognition itself leaves this device.
 *
 * "puter": Puter.js (js.puter.com) — a free, keyless, serverless OCR
 * call (puter.ai.img2txt) that sends the image to Puter's own backend.
 * Meaningfully more accurate on messy handwritten bills, at the cost of
 * a real per-scan external network call. Off by default; the user opts
 * in from Settings.
 */
export type OcrProvider = "offline" | "puter";

const STORAGE_KEY = "rechvix.ocrProvider";

export function getOcrProvider(): OcrProvider {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "puter" || stored === "offline") return stored;
  } catch {
    // Falls through to the privacy-preserving default below.
  }
  return "offline";
}

export function setOcrProvider(provider: OcrProvider) {
  try {
    localStorage.setItem(STORAGE_KEY, provider);
  } catch {
    // Per-browser preference only — nothing breaks if this can't persist.
  }
}

let puterLoadPromise: Promise<void> | null = null;

function loadPuterScript(): Promise<void> {
  if (puterLoadPromise) return puterLoadPromise;
  puterLoadPromise = new Promise((resolve, reject) => {
    if ((window as unknown as { puter?: unknown }).puter) {
      resolve();
      return;
    }
    const script = document.createElement("script");
    script.src = "https://js.puter.com/v2/";
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error("Could not load Puter.js — check your internet connection."));
    document.head.appendChild(script);
  });
  return puterLoadPromise;
}

interface PuterGlobal {
  ai: { img2txt: (image: File | Blob | string) => Promise<string> };
}

async function runPuterOcr(image: File): Promise<string> {
  await loadPuterScript();
  const puter = (window as unknown as { puter?: PuterGlobal }).puter;
  if (!puter) throw new Error("Puter.js did not load correctly.");
  return puter.ai.img2txt(image);
}

async function runOfflineOcr(image: File, onProgress?: (fraction: number) => void): Promise<string> {
  const { recognize } = await import("tesseract.js");
  const result = await recognize(image, "eng", {
    logger: (m) => {
      if (m.status === "recognizing text" && typeof m.progress === "number") onProgress?.(m.progress);
    },
  });
  return result.data.text;
}

export async function runOcr(image: File, provider: OcrProvider, onProgress?: (fraction: number) => void): Promise<string> {
  return provider === "puter" ? runPuterOcr(image) : runOfflineOcr(image, onProgress);
}
