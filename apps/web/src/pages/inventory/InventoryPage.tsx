import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { ReportTable } from "../../components/ReportTable";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";

/** Mirrors internal/modules/sales/app.BillingLookup's result shape
 * (apps/web/src/pages/sales/BillingPage.tsx already trusts this exact
 * shape for the same product-search-plus-stock use case). */
interface LookupResult {
  ProductID: string;
  ProductName: string;
  HSNSACCode: string;
  ProductVariantID: string;
  SKUCode: string;
  QuantityOnHand: string;
  QuantityAvailable: string;
}
/** Mirrors internal/modules/inventory/domain.StockBalance. */
interface StockBalance {
  QuantityOnHand: string;
  QuantityReserved: string;
  AverageCost: string;
}
/** Mirrors internal/modules/inventory/domain.StockMovement. */
interface StockMovement {
  ID: string;
  MovementType: string;
  Quantity: string;
  ReferenceType: string;
  Notes: string;
  CreatedAt: string;
}

const MOVEMENT_TYPE_LABELS: Record<string, string> = {
  OPENING: "Opening stock",
  PURCHASE_RECEIPT: "Purchase",
  PURCHASE_RETURN: "Purchase return",
  SALE: "Sale",
  SALE_RETURN: "Sale return",
  TRANSFER_IN: "Transfer in",
  TRANSFER_OUT: "Transfer out",
  ADJUSTMENT_IN: "Adjustment (added)",
  ADJUSTMENT_OUT: "Adjustment (removed)",
  ASSEMBLY_IN: "Assembly in",
  ASSEMBLY_OUT: "Assembly out",
  DAMAGE: "Damage",
  EXPIRY: "Expiry",
};

/** Selected product's stock card — current/reserved/available/average
 * cost, a movement timeline, and a manual adjustment form. None of
 * internal/modules/inventory's GET /inventory/balances, GET
 * /inventory/movements, or POST /inventory/adjustments had a frontend
 * caller before this — the whole module was reachable only via two
 * report tables (low-stock, valuation), never a real per-product view. */
function StockCard({ product, warehouseId, baseUOMID }: { product: LookupResult; warehouseId: string; baseUOMID: string }) {
  const queryClient = useQueryClient();
  const [adjQty, setAdjQty] = useState("");
  const [adjType, setAdjType] = useState("ADJUSTMENT_IN");
  const [adjReason, setAdjReason] = useState("");

  const balanceKey = ["inventory-balance", warehouseId, product.ProductVariantID];
  const movementsKey = ["inventory-movements", warehouseId, product.ProductVariantID];

  const balance = useQuery({
    queryKey: balanceKey,
    queryFn: () => api.get<StockBalance>(`/inventory/balances?warehouse_id=${warehouseId}&product_variant_id=${product.ProductVariantID}`),
  });
  const movements = useQuery({
    queryKey: movementsKey,
    queryFn: () => api.getListField<StockMovement>(`/inventory/movements?warehouse_id=${warehouseId}&product_variant_id=${product.ProductVariantID}`, "movements"),
  });

  const adjust = useMutation({
    mutationFn: () =>
      api.post("/inventory/adjustments", {
        warehouse_id: warehouseId,
        reason: adjReason || MOVEMENT_TYPE_LABELS[adjType],
        notes: "",
        lines: [{ product_variant_id: product.ProductVariantID, unit_id: baseUOMID, quantity: adjQty, movement_type: adjType }],
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: balanceKey });
      void queryClient.invalidateQueries({ queryKey: movementsKey });
      void queryClient.invalidateQueries({ queryKey: ["report-table"] }); // low-stock/valuation tables
      setAdjQty("");
      setAdjReason("");
    },
  });

  const onHand = Number(balance.data?.QuantityOnHand ?? "0");
  const reserved = Number(balance.data?.QuantityReserved ?? "0");

  return (
    <div className={layout.panel}>
      <h2>
        {product.ProductName} <span className={ui.muted}>· {product.SKUCode}</span>
      </h2>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>HSN/SAC: {product.HSNSACCode || "—"}</p>

      {balance.isPending ? (
        <div className={layout.skeleton} style={{ height: 60 }} aria-hidden="true" />
      ) : balance.isError ? (
        <p className={layout.errorState} role="alert">Couldn't load stock for this product.</p>
      ) : (
        <div className={ui.formGrid} style={{ marginBottom: 20 }}>
          <div>
            <span className={ui.muted}>Current stock</span>
            <div className="num">{onHand}</div>
          </div>
          <div>
            <span className={ui.muted}>Reserved</span>
            <div className="num">{reserved}</div>
          </div>
          <div>
            <span className={ui.muted}>Available</span>
            <div className="num">{onHand - reserved}</div>
          </div>
          <div>
            <span className={ui.muted}>Average cost</span>
            <div className="num">₹{Number(balance.data?.AverageCost ?? "0").toFixed(2)}</div>
          </div>
        </div>
      )}

      <h3 style={{ marginBottom: 8 }}>Stock adjustment</h3>
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="adj-type">Type</label>
          <select id="adj-type" className={ui.select} value={adjType} onChange={(e) => setAdjType(e.target.value)}>
            <option value="ADJUSTMENT_IN">Add stock (found / recount)</option>
            <option value="ADJUSTMENT_OUT">Remove stock (recount)</option>
            <option value="DAMAGE">Damaged</option>
            <option value="EXPIRY">Expired</option>
          </select>
        </div>
        <div className={ui.field}>
          <label htmlFor="adj-qty">Quantity</label>
          <input id="adj-qty" className={ui.input} value={adjQty} onChange={(e) => setAdjQty(e.target.value)} placeholder="0" />
        </div>
        <div className={ui.field}>
          <label htmlFor="adj-reason">Reason (optional)</label>
          <input id="adj-reason" className={ui.input} value={adjReason} onChange={(e) => setAdjReason(e.target.value)} />
        </div>
        <button type="button" className={ui.btnSecondary} disabled={!adjQty || adjust.isPending} onClick={() => adjust.mutate()}>
          {adjust.isPending ? "Saving…" : "Record adjustment"}
        </button>
      </div>
      {adjust.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {adjust.error instanceof ApiError ? adjust.error.message : "Could not record this adjustment."}
        </p>
      ) : null}

      <h3 style={{ margin: "20px 0 8px" }}>Recent movements</h3>
      {movements.isPending ? (
        <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />
      ) : (movements.data ?? []).length === 0 ? (
        <p className={layout.emptyState}>No stock movements recorded yet for this product.</p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Date</th>
                <th scope="col">Type</th>
                <th scope="col">Quantity</th>
                <th scope="col">Notes</th>
              </tr>
            </thead>
            <tbody>
              {(movements.data ?? []).map((m) => (
                <tr key={m.ID}>
                  <td>{new Date(m.CreatedAt).toLocaleString()}</td>
                  <td>{MOVEMENT_TYPE_LABELS[m.MovementType] ?? m.MovementType}</td>
                  <td className="num">{m.Quantity}</td>
                  <td>{m.Notes || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export function InventoryPage() {
  const org = useOrgContext();
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<{ result: LookupResult; baseUOMID: string } | null>(null);

  const lookup = useQuery({
    queryKey: ["inventory-lookup", query, org.warehouse?.ID],
    queryFn: () => api.get<{ results: LookupResult[] | null }>(`/sales/billing-lookup?q=${encodeURIComponent(query)}&warehouse_id=${org.warehouse?.ID}`),
    enabled: query.trim().length >= 2 && !!org.warehouse,
  });

  async function selectProduct(r: LookupResult) {
    const product = await api.get<{ BaseUOMID: string }>(`/catalogue/products/${r.ProductID}`);
    setSelected({ result: r, baseUOMID: product.BaseUOMID });
    setQuery("");
  }

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Inventory</h1>
          <p className={layout.subtitle}>Search a product to see its stock, movement history, and adjust it.</p>
        </div>
        {/* This page could only ever search products that already existed —
            someone realising mid-stocktake that a product isn't in the
            system had to work out on their own that products are created
            over on Catalogue. */}
        <Link to="/catalogue" search={{ new: true }} className={ui.btnPrimary}>
          + New product
        </Link>
      </div>

      <div className={layout.panel}>
        <div className={ui.field} style={{ maxWidth: 420, position: "relative" }}>
          <label htmlFor="inventory-search">Search a product by name or SKU</label>
          <input
            id="inventory-search"
            className={ui.input}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="e.g. Parle-G 800g"
          />
          {lookup.data?.results?.length ? (
            <ul style={{ listStyle: "none", margin: "4px 0 0", padding: 0, border: "1px solid var(--color-border)", borderRadius: "var(--radius-md)" }}>
              {lookup.data.results.map((r) => (
                <li key={r.ProductVariantID}>
                  <button type="button" className={ui.linkRowButton} style={{ padding: "8px 10px" }} onClick={() => void selectProduct(r)}>
                    {r.ProductName} <span className={ui.muted}>· {r.SKUCode} · {r.QuantityOnHand} in stock</span>
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </div>

      {selected ? (
        <StockCard product={selected.result} warehouseId={org.warehouse!.ID} baseUOMID={selected.baseUOMID} />
      ) : null}

      <div className={layout.panel}>
        <h2>Low stock</h2>
        <ReportTable path="/reports/inventory/low-stock?format=json" emptyLabel="Nothing is running low." />
      </div>

      <div className={layout.panel}>
        <h2>Stock valuation</h2>
        <ReportTable path="/reports/inventory/valuation?format=json" emptyLabel="No stock recorded yet." />
      </div>
    </div>
  );
}
