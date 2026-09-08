import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import modal from "../../components/Modal.module.css";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { SalesDocument, SalesDocumentLine } from "./types";

type ReturnType = "SALES_RETURN" | "CREDIT_NOTE";

/** POST /sales/documents/{id}/convert existed with zero frontend
 * callers — there was no path at all for a customer returning a
 * defective item. Only lines the user sets a return quantity for (>0)
 * are actually included, at that quantity — see
 * sales.Service.ConvertDocument's own doc comment for why this needed a
 * backend change (no line-update/delete endpoint exists to fix up a
 * full copy after the fact). */
export function CreateReturnModal({
  open,
  onOpenChange,
  document,
  lines,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  document: SalesDocument;
  lines: SalesDocumentLine[];
}) {
  const navigate = useNavigate();
  const [returnType, setReturnType] = useState<ReturnType>("SALES_RETURN");
  const [quantities, setQuantities] = useState<Record<string, string>>({});

  const create = useMutation({
    mutationFn: async () => {
      const lineQuantities: Record<string, string> = {};
      for (const l of lines) {
        const q = quantities[l.ID];
        if (q && Number(q) > 0) lineQuantities[l.ID] = q;
      }
      const target = await api.post<SalesDocument>(`/sales/documents/${document.ID}/convert`, {
        target_type: returnType,
        line_quantities: lineQuantities,
      });
      await api.post(`/sales/documents/${target.ID}/finalize`);
      return target;
    },
    onSuccess: (target) => {
      onOpenChange(false);
      void navigate({ to: "/sales/$id", params: { id: target.ID } });
    },
  });

  if (!open) return null;

  const anyQuantitySet = lines.some((l) => Number(quantities[l.ID] ?? "0") > 0);

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={`${modal.dialog} ${modal.dialogWide}`} role="dialog" aria-modal="true" aria-label="Create return or credit note" onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>Return or credit note</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <div className={modal.body}>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <label style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
              <input type="radio" name="return-type" checked={returnType === "SALES_RETURN"} onChange={() => setReturnType("SALES_RETURN")} style={{ marginTop: 4 }} />
              <span>
                <strong>Sales return</strong>
                <div className={ui.muted}>The item is physically coming back — stock goes back up.</div>
              </span>
            </label>
            <label style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
              <input type="radio" name="return-type" checked={returnType === "CREDIT_NOTE"} onChange={() => setReturnType("CREDIT_NOTE")} style={{ marginTop: 4 }} />
              <span>
                <strong>Credit note</strong>
                <div className={ui.muted}>A financial adjustment only (e.g. price correction, damaged write-off) — no stock change.</div>
              </span>
            </label>
          </div>

          <div className={ui.tableScroll} style={{ marginTop: 16 }}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Item</th>
                  <th scope="col">Sold qty</th>
                  <th scope="col">Rate</th>
                  <th scope="col">Return qty</th>
                </tr>
              </thead>
              <tbody>
                {lines.map((l) => (
                  <tr key={l.ID}>
                    <td>{l.HSNSACCode || l.ID.slice(0, 8)}</td>
                    <td className="num">{l.Quantity}</td>
                    <td className="num">{formatMoney(l.UnitPrice)}</td>
                    <td>
                      <input
                        className={ui.input}
                        style={{ width: 90 }}
                        inputMode="decimal"
                        value={quantities[l.ID] ?? ""}
                        onChange={(e) => setQuantities((cur) => ({ ...cur, [l.ID]: e.target.value }))}
                        placeholder="0"
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {create.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {create.error instanceof ApiError ? create.error.message : "Could not create this return."}
            </p>
          ) : null}
        </div>
        <div className={modal.footer}>
          <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
            Cancel
          </button>
          <button type="button" className={ui.btnPrimary} disabled={!anyQuantitySet || create.isPending} onClick={() => create.mutate()}>
            {create.isPending ? "Creating…" : returnType === "SALES_RETURN" ? "Create sales return" : "Create credit note"}
          </button>
        </div>
      </div>
    </div>
  );
}
