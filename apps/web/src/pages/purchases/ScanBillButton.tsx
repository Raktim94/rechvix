import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useRef, useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { parseBillText, type ParsedBill } from "../../lib/billParser";
import { getOcrProvider, runOcr } from "../../lib/ocr";
import { useOrgContext } from "../../lib/useOrgContext";
import { PurchaseScanReviewModal, type ResolvedScanLine } from "./PurchaseScanReviewModal";

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
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const [scanning, setScanning] = useState(false);
  const [scanProgress, setScanProgress] = useState(0);
  const [scanError, setScanError] = useState<string | null>(null);
  const [parsedBill, setParsedBill] = useState<ParsedBill | null>(null);
  const [reviewOpen, setReviewOpen] = useState(false);

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

  const commitScan = useMutation({
    mutationFn: async (result: { supplierId: string; lines: ResolvedScanLine[] }) => {
      if (!org.branch || !org.warehouse) throw new Error("Missing organisation context.");
      const doc = await api.post<{ ID: string }>("/purchases/documents", {
        branch_id: org.branch.ID,
        warehouse_id: org.warehouse.ID,
        supplier_party_id: result.supplierId,
        document_type: "PURCHASE_INVOICE",
        currency_code: org.organisation?.DefaultCurrencyCode || "INR",
        notes: "Created from a scanned distributor bill.",
      });
      for (const line of result.lines) {
        await api.post(`/purchases/documents/${doc.ID}/lines`, {
          product_variant_id: line.productVariantId,
          unit_id: line.unitId,
          quantity: line.quantity,
          unit_price: line.unitPrice,
          batch_code: "",
        });
      }
      return doc;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      setReviewOpen(false);
      setParsedBill(null);
      // PurchasesPage has no deep-link-to-a-specific-document search
      // param (activeDocId is local state, picked by clicking a row) —
      // land on the list, where the new draft purchase this scan just
      // created is now the newest row.
      void navigate({ to: "/purchases" });
    },
    onError: (err) => {
      setScanError(err instanceof ApiError ? err.message : "Could not create the purchase from this scan.");
    },
  });

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
      {scanError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {scanError}
        </p>
      ) : null}
      <PurchaseScanReviewModal
        open={reviewOpen}
        onOpenChange={setReviewOpen}
        parsedBill={parsedBill}
        currencyCode={org.organisation?.DefaultCurrencyCode || "INR"}
        committing={commitScan.isPending}
        onCommitted={(result) => commitScan.mutate(result)}
      />
    </>
  );
}
