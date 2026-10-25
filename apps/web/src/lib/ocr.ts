/** Reads a photo of a distributor bill with tesseract.js, entirely in this
 * browser tab: nothing about the scan leaves the device. The worker, WASM
 * core and English language data are served from this origin under /ocr
 * (copied from node_modules by scripts/copy-ocr-assets.mjs), because the
 * server's CSP only allows same-origin loads.
 *
 * Phone photos are large and low-contrast, which is what makes raw
 * tesseract output poor, so the image is downscaled and converted to a
 * contrast-stretched greyscale before recognition.
 */
import type { Worker } from "tesseract.js";

const OCR_BASE = "/ocr";
const MAX_SIDE = 2200;

let workerPromise: Promise<Worker> | null = null;
let progressHandler: ((fraction: number) => void) | undefined;

function getWorker(): Promise<Worker> {
  workerPromise ??= import("tesseract.js")
    .then(({ createWorker, OEM }) =>
      createWorker("eng", OEM.LSTM_ONLY, {
        workerPath: `${OCR_BASE}/worker.min.js`,
        corePath: OCR_BASE,
        langPath: OCR_BASE,
        workerBlobURL: false,
        logger: (m) => {
          if (m.status === "recognizing text" && typeof m.progress === "number") progressHandler?.(m.progress);
        },
      }),
    )
    .catch((err) => {
      console.error("OCR worker failed to start", err);
      workerPromise = null;
      throw err;
    });
  return workerPromise;
}

async function preprocess(image: File): Promise<Blob> {
  const bitmap = await createImageBitmap(image, { imageOrientation: "from-image" });
  const scale = Math.min(1, MAX_SIDE / Math.max(bitmap.width, bitmap.height));
  const width = Math.round(bitmap.width * scale);
  const height = Math.round(bitmap.height * scale);
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  if (!ctx) return image;
  ctx.drawImage(bitmap, 0, 0, width, height);
  bitmap.close();

  const data = ctx.getImageData(0, 0, width, height);
  const px = data.data;
  const hist = new Uint32Array(256);
  for (let i = 0; i < px.length; i += 4) {
    const g = Math.round(px[i]! * 0.299 + px[i + 1]! * 0.587 + px[i + 2]! * 0.114);
    px[i] = g;
    hist[g]!++;
  }
  // Stretch between the 1st and 99th percentile so faint ink on grey paper gets full contrast.
  const total = width * height;
  let lo = 0;
  let hi = 255;
  for (let acc = 0; lo < 255 && (acc += hist[lo]!) < total * 0.01; lo++);
  for (let acc = 0; hi > 0 && (acc += hist[hi]!) < total * 0.01; hi--);
  const range = Math.max(1, hi - lo);
  for (let i = 0; i < px.length; i += 4) {
    const v = Math.max(0, Math.min(255, ((px[i]! - lo) * 255) / range));
    px[i] = px[i + 1] = px[i + 2] = v;
  }
  ctx.putImageData(data, 0, 0);
  return new Promise((resolve) => canvas.toBlob((b) => resolve(b ?? image), "image/png"));
}

export async function runOcr(image: File, onProgress?: (fraction: number) => void): Promise<string> {
  let prepared: Blob = image;
  try {
    prepared = await preprocess(image);
  } catch {
    // Unsupported format for decoding here (e.g. HEIC) — let the engine try the original.
  }
  let worker: Worker;
  try {
    worker = await getWorker();
  } catch {
    throw new Error("The text reader could not start. Reload the page and try again.");
  }
  progressHandler = onProgress;
  try {
    const result = await worker.recognize(prepared);
    const text = result.data.text.trim();
    if (!text) throw new Error("No text found in this image. Try a sharper, well-lit photo of the bill.");
    return text;
  } finally {
    progressHandler = undefined;
  }
}
