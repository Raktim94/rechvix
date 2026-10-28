import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import modal from "./Modal.module.css";
import ui from "./ui.module.css";
import { api, ApiError } from "../lib/api-client";
import { slugCode } from "../lib/slug";
import { useOrgContext } from "../lib/useOrgContext";

interface Unit {
  ID: string;
  Code: string;
  Name: string;
}

export interface NewItem {
  productId: string;
  variantId: string;
  name: string;
  hsn: string;
  unitId: string;
  unitCode: string;
  salePrice: string;
  mrp: string;
  taxRate: string;
}

const GST_SLABS = ["0", "0.25", "3", "5", "12", "18", "28"];

/** The one-screen "Add New Item" form shared by the purchase and sale
 * grids: just what a counter needs (name, HSN, sale rate, MRP, tax rate,
 * unit). Enter walks to the next field and saves from the last, so a run
 * of new items can be typed without touching the mouse; "Save & add
 * another" keeps the dialog open for the next one. */
export function QuickAddItemModal({
  open,
  onOpenChange,
  defaultName,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  defaultName?: string;
  onCreated: (item: NewItem) => void;
}) {
  const queryClient = useQueryClient();
  const org = useOrgContext();
  const [name, setName] = useState("");
  const [hsn, setHsn] = useState("");
  const [salePrice, setSalePrice] = useState("");
  const [mrp, setMrp] = useState("");
  const [taxRate, setTaxRate] = useState("18");
  const [unitId, setUnitId] = useState("");
  const [another, setAnother] = useState(false);
  const nameRef = useRef<HTMLInputElement>(null);
  const fieldRefs = useRef<(HTMLInputElement | HTMLSelectElement | null)[]>([]);

  const units = useQuery({
    queryKey: ["units"],
    queryFn: () => api.getListField<Unit>("/catalogue/units", "units_of_measure"),
    enabled: open,
  });

  useEffect(() => {
    if (!open) return;
    setName(defaultName ?? "");
    setHsn("");
    setSalePrice("");
    setMrp("");
    setTaxRate("18");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // A fresh install has no units; the same idempotent call the catalogue
  // page offers, done silently so the form is usable immediately.
  useEffect(() => {
    if (!open || !units.data) return;
    if (units.data.length === 0) {
      void api.post("/catalogue/units/ensure-default", {}).then(() => queryClient.invalidateQueries({ queryKey: ["units"] }));
      return;
    }
    if (!unitId || !units.data.some((u) => u.ID === unitId)) {
      const pick = units.data.find((u) => u.Code === "PCS") ?? units.data[0];
      if (pick) setUnitId(pick.ID);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, units.data]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onOpenChange(false);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onOpenChange]);

  const create = useMutation({
    mutationFn: async (): Promise<NewItem> => {
      const currency = org.organisation?.DefaultCurrencyCode || "INR";
      const product = await api.post<{ ID: string; Name: string }>("/catalogue/products", {
        category_id: null,
        brand_id: null,
        base_uom_id: unitId,
        name: name.trim(),
        description: "",
        hsn_sac_code: hsn.trim(),
      });
      const variant = await api.post<{ ID: string }>("/catalogue/variants", {
        product_id: product.ID,
        sku_code: slugCode(product.Name, 24) || "SKU-" + product.ID.slice(0, 8).toUpperCase(),
        attributes: mrp.trim() ? { mrp: mrp.trim() } : {},
      });
      if (Number(salePrice) > 0) {
        const list = await api.post<{ ID: string }>("/pricing/price-lists/ensure-default", { currency_code: currency });
        await api.post(`/pricing/price-lists/${list.ID}/items`, {
          product_variant_id: variant.ID,
          unit_id: unitId,
          amount: salePrice,
          currency_code: currency,
        });
      }
      // The tax rate belongs to the HSN code, so it is recorded once per
      // code. A code that already has a rate keeps it; a failure here (no
      // permission, odd code) must not lose the item that was just saved.
      if (hsn.trim() && taxRate !== "") {
        try {
          const existing = await api.getListField<unknown>(`/gst/tax-rates/${encodeURIComponent(hsn.trim())}`, "tax_rates");
          if (existing.length === 0) {
            await api.post("/gst/tax-rates", { hsn_sac_code: hsn.trim(), gst_rate: taxRate, cess_rate: "0", valid_from: "2017-07-01" });
          }
        } catch {
          /* item is saved; the rate can be set from GST settings */
        }
      }
      const unit = units.data?.find((u) => u.ID === unitId);
      return {
        productId: product.ID,
        variantId: variant.ID,
        name: product.Name,
        hsn: hsn.trim(),
        unitId,
        unitCode: unit?.Code ?? "",
        salePrice,
        mrp,
        taxRate,
      };
    },
    onSuccess: (item) => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      void queryClient.invalidateQueries({ queryKey: ["price-items"] });
      onCreated(item);
      if (another) {
        setName("");
        setHsn("");
        setSalePrice("");
        setMrp("");
        setAnother(false);
        nameRef.current?.focus();
      } else {
        onOpenChange(false);
      }
    },
  });

  if (!open) return null;

  const canSave = name.trim() !== "" && unitId !== "" && !create.isPending;

  // Enter walks forward; on the last field it saves. Selects keep their
  // own Enter behaviour (open/choose), so only inputs are wired.
  function onEnter(index: number) {
    return (e: React.KeyboardEvent) => {
      if (e.key !== "Enter") return;
      e.preventDefault();
      const next = fieldRefs.current[index + 1];
      if (next) next.focus();
      else if (canSave) create.mutate();
    };
  }
  const ref = (i: number) => (el: HTMLInputElement | HTMLSelectElement | null) => {
    fieldRefs.current[i] = el;
    if (i === 0) nameRef.current = el as HTMLInputElement | null;
  };

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label="Add new item" onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>Add new item</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (canSave) create.mutate();
          }}
        >
          <div className={modal.body}>
            <div className={ui.field}>
              <label htmlFor="qai-name">Item name*</label>
              <input id="qai-name" ref={ref(0)} className={ui.input} autoFocus required value={name} onChange={(e) => setName(e.target.value)} onKeyDown={onEnter(0)} autoComplete="off" />
            </div>
            <div className={ui.formGrid}>
              <div className={ui.field}>
                <label htmlFor="qai-rate">Sale rate</label>
                <input id="qai-rate" ref={ref(1)} className={ui.input} inputMode="decimal" value={salePrice} onChange={(e) => setSalePrice(e.target.value.replace(/[^\d.]/g, ""))} onKeyDown={onEnter(1)} />
              </div>
              <div className={ui.field}>
                <label htmlFor="qai-mrp">MRP</label>
                <input id="qai-mrp" ref={ref(2)} className={ui.input} inputMode="decimal" value={mrp} onChange={(e) => setMrp(e.target.value.replace(/[^\d.]/g, ""))} onKeyDown={onEnter(2)} />
              </div>
            </div>
            <div className={ui.formGrid}>
              <div className={ui.field}>
                <label htmlFor="qai-hsn">HSN code</label>
                <input id="qai-hsn" ref={ref(3)} className={ui.input} inputMode="numeric" value={hsn} onChange={(e) => setHsn(e.target.value.replace(/\D/g, "").slice(0, 8))} onKeyDown={onEnter(3)} />
              </div>
              <div className={ui.field}>
                <label htmlFor="qai-tax">Tax rate (GST %)</label>
                <input id="qai-tax" ref={ref(4)} className={ui.input} inputMode="decimal" list="qai-slabs" value={taxRate} onChange={(e) => setTaxRate(e.target.value.replace(/[^\d.]/g, ""))} onKeyDown={onEnter(4)} />
                <datalist id="qai-slabs">
                  {GST_SLABS.map((s) => (
                    <option key={s} value={s} />
                  ))}
                </datalist>
              </div>
            </div>
            <div className={ui.field}>
              <label htmlFor="qai-unit">Unit</label>
              <select id="qai-unit" ref={ref(5)} className={ui.select} value={unitId} onChange={(e) => setUnitId(e.target.value)}>
                {units.data?.map((u) => (
                  <option key={u.ID} value={u.ID}>
                    {u.Name} ({u.Code})
                  </option>
                ))}
              </select>
            </div>
            {create.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", margin: 0 }}>
                {create.error instanceof ApiError ? create.error.message : "Could not save this item."}
              </p>
            ) : null}
          </div>
          <div className={modal.footer}>
            <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
              Cancel
            </button>
            <button
              type="button"
              className={ui.btnSecondary}
              disabled={!canSave}
              onClick={() => {
                setAnother(true);
                create.mutate();
              }}
            >
              Save &amp; add another
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={!canSave}>
              {create.isPending ? "Saving…" : "Save item"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
