import type { ParsedBill, ParsedBillLine } from "./billParser";

/** Parses the structured Markdown format docs/ai-bill-import-prompt.md
 * asks an external AI chat tool (ChatGPT, Gemini, ...) to produce from a
 * photographed/scanned distributor bill — a free, no-API-key alternative
 * to this app's own in-browser OCR (billParser.ts): the user runs the AI
 * themselves on a site they already have access to, saves its reply as a
 * .md file, and uploads that here. Deliberately reuses ParsedBill/
 * ParsedBillLine (billParser.ts's own output shape) so the result feeds
 * straight into the exact same PurchaseScanReviewModal review/product-
 * matching/commit-to-purchase flow the OCR path already uses — no
 * separate review UI or backend endpoint needed for this second input
 * method.
 *
 * Expected shape (see the prompt file for the exact instructions given
 * to the AI):
 *
 *   Distributor Name: ...
 *   GSTIN: ...
 *   Phone: ...
 *
 *   ## Items
 *
 *   | Description | HSN/SAC | Quantity | Unit | Rate | Amount |
 *   |---|---|---|---|---|---|
 *   | ... | ... | ... | ... | ... | ... |
 *
 * Tolerant of the common ways an AI deviates from instructions anyway:
 * wrapping the whole reply in a ```markdown fence, extra blank lines,
 * a leading "Here's the extracted data:" sentence, minor header-name
 * variations (case/spacing) — none of that is a parse failure, it just
 * falls back to blank fields for whatever it can't confidently find,
 * same "never invent data, let a human fill the gap" principle as
 * billParser.ts.
 */

// The whitespace between ":" and the captured value is [ \t]*, not \s* —
// \s matches a newline too, and when a field is left blank ("GSTIN:"
// with nothing after it), a \s* there would swallow the line break and
// let (.+) match into the START OF THE NEXT LINE instead of failing to
// match at all, silently attributing e.g. a Phone value to GSTIN.
const FIELD_PATTERNS: Record<keyof Pick<ParsedBill, "distributorName" | "gstin" | "phone">, RegExp> = {
  distributorName: /^\**distributor\s*name\**\s*:[ \t]*(.+)$/im,
  gstin: /^\**gstin\**\s*:[ \t]*(.+)$/im,
  phone: /^\**phone\**\s*:[ \t]*(.+)$/im,
};

function stripCodeFence(text: string): string {
  const trimmed = text.trim();
  const fenced = trimmed.match(/^```[a-z]*\n([\s\S]*?)\n```$/i);
  return fenced?.[1] ?? trimmed;
}

// Strips markdown emphasis wrapping a whole value ("**Sharma Stores**",
// "_9876543210_") — an AI bolding the label often bolds the value too,
// even though only the label's own "**" is anchored by FIELD_PATTERNS.
function stripEmphasis(value: string): string {
  // Lazy `.+?` -- a greedy `.+` here over-consumes into the trailing
  // marker itself (backtracking stops as soon as ANY match is found,
  // which for "**Sharma Stores**" is satisfied by giving back only the
  // last "*", leaving one stray "*" inside the capture) and left a
  // trailing asterisk in the result; verified by hand against exactly
  // that input before switching to `.+?`.
  return value.replace(/^\*{1,2}(.+?)\*{1,2}$/, "$1").replace(/^_{1,2}(.+?)_{1,2}$/, "$1");
}

function extractField(text: string, pattern: RegExp): string {
  const match = text.match(pattern);
  return stripEmphasis(match?.[1]?.trim() ?? "");
}

// A markdown table row: leading/trailing "|" are optional, cells
// separated by "|" — split naively then trim each cell, which is
// exactly what every AI-generated GFM table looks like in practice.
function parseTableRow(line: string): string[] | null {
  const trimmed = line.trim();
  if (!trimmed.startsWith("|") && !trimmed.includes("|")) return null;
  const cells = trimmed
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => c.trim());
  return cells;
}

// A separator row is "|---|---|" (any number of dashes/colons per
// cell) — the GFM header-divider line, never real data.
function isSeparatorRow(cells: string[]): boolean {
  return cells.length > 0 && cells.every((c) => /^:?-{2,}:?$/.test(c) || c === "");
}

function normalizeNumber(raw: string): string {
  // Strip anything that isn't a digit/decimal point/minus (currency
  // symbols, thousands separators, stray whitespace) — the AI was told
  // to emit plain numbers, but "₹1,234.50" slipping through anyway is
  // exactly the kind of thing a human reviews on the next screen, not
  // something worth rejecting the whole row over.
  return raw.replace(/[^\d.-]/g, "");
}

export function parseAiMarkdownBill(rawText: string): ParsedBill {
  const text = stripCodeFence(rawText);

  const distributorName = extractField(text, FIELD_PATTERNS.distributorName);
  const gstin = extractField(text, FIELD_PATTERNS.gstin).toUpperCase();
  const phone = extractField(text, FIELD_PATTERNS.phone);

  const lines = text.split("\n");
  const tableRows = lines.map(parseTableRow).filter((r): r is string[] => r !== null && r.length >= 2);

  // The header row names each column; everything after it (skipping the
  // GFM "|---|---|" divider) is data, matched by column NAME rather than
  // a fixed position — an AI reordering "Rate"/"Amount" or adding an
  // extra column shouldn't silently misalign every field.
  const headerIdx = tableRows.findIndex((r) => r.some((c) => /description/i.test(c)));
  const parsedLines: ParsedBillLine[] = [];
  if (headerIdx !== -1) {
    const header = (tableRows[headerIdx] ?? []).map((c) => c.toLowerCase());
    const colIndex = (...names: string[]) => header.findIndex((h) => names.some((n) => h.includes(n)));
    const descCol = colIndex("description", "item", "product");
    const qtyCol = colIndex("quantity", "qty");
    const rateCol = colIndex("rate", "price", "unit price");
    const amountCol = colIndex("amount", "total");

    for (let i = headerIdx + 1; i < tableRows.length; i++) {
      const row = tableRows[i];
      if (!row || isSeparatorRow(row)) continue;
      const description = descCol !== -1 ? (row[descCol] ?? "") : "";
      if (!description || !/[a-zA-Z]/.test(description)) continue; // a truly empty/decorative row
      const quantity = normalizeNumber(qtyCol !== -1 ? (row[qtyCol] ?? "") : "");
      let unitPrice = normalizeNumber(rateCol !== -1 ? (row[rateCol] ?? "") : "");
      const amount = normalizeNumber(amountCol !== -1 ? (row[amountCol] ?? "") : "");
      // The prompt asks the AI to compute Rate = Amount / Quantity when
      // Rate is missing, but doing it again here means a human editing
      // the AI's raw table by hand (rather than re-running the AI)
      // doesn't need to also do that arithmetic themselves.
      if (!unitPrice && amount && quantity && Number(quantity) > 0) {
        unitPrice = String(Number(amount) / Number(quantity));
      }
      parsedLines.push({ description, quantity: quantity || "1", unitPrice: unitPrice || "0" });
    }
  }

  return { distributorName, phone, gstin, lines: parsedLines, rawText: text };
}
