/** Best-effort heuristic extraction from OCR'd distributor-bill text.
 * OCR text has no structure (no columns, no table borders) — this can
 * only ever be a starting point for the review screen, never a silent
 * auto-commit; every field it produces is edited by a human before
 * anything is saved (PurchaseReviewModal). Tuned against typical Indian
 * wholesale/distributor invoice layouts, not guaranteed against any
 * specific printer/format.
 */

export interface ParsedBillLine {
  description: string;
  quantity: string;
  unitPrice: string;
}

export interface ParsedBill {
  distributorName: string;
  phone: string;
  gstin: string;
  lines: ParsedBillLine[];
  rawText: string;
}

const GSTIN_RE = /\b\d{2}[A-Z]{5}\d{4}[A-Z]\d[A-Z\d]Z[A-Z\d]\b/;
const PHONE_RE = /(?:\+?91[\s-]?)?\b([6-9]\d{9})\b/;

// "<description> <qty> <rate> <amount>" — the common case, where OCR
// preserved four whitespace-separated groups and the last three are all
// numeric. Description may itself contain internal spaces/digits (a
// product code), so this only anchors on the LAST three tokens being
// numbers, not the first.
const LINE_QTY_RATE_AMOUNT_RE = /^(.+?)\s+(\d+(?:\.\d+)?)\s+(\d+(?:\.\d+)?)\s+(\d+(?:\.\d+)?)$/;
// "<description> <qty> <rate>" — amount omitted or merged into rate;
// caller computes amount = qty * rate.
const LINE_QTY_RATE_RE = /^(.+?)\s+(\d+(?:\.\d+)?)\s+(\d+(?:\.\d+)?)$/;

const NOISE_LINE_RE = /^(invoice|bill|tax invoice|gstin|gst no|phone|mobile|contact|date|subtotal|total|grand total|amount|qty|rate|description|hsn|sac|terms|thank you)\b/i;

export function parseBillText(rawText: string): ParsedBill {
  const lines = rawText
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.length > 0);

  const gstinMatch = rawText.match(GSTIN_RE);
  const phoneMatch = rawText.match(PHONE_RE);

  // The distributor's name is almost always one of the first few lines,
  // before any GSTIN/phone/"Invoice"/table-header line shows up — take
  // the first line that looks like a business name (has letters, isn't
  // a noise/header line, isn't itself the GSTIN or phone line).
  let distributorName = "";
  for (const line of lines.slice(0, 8)) {
    if (NOISE_LINE_RE.test(line)) continue;
    if (GSTIN_RE.test(line) || PHONE_RE.test(line)) continue;
    if (!/[a-zA-Z]{3,}/.test(line)) continue;
    distributorName = line;
    break;
  }

  const parsedLines: ParsedBillLine[] = [];
  for (const line of lines) {
    if (NOISE_LINE_RE.test(line)) continue;
    if (GSTIN_RE.test(line)) continue;

    const full = line.match(LINE_QTY_RATE_AMOUNT_RE);
    if (full && full[1] && full[2] && full[3]) {
      const description = full[1];
      if (/[a-zA-Z]{2,}/.test(description)) {
        parsedLines.push({ description: description.trim(), quantity: full[2], unitPrice: full[3] });
        continue;
      }
    }
    const partial = line.match(LINE_QTY_RATE_RE);
    if (partial && partial[1] && partial[2] && partial[3]) {
      const description = partial[1];
      if (/[a-zA-Z]{2,}/.test(description)) {
        parsedLines.push({ description: description.trim(), quantity: partial[2], unitPrice: partial[3] });
      }
    }
  }

  return {
    distributorName,
    phone: phoneMatch?.[1] ?? "",
    gstin: gstinMatch?.[0] ?? "",
    lines: parsedLines,
    rawText,
  };
}
