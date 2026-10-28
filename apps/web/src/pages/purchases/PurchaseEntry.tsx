import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { QuickAddItemModal, type NewItem } from "../../components/QuickAddItemModal";
import { QuickAddPartyModal } from "../../components/QuickAddPartyModal";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";
import styles from "./PurchaseEntry.module.css";

interface LookupResult {
  ProductID: string;
  ProductName: string;
  HSNSACCode: string;
  ProductVariantID: string;
  SKUCode: string;
  QuantityOnHand: string;
  UnitPrice: { amount: string; currency: string } | null;
}

interface Row {
  key: number;
  query: string;
  variantId: string;
  productId: string;
  name: string;
  unitId: string;
  unitCode: string;
  qty: string;
  rate: string;
  discount: string;
  taxRate: string;
}

const COLS = ["item", "qty", "rate", "discount"] as const;
let rowKey = 1;
const blankRow = (): Row => ({ key: rowKey++, query: "", variantId: "", productId: "", name: "", unitId: "", unitCode: "", qty: "1", rate: "", discount: "", taxRate: "" });

const num = (s: string) => {
  const n = Number(s);
  return Number.isFinite(n) ? n : 0;
};
const money = (n: number) => n.toLocaleString("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
/** Net unit price after the line discount, rounded to the currency's two places. */
const netRate = (r: Row) => Math.round(num(r.rate) * (1 - Math.min(num(r.discount), 100) / 100) * 100) / 100;
const lineAmount = (r: Row) => Math.round(num(r.qty) * netRate(r) * 100) / 100;

/** Vyapar-style multi-row purchase entry: pick the supplier, type items
 * down a grid (Enter walks item → qty → rate → discount → next row), add a
 * brand-new item or supplier without leaving, then Save. */
export function PurchaseEntry({ onBack, onSaved }: { onBack: () => void; onSaved: (documentId: string) => void }) {
  const org = useOrgContext();
  const currency = org.organisation?.DefaultCurrencyCode || "INR";
  const today = new Date().toISOString().slice(0, 10);

  const [supplier, setSupplier] = useState<Party | null>(null);
  const [supplierQuery, setSupplierQuery] = useState("");
  const [supplierOpen, setSupplierOpen] = useState(false);
  const [addSupplier, setAddSupplier] = useState(false);
  const [billNo, setBillNo] = useState("");
  const [billDate, setBillDate] = useState(today);
  const [rows, setRows] = useState<Row[]>(() => [blankRow(), blankRow()]);
  const [activeRow, setActiveRow] = useState<number | null>(null);
  const [results, setResults] = useState<LookupResult[]>([]);
  const [highlight, setHighlight] = useState(0);
  const [itemModal, setItemModal] = useState<{ rowKey: number; name: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const cells = useRef(new Map<string, HTMLInputElement>());
  const supplierBox = useRef<HTMLDivElement>(null);

  const suppliers = useQuery({
    queryKey: ["supplier-search", supplierQuery],
    queryFn: () => api.getListField<Party>(`/contacts/parties?q=${encodeURIComponent(supplierQuery)}`, "parties"),
    enabled: supplierOpen,
  });

  const activeQuery = rows.find((r) => r.key === activeRow)?.query ?? "";
  function lookup(q: string) {
    const params = new URLSearchParams({ q });
    if (org.warehouse) params.set("warehouse_id", org.warehouse.ID);
    return api.get<{ results: LookupResult[] | null }>(`/sales/billing-lookup?${params.toString()}`).then((res) => res.results ?? []);
  }
  useEffect(() => {
    if (activeRow === null) return;
    let stale = false;
    const handle = setTimeout(() => {
      lookup(activeQuery)
        .then((r) => {
          if (stale) return;
          setResults(r);
          setHighlight(0);
        })
        .catch(() => !stale && setResults([]));
    }, 150);
    return () => {
      stale = true;
      clearTimeout(handle);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeQuery, activeRow, org.warehouse]);

  useEffect(() => {
    const close = (e: MouseEvent) => {
      if (supplierBox.current && !supplierBox.current.contains(e.target as Node)) setSupplierOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, []);

  const filled = rows.filter((r) => r.variantId);
  const subtotal = filled.reduce((s, r) => s + num(r.qty) * num(r.rate), 0);
  const total = filled.reduce((s, r) => s + lineAmount(r), 0);
  const discountTotal = subtotal - total;

  function patch(key: number, p: Partial<Row>) {
    setRows((rs) => rs.map((r) => (r.key === key ? { ...r, ...p } : r)));
  }
  function focusCell(key: number, col: (typeof COLS)[number]) {
    const el = cells.current.get(`${key}:${col}`);
    if (el) el.focus();
    else requestAnimationFrame(() => cells.current.get(`${key}:${col}`)?.focus());
  }
  function focusFirstItem() {
    const first = rows[0];
    if (first) focusCell(first.key, "item");
  }
  function nextRow(key: number) {
    const i = rows.findIndex((r) => r.key === key);
    const following = rows[i + 1];
    if (following) return following.key;
    const fresh = blankRow();
    setRows((rs) => [...rs, fresh]);
    return fresh.key;
  }

  async function chooseItem(key: number, r: LookupResult) {
    const product = await api.get<{ BaseUOMID: string }>(`/catalogue/products/${r.ProductID}`);
    const units = await api.getListField<{ ID: string; Code: string }>("/catalogue/units", "units_of_measure");
    patch(key, {
      query: r.ProductName,
      name: r.ProductName,
      variantId: r.ProductVariantID,
      productId: r.ProductID,
      unitId: product.BaseUOMID,
      unitCode: units.find((u) => u.ID === product.BaseUOMID)?.Code ?? "",
      // The catalogue knows the sale price; the purchase cost is whatever
      // this bill says, so only a sensible starting point is offered.
      rate: "",
    });
    setActiveRow(null);
    setResults([]);
    focusCell(key, "qty");
  }

  function applyNewItem(item: NewItem) {
    const key = itemModal?.rowKey;
    if (key === undefined) return;
    patch(key, { query: item.name, name: item.name, variantId: item.variantId, productId: item.productId, unitId: item.unitId, unitCode: item.unitCode, taxRate: item.taxRate });
    // "Save & add another" keeps the dialog open: the next new item lands
    // in the following row instead of overwriting this one.
    setItemModal({ rowKey: nextRow(key), name: "" });
    focusCell(key, "qty");
  }

  const save = useMutation({
    mutationFn: async (finalize: boolean) => {
      if (!supplier) throw new Error("Choose a supplier first.");
      if (!org.branch || !org.warehouse) throw new Error("Missing branch or warehouse. Check Settings.");
      if (filled.length === 0) throw new Error("Add at least one item.");
      const bad = filled.find((r) => num(r.qty) <= 0 || num(r.rate) < 0 || r.rate.trim() === "");
      if (bad) throw new Error(`Enter a quantity and a rate for ${bad.name}.`);
      const doc = await api.post<{ ID: string }>("/purchases/documents", {
        branch_id: org.branch.ID,
        warehouse_id: org.warehouse.ID,
        supplier_party_id: supplier.ID,
        document_type: "PURCHASE_INVOICE",
        currency_code: currency,
        supplier_invoice_number: billNo.trim(),
        supplier_invoice_date: billDate ? `${billDate}T00:00:00Z` : null,
        notes: discountTotal > 0 ? `Discount allowed: ${money(discountTotal)}` : "",
      });
      for (const r of filled) {
        await api.post(`/purchases/documents/${doc.ID}/lines`, {
          product_variant_id: r.variantId,
          unit_id: r.unitId,
          quantity: String(num(r.qty)),
          unit_price: netRate(r).toFixed(2),
          batch_code: "",
        });
      }
      if (finalize) await api.post(`/purchases/documents/${doc.ID}/finalize`);
      return doc.ID;
    },
    onSuccess: (id) => onSaved(id),
    onError: (e) => setError(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "Could not save this purchase."),
  });

  // Ctrl+Enter saves from anywhere in the grid.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.key === "Enter" && !save.isPending) {
        e.preventDefault();
        setError(null);
        save.mutate(true);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  });

  const supplierList = useMemo(() => suppliers.data ?? [], [suppliers.data]);

  function cellKey(key: number, col: (typeof COLS)[number]) {
    return (e: React.KeyboardEvent<HTMLInputElement>) => {
      const i = COLS.indexOf(col);
      if (col === "item") {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          setHighlight((h) => Math.min(h + 1, results.length - 1));
        } else if (e.key === "ArrowUp") {
          e.preventDefault();
          setHighlight((h) => Math.max(h - 1, 0));
        } else if (e.key === "Enter") {
          e.preventDefault();
          const row = rows.find((r) => r.key === key);
          if (row?.variantId && row.query === row.name) focusCell(key, "qty");
          else if (row?.query.trim()) {
            // Never trust the on-screen list here: a scanner or a fast
            // typist presses Enter before the debounced search has caught
            // up, so look the typed text up fresh.
            const typed = row.query.trim();
            void lookup(typed).then((fresh) => {
              const pick = fresh.length === 1 ? fresh[0] : results.length === fresh.length ? results[highlight] : fresh[0];
              if (pick) void chooseItem(key, pick);
              else setItemModal({ rowKey: key, name: typed });
            });
          }
        } else if (e.key === "Escape") {
          setActiveRow(null);
        }
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        const nextCol = COLS[i + 1];
        if (nextCol) focusCell(key, nextCol);
        else focusCell(nextRow(key), "item");
      }
    };
  }

  const remove = (key: number) => setRows((rs) => (rs.length > 1 ? rs.filter((r) => r.key !== key) : [blankRow()]));

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>New purchase</h1>
          <p className={layout.subtitle}>Pick the supplier, type the items, press Ctrl+Enter to save.</p>
        </div>
        <button
          type="button"
          className={ui.btnSecondary}
          onClick={() => {
            if (filled.length === 0 || window.confirm("Discard this purchase? The items you entered will be lost.")) onBack();
          }}
        >
          Back to list
        </button>
      </div>

      <div className={layout.panel}>
        <div className={styles.head}>
          <div className={`${ui.field} ${styles.supplier}`} ref={supplierBox}>
            <label htmlFor="pe-supplier">Supplier *</label>
            {supplier ? (
              <div className={styles.picked}>
                <strong>{supplier.LegalName}</strong>
                <button type="button" className={ui.btnSecondary} onClick={() => setSupplier(null)}>
                  Change
                </button>
              </div>
            ) : (
              <>
                <input
                  id="pe-supplier"
                  className={ui.input}
                  role="combobox"
                  aria-expanded={supplierOpen}
                  aria-controls="pe-supplier-list"
                  autoComplete="off"
                  placeholder="Search by name or phone…"
                  value={supplierQuery}
                  onFocus={() => setSupplierOpen(true)}
                  onChange={(e) => {
                    setSupplierQuery(e.target.value);
                    setSupplierOpen(true);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      if (supplierList[0]) {
                        setSupplier(supplierList[0]);
                        setSupplierOpen(false);
                        focusFirstItem();
                      } else if (supplierQuery.trim()) setAddSupplier(true);
                    }
                  }}
                />
                {supplierOpen ? (
                  <ul id="pe-supplier-list" className={styles.menu} role="listbox">
                    <li>
                      <button type="button" className={styles.addOption} onClick={() => setAddSupplier(true)}>
                        + Add party{supplierQuery.trim() ? ` “${supplierQuery.trim()}”` : ""}
                      </button>
                    </li>
                    {supplierList.map((p) => (
                      <li key={p.ID} role="option" aria-selected="false">
                        <button
                          type="button"
                          className={styles.option}
                          onClick={() => {
                            setSupplier(p);
                            setSupplierOpen(false);
                            focusFirstItem();
                          }}
                        >
                          {p.LegalName}
                        </button>
                      </li>
                    ))}
                    {suppliers.isPending ? <li className={styles.hint}>Loading…</li> : supplierList.length === 0 ? <li className={styles.hint}>No supplier found.</li> : null}
                  </ul>
                ) : null}
              </>
            )}
          </div>
          <div className={ui.field}>
            <label htmlFor="pe-billno">Supplier bill number</label>
            <input id="pe-billno" className={ui.input} value={billNo} onChange={(e) => setBillNo(e.target.value)} />
          </div>
          <div className={ui.field}>
            <label htmlFor="pe-billdate">Bill date</label>
            <input id="pe-billdate" type="date" className={ui.input} value={billDate} max={today} onChange={(e) => setBillDate(e.target.value)} />
          </div>
        </div>
      </div>

      <div className={layout.panel}>
        <div className={ui.tableScroll}>
          <table className={`${ui.table} ${styles.grid}`}>
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col" className={styles.itemCol}>
                  Item
                </th>
                <th scope="col">Qty</th>
                <th scope="col">Unit</th>
                <th scope="col">Rate (before discount)</th>
                <th scope="col">Discount %</th>
                <th scope="col" className="num">
                  Amount
                </th>
                <th scope="col">
                  <span className="sr-only">Remove</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, idx) => (
                <tr key={r.key}>
                  <td className="num">{idx + 1}</td>
                  <td className={styles.itemCell}>
                    <input
                      ref={(el) => {
                        if (el) cells.current.set(`${r.key}:item`, el);
                      }}
                      className={ui.input}
                      aria-label={`Item, row ${idx + 1}`}
                      role="combobox"
                      aria-expanded={activeRow === r.key}
                      autoComplete="off"
                      placeholder="Type an item name or scan a barcode"
                      value={r.query}
                      onFocus={() => setActiveRow(r.key)}
                      onChange={(e) => patch(r.key, { query: e.target.value, variantId: "", name: "" })}
                      onKeyDown={cellKey(r.key, "item")}
                    />
                    {activeRow === r.key && !r.variantId ? (
                      <ul className={styles.menu} role="listbox">
                        <li>
                          <button type="button" className={styles.addOption} onMouseDown={(e) => e.preventDefault()} onClick={() => setItemModal({ rowKey: r.key, name: r.query.trim() })}>
                            + Add new item{r.query.trim() ? ` “${r.query.trim()}”` : ""}
                          </button>
                        </li>
                        {results.map((x, i) => (
                          <li key={x.ProductVariantID} role="option" aria-selected={i === highlight}>
                            <button type="button" className={styles.option} data-active={i === highlight} onMouseDown={(e) => e.preventDefault()} onClick={() => void chooseItem(r.key, x)}>
                              <span>{x.ProductName}</span>
                              <span className={ui.muted}>
                                {x.UnitPrice ? `Sale ${money(num(x.UnitPrice.amount))} · ` : ""}Stock {x.QuantityOnHand || "0"}
                              </span>
                            </button>
                          </li>
                        ))}
                      </ul>
                    ) : null}
                  </td>
                  <td>
                    <input
                      ref={(el) => {
                        if (el) cells.current.set(`${r.key}:qty`, el);
                      }}
                      className={ui.input}
                      aria-label={`Quantity, row ${idx + 1}`}
                      inputMode="decimal"
                      value={r.qty}
                      onChange={(e) => patch(r.key, { qty: e.target.value.replace(/[^\d.]/g, "") })}
                      onFocus={(e) => e.currentTarget.select()}
                      onKeyDown={cellKey(r.key, "qty")}
                    />
                  </td>
                  <td>{r.unitCode || "—"}</td>
                  <td>
                    <input
                      ref={(el) => {
                        if (el) cells.current.set(`${r.key}:rate`, el);
                      }}
                      className={ui.input}
                      aria-label={`Rate, row ${idx + 1}`}
                      inputMode="decimal"
                      value={r.rate}
                      onChange={(e) => patch(r.key, { rate: e.target.value.replace(/[^\d.]/g, "") })}
                      onFocus={(e) => e.currentTarget.select()}
                      onKeyDown={cellKey(r.key, "rate")}
                    />
                  </td>
                  <td>
                    <input
                      ref={(el) => {
                        if (el) cells.current.set(`${r.key}:discount`, el);
                      }}
                      className={ui.input}
                      aria-label={`Discount percent, row ${idx + 1}`}
                      inputMode="decimal"
                      value={r.discount}
                      onChange={(e) => patch(r.key, { discount: e.target.value.replace(/[^\d.]/g, "") })}
                      onFocus={(e) => e.currentTarget.select()}
                      onKeyDown={cellKey(r.key, "discount")}
                    />
                  </td>
                  <td className="num">{r.variantId ? money(lineAmount(r)) : "—"}</td>
                  <td>
                    <button type="button" className={styles.remove} aria-label={`Remove row ${idx + 1}`} onClick={() => remove(r.key)}>
                      ×
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className={styles.foot}>
          <button type="button" className={ui.btnSecondary} onClick={() => focusCell(nextRow(rows[rows.length - 1]?.key ?? 0), "item")}>
            + Add row
          </button>
          <dl className={styles.totals}>
            {discountTotal > 0 ? (
              <>
                <dt>Discount</dt>
                <dd>− {money(discountTotal)}</dd>
              </>
            ) : null}
            <dt>Total before tax</dt>
            <dd>
              <strong>{money(total)}</strong> {currency}
            </dd>
          </dl>
        </div>
        <p className={ui.muted}>GST on this bill is worked out from each item&apos;s HSN code and the supplier&apos;s GST details when you save.</p>
      </div>

      {error ? (
        <p role="alert" style={{ color: "var(--color-negative)" }}>
          {error}
        </p>
      ) : null}
      <div className={ui.formActions}>
        <button
          type="button"
          className={ui.btnSecondary}
          disabled={save.isPending}
          onClick={() => {
            setError(null);
            save.mutate(false);
          }}
        >
          Save as draft
        </button>
        <button
          type="button"
          className={ui.btnPrimary}
          disabled={save.isPending}
          onClick={() => {
            setError(null);
            save.mutate(true);
          }}
        >
          {save.isPending ? "Saving…" : "Save purchase"}
        </button>
      </div>

      <QuickAddPartyModal
        open={addSupplier}
        onOpenChange={setAddSupplier}
        partyType="SUPPLIER"
        currencyCode={currency}
        initialLegalName={supplierQuery.trim()}
        onCreated={(p) => {
          setSupplier(p);
          setSupplierOpen(false);
          setSupplierQuery("");
        }}
      />
      <QuickAddItemModal open={itemModal !== null} onOpenChange={(o) => !o && setItemModal(null)} defaultName={itemModal?.name} onCreated={applyNewItem} />
    </div>
  );
}
