import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { api, ApiError } from "../../lib/api-client";
import { useOrgContext } from "../../lib/useOrgContext";
import type { ResolvedScanLine } from "./PurchaseScanReviewModal";

/** The one "turn a resolved bill scan into a real purchase" operation —
 * shared by ScanBillButton (in-browser OCR) and ImportAiMarkdownButton
 * (an externally-run AI's Markdown reply), since both hand
 * PurchaseScanReviewModal's resolved result to the exact same
 * create-purchase-document-then-add-lines sequence. Creates a real
 * purchase document (that's what actually posts stock/accounting), not
 * a shortcut around it. */
export function useCommitScanToPurchase(onDone: () => void) {
  const org = useOrgContext();
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  return useMutation({
    mutationFn: async (result: { supplierId: string; lines: ResolvedScanLine[]; notes: string }) => {
      if (!org.branch || !org.warehouse) throw new Error("Missing organisation context.");
      const doc = await api.post<{ ID: string }>("/purchases/documents", {
        branch_id: org.branch.ID,
        warehouse_id: org.warehouse.ID,
        supplier_party_id: result.supplierId,
        document_type: "PURCHASE_INVOICE",
        currency_code: org.organisation?.DefaultCurrencyCode || "INR",
        notes: result.notes,
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
      onDone();
      // Same reasoning as ScanBillButton: PurchasesPage has no deep-
      // link-to-a-specific-document search param, so land on the list,
      // where the new draft is the newest row.
      void navigate({ to: "/purchases" });
    },
  });
}

export function commitScanErrorMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Could not create the purchase from this.";
}
