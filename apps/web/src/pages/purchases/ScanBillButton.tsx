import { useRef, useState } from "react";
import ui from "../../components/ui.module.css";
import { parseBillText, type ParsedBill } from "../../lib/billParser";
import { getOcrProvider, runOcr } from "../../lib/ocr";
import { useOrgContext } from "../../lib/useOrgContext";
import { PurchaseScanReviewModal } from "./PurchaseScanReviewModal";
import { commitScanErrorMessage, useCommitScanToPurchase } from "./useCommitScanToPurchase";

/** "Scan bill" used to only exist on the Purchases page — someone
 * managing Inventory/Catalogue and noticing a distributor bill in hand
 * wouldn't find it there. Self-contained (not shared with PurchasesPage's
 * own scan flow, which stays as-is) so this is a plain drop-in button:
 * scanning here still creates a real purchase document (that's what
 * actually moves stock in, per the same accounting/inventory rules as
 * any other purchase) — this is a more discoverable door into that same
 * correct flow, not a shortcut around it. */
export function ScanBillButton() {
  const org = useOrgContext();
  const inputRef = useRef<HTMLInputElement>(null);
  const [scanning, setScanning] = useState(false);
  const [scanProgress, setScanProgress] = useState(0);
  const [scanError, setScanError] = useState<string | null>(null);
  const [parsedBill, setParsedBill] = useState<ParsedBill | null>(null);
  const [reviewOpen, setReviewOpen] = useState(false);

  const commitScan = useCommitScanToPurchase(() => {
    setReviewOpen(false);
    setParsedBill(null);
  });

  async function handleFile(file: File) {
    setScanning(true);
    setScanProgress(0);
    setScanError(null);
    try {
      const text = await runOcr(file, getOcrProvider(), setScanProgress);
      setParsedBill(parseBillText(text));
      setReviewOpen(true);
    } catch (err) {
      setScanError(err instanceof Error ? err.message : "Could not read this image.");
    } finally {
      setScanning(false);
    }
  }

  return (
    <>
      <input
        ref={inputRef}
        type="file"
        accept="image/*"
        capture="environment"
        style={{ display: "none" }}
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) void handleFile(file);
        }}
      />
      <button type="button" className={ui.btnSecondary} disabled={scanning} onClick={() => inputRef.current?.click()}>
        {scanning ? `Scanning… ${Math.round(scanProgress * 100)}%` : "Scan distributor bill"}
      </button>
      {scanError || commitScan.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {scanError ?? commitScanErrorMessage(commitScan.error)}
        </p>
      ) : null}
      <PurchaseScanReviewModal
        open={reviewOpen}
        onOpenChange={setReviewOpen}
        parsedBill={parsedBill}
        currencyCode={org.organisation?.DefaultCurrencyCode || "INR"}
        committing={commitScan.isPending}
        onCommitted={(result) => commitScan.mutate({ ...result, notes: "Created from a scanned distributor bill." })}
      />
    </>
  );
}
