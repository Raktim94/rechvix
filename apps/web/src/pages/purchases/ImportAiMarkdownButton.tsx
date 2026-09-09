import { useRef, useState } from "react";
import modal from "../../components/Modal.module.css";
import ui from "../../components/ui.module.css";
import { parseAiMarkdownBill } from "../../lib/aiMarkdownParser";
import type { ParsedBill } from "../../lib/billParser";
import { useOrgContext } from "../../lib/useOrgContext";
import { PurchaseScanReviewModal } from "./PurchaseScanReviewModal";
import { commitScanErrorMessage, useCommitScanToPurchase } from "./useCommitScanToPurchase";

// Kept in sync by hand with docs/ai-bill-import-prompt.md's "The prompt"
// section — that file is the fuller explanation (why this format, how
// to use it end to end); this is the same prompt text, just where a
// user actually is when they need it, instead of making them go find
// it in the repo.
const AI_PROMPT = `You are extracting data from a distributor/supplier bill or invoice
(image or PDF) for import into inventory/billing software. Read the
attached document carefully and output ONLY a Markdown document in
EXACTLY this format, with no extra commentary before or after it:

Distributor Name: <the seller/distributor's business name>
GSTIN: <their GSTIN if shown on the bill, else leave blank>
Phone: <their phone number if shown, else leave blank>
Invoice Number: <the bill/invoice number, if shown>
Invoice Date: <the bill date, in YYYY-MM-DD format if you can tell>

## Items

| Description | HSN/SAC | Quantity | Unit | Rate | Amount |
|---|---|---|---|---|---|
| <item name as printed> | <HSN/SAC code if shown, else blank> | <quantity, digits only> | <unit, e.g. PCS/KG/BOX/LTR> | <price per unit, digits only> | <line total, digits only> |

(one row per line item on the bill — include every item, even if a field is unclear)

Rules:
- Use plain numbers only for Quantity, Rate, and Amount — no currency symbols, no thousands separators, no text.
- If Rate is missing but Quantity and Amount are both present, compute Rate = Amount / Quantity yourself.
- Do not invent or guess any data that isn't actually on the bill — leave a field blank rather than making something up.
- Output nothing except the Markdown described above: no explanation, no "here is the extracted data" preamble, no code fence around the whole reply.`;

function useCopyToClipboard() {
  const [copied, setCopied] = useState(false);
  return {
    copied,
    copy: async (text: string) => {
      try {
        await navigator.clipboard.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      } catch {
        // Clipboard access can be denied by the browser — the prompt
        // text is still fully visible and selectable either way.
      }
    },
  };
}

/** The free, no-API-key alternative to ScanBillButton's in-browser OCR:
 * the user runs an AI chat tool they already have (ChatGPT, Gemini, ...)
 * themselves, on a site they already have access to, with the prompt
 * below plus their own photo/PDF of the bill — Rechvix never calls any
 * AI API or sends the bill anywhere. They save that AI's reply as a
 * .md/.txt file and upload it here; aiMarkdownParser.ts turns it into
 * the same ParsedBill shape billParser.ts's OCR path produces, so it
 * lands on the exact same review/product-matching/commit-to-purchase
 * screen. */
export function ImportAiMarkdownButton() {
  const org = useOrgContext();
  const [promptOpen, setPromptOpen] = useState(false);
  const [parsedBill, setParsedBill] = useState<ParsedBill | null>(null);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [fileError, setFileError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { copied, copy } = useCopyToClipboard();

  const commitScan = useCommitScanToPurchase(() => {
    setReviewOpen(false);
    setParsedBill(null);
  });

  function handleFile(file: File) {
    setFileError(null);
    file
      .text()
      .then((text) => {
        const parsed = parseAiMarkdownBill(text);
        if (!parsed.distributorName && parsed.lines.length === 0) {
          setFileError("Couldn't find a distributor name or any items in this file — make sure it's the AI's Markdown reply, following the prompt's format.");
          return;
        }
        setParsedBill(parsed);
        setPromptOpen(false);
        setReviewOpen(true);
      })
      .catch(() => setFileError("Could not read this file."));
  }

  return (
    <>
      <button type="button" className={ui.btnSecondary} onClick={() => setPromptOpen(true)}>
        Import from AI
      </button>

      {promptOpen ? (
        <div className={modal.overlay} onClick={() => setPromptOpen(false)}>
          <div className={`${modal.dialog} ${modal.dialogWide}`} role="dialog" aria-modal="true" aria-label="Import from an AI chat tool" onClick={(e) => e.stopPropagation()}>
            <div className={modal.header}>
              <h2>Import from an AI chat tool</h2>
              <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => setPromptOpen(false)}>
                ×
              </button>
            </div>
            <div className={modal.body}>
              <ol style={{ margin: "0 0 16px", paddingLeft: 20, display: "flex", flexDirection: "column", gap: 6 }}>
                <li>Open ChatGPT, Gemini, or a similar AI chat site and upload a photo or PDF of the distributor bill there.</li>
                <li>
                  Copy the prompt below and paste it into that same chat.{" "}
                  <button type="button" className={ui.btnSecondary} onClick={() => void copy(AI_PROMPT)}>
                    {copied ? "Copied!" : "Copy prompt"}
                  </button>
                </li>
                <li>Save the AI's reply as a .md or .txt file (select all, copy, paste into a plain text file, save).</li>
                <li>Upload that file below.</li>
              </ol>
              <pre
                style={{
                  whiteSpace: "pre-wrap",
                  fontFamily: "var(--font-mono)",
                  fontSize: "var(--text-xs)",
                  background: "var(--color-surface-alt)",
                  borderRadius: "var(--radius-sm)",
                  padding: "var(--space-3)",
                  maxHeight: 220,
                  overflowY: "auto",
                  marginBottom: 16,
                }}
              >
                {AI_PROMPT}
              </pre>
              <input
                ref={fileInputRef}
                type="file"
                accept=".md,.txt,text/markdown,text/plain"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  e.target.value = "";
                  if (file) handleFile(file);
                }}
              />
              {fileError ? (
                <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
                  {fileError}
                </p>
              ) : null}
            </div>
          </div>
        </div>
      ) : null}

      {commitScan.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {commitScanErrorMessage(commitScan.error)}
        </p>
      ) : null}

      <PurchaseScanReviewModal
        open={reviewOpen}
        onOpenChange={setReviewOpen}
        parsedBill={parsedBill}
        currencyCode={org.organisation?.DefaultCurrencyCode || "INR"}
        committing={commitScan.isPending}
        onCommitted={(result) => commitScan.mutate({ ...result, notes: "Created from an AI-parsed distributor bill." })}
      />
    </>
  );
}
