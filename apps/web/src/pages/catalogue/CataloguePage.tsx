import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ImportPanel } from "../../components/ImportPanel";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";
import { ImportAiMarkdownButton } from "../purchases/ImportAiMarkdownButton";
import { ScanBillButton } from "../purchases/ScanBillButton";

interface Product {
  ID: string;
  Name: string;
  HSNSACCode: string;
  BaseUOMID: string;
  CategoryID: string | null;
  BrandID: string | null;
  Status: "ACTIVE" | "INACTIVE";
  // Added by catalogue/httpapi's productListDTO — the product's first
  // variant, i.e. the one this page's single "Price" field reads/writes
  // (see priceByVariantId below). Absent only if a product somehow has
  // zero variants, which this form never itself creates.
  DefaultVariantID?: string;
}
interface Unit {
  ID: string;
  Code: string;
  Name: string;
}
interface Category {
  ID: string;
  Name: string;
}
interface Brand {
  ID: string;
  Name: string;
}
interface PriceListItem {
  ProductVariantID: string;
  UnitID: string;
  Price: { amount: string; currency: string };
}
interface BulkDeleteResult {
  hard_deleted: string[];
  deactivated: string[];
}
interface StockBalance {
  QuantityOnHand: string;
}

const ADJUSTMENT_REASON_BY_TYPE: Record<string, string> = {
  ADJUSTMENT_IN: "Add stock (found / recount)",
  ADJUSTMENT_OUT: "Remove stock (recount)",
  DAMAGE: "Damaged",
  EXPIRY: "Expired",
};

/** Editing an existing product used to have no way to change its stock at
 * all — PUT /catalogue/products/{id} only ever covered the product's own
 * fields (name/HSN/unit/category/brand), so a shop owner correcting a
 * count mid-edit had to abandon this form, go to Inventory, search for
 * the same product again, and adjust it there. Same POST
 * /inventory/adjustments call InventoryPage's own StockCard already uses
 * (Stage 13) — kept intentionally small here (current balance + one
 * adjustment form) rather than the movement-history table that page
 * also shows, since this is a side panel inside product editing, not a
 * dedicated stock screen. */
function StockChangeSection({ variantId, warehouseId, baseUOMID }: { variantId: string; warehouseId: string; baseUOMID: string }) {
  const queryClient = useQueryClient();
  const [adjQty, setAdjQty] = useState("");
  const [adjType, setAdjType] = useState("ADJUSTMENT_IN");
  const [adjReason, setAdjReason] = useState("");

  const balanceKey = ["inventory-balance", warehouseId, variantId];
  const balance = useQuery({
    queryKey: balanceKey,
    queryFn: () => api.get<StockBalance>(`/inventory/balances?warehouse_id=${warehouseId}&product_variant_id=${variantId}`),
  });

  const adjust = useMutation({
    mutationFn: () =>
      api.post("/inventory/adjustments", {
        warehouse_id: warehouseId,
        reason: adjReason || ADJUSTMENT_REASON_BY_TYPE[adjType],
        notes: "",
        lines: [{ product_variant_id: variantId, unit_id: baseUOMID, quantity: adjQty, movement_type: adjType }],
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: balanceKey });
      void queryClient.invalidateQueries({ queryKey: ["report-table"] }); // low-stock/valuation tables
      setAdjQty("");
      setAdjReason("");
    },
  });

  return (
    <div className={ui.field} style={{ gridColumn: "1 / -1" }}>
      <label>Stock (in {warehouseId ? "your current warehouse" : "—"})</label>
      <p style={{ margin: "0 0 8px" }}>
        Current stock:{" "}
        <strong className="num">{balance.isPending ? "…" : balance.isError ? "—" : Number(balance.data?.QuantityOnHand ?? "0")}</strong>
      </p>
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="edit-stock-type">Type</label>
          <select id="edit-stock-type" className={ui.select} value={adjType} onChange={(e) => setAdjType(e.target.value)}>
            <option value="ADJUSTMENT_IN">Add stock (found / recount)</option>
            <option value="ADJUSTMENT_OUT">Remove stock (recount)</option>
            <option value="DAMAGE">Damaged</option>
            <option value="EXPIRY">Expired</option>
          </select>
        </div>
        <div className={ui.field}>
          <label htmlFor="edit-stock-qty">Quantity</label>
          <input id="edit-stock-qty" className={ui.input} value={adjQty} onChange={(e) => setAdjQty(e.target.value)} placeholder="0" />
        </div>
        <div className={ui.field}>
          <label htmlFor="edit-stock-reason">Reason (optional)</label>
          <input id="edit-stock-reason" className={ui.input} value={adjReason} onChange={(e) => setAdjReason(e.target.value)} />
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
    </div>
  );
}

export function CataloguePage({ openNewForm = false }: { openNewForm?: boolean }) {
  const queryClient = useQueryClient();
  const org = useOrgContext();
  const [query, setQuery] = useState("");
  // Opened directly from another page's "+ New product" button (e.g.
  // Inventory, where a shop owner discovers a product doesn't exist yet)
  // via /catalogue?new=1 — the form is right here, it just used to be
  // one unexplained click away for anyone arriving from elsewhere.
  const [showForm, setShowForm] = useState(openNewForm);
  // Set while editing an existing product — the same form panel is
  // reused, but submit calls updateProduct instead of createProduct and
  // the create-only fields below (SKU/barcode/opening stock/GST rate)
  // are hidden, since PUT /catalogue/products/{id} only covers the
  // fields a product itself has (name/HSN/unit/category/brand).
  const [editingId, setEditingId] = useState<string | null>(null);
  // The product being edited's first variant — captured at startEdit so
  // the Price field's save can target the right variant without a
  // separate lookup. See DefaultVariantID's own comment above.
  const [editingVariantId, setEditingVariantId] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);
  const [confirmingBulkDelete, setConfirmingBulkDelete] = useState(false);
  const [confirmingBulkRestore, setConfirmingBulkRestore] = useState(false);
  // Transient summary shown after a delete completes — the backend now
  // reports which ids were actually removed vs. which still have
  // sales/purchase history and were deactivated instead (see
  // catalogue.app.Service.DeleteProductsIfUnused), which the old plain
  // "204 No Content" response gave no way to surface.
  const [lastDeleteResult, setLastDeleteResult] = useState<BulkDeleteResult | null>(null);
  const [name, setName] = useState("");
  const [hsn, setHsn] = useState("");
  const [unitId, setUnitId] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("");
  const [gstRate, setGstRate] = useState("");
  // There's only ever one price per product now (the Pricing page and
  // its per-list prices are gone) — this sets it directly on the org's
  // one price list, auto-created on first use via ensure-default below.
  const [price, setPrice] = useState("");
  const [skuCode, setSkuCode] = useState("");
  const [newUnitCode, setNewUnitCode] = useState("");
  const [newUnitName, setNewUnitName] = useState("");
  const [newCategoryName, setNewCategoryName] = useState("");
  const [newBrandName, setNewBrandName] = useState("");
  // A brand-new product otherwise has zero stock the moment it's saved —
  // the very first sale of it fails with INSUFFICIENT_STOCK, and nothing
  // on this screen ever told the person creating it that they'd need to
  // separately visit Inventory afterward to fix that. Optional: leave
  // both blank and skip it, same as before.
  const [openingQty, setOpeningQty] = useState("");
  const [openingCost, setOpeningCost] = useState("");
  const [barcode, setBarcode] = useState("");

  const products = useQuery({
    queryKey: ["products", query],
    queryFn: () => api.getListField<Product>(`/catalogue/products${query ? `?q=${encodeURIComponent(query)}` : ""}`, "products"),
  });
  const units = useQuery({
    queryKey: ["units"],
    queryFn: () => api.getListField<Unit>("/catalogue/units", "units_of_measure"),
  });
  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: () => api.getListField<Category>("/catalogue/categories", "categories"),
  });
  const brands = useQuery({
    queryKey: ["brands"],
    queryFn: () => api.getListField<Brand>("/catalogue/brands", "brands"),
  });
  const categoryNameById = new Map(categories.data?.map((c) => [c.ID, c.Name]));
  const brandNameById = new Map(brands.data?.map((b) => [b.ID, b.Name]));

  // ensure-default is idempotent — creates the org's "Default" price
  // list on first call (fixing "price not displaying" on a fresh
  // install, see pricing.app.Service.EnsureDefaultPriceList's own doc
  // comment) and just returns it every call after, so it's safe to fire
  // on every page load with no separate setup step.
  const defaultPriceList = useQuery({
    queryKey: ["default-price-list", org.organisation?.DefaultCurrencyCode],
    queryFn: () => api.post<{ ID: string }>("/pricing/price-lists/ensure-default", { currency_code: org.organisation?.DefaultCurrencyCode || "INR" }),
    enabled: !!org.organisation,
  });
  const priceItems = useQuery({
    queryKey: ["price-items", defaultPriceList.data?.ID],
    queryFn: () => api.getListField<PriceListItem>(`/pricing/price-lists/${defaultPriceList.data?.ID}/items`, "items"),
    enabled: !!defaultPriceList.data?.ID,
  });
  const priceByVariantId = new Map(priceItems.data?.map((item) => [item.ProductVariantID, item]));

  async function ensureAndSetPrice(variantId: string, unitIdForPrice: string, amount: string) {
    const currencyCode = org.organisation?.DefaultCurrencyCode || "INR";
    const priceList = await api.post<{ ID: string }>("/pricing/price-lists/ensure-default", { currency_code: currencyCode });
    await api.post(`/pricing/price-lists/${priceList.ID}/items`, {
      product_variant_id: variantId,
      unit_id: unitIdForPrice,
      amount,
      currency_code: currencyCode,
    });
  }

  const createUnit = useMutation({
    mutationFn: () => api.post<Unit>("/catalogue/units", { code: newUnitCode, name: newUnitName }),
    onSuccess: (u) => {
      void queryClient.invalidateQueries({ queryKey: ["units"] });
      setUnitId(u.ID);
      setNewUnitCode("");
      setNewUnitName("");
    },
  });

  const addDefaultUnits = useMutation({
    mutationFn: () => api.post<{ units_of_measure: Unit[] }>("/catalogue/units/ensure-default", {}),
    onSuccess: (res) => {
      void queryClient.invalidateQueries({ queryKey: ["units"] });
      const first = res.units_of_measure[0];
      if (first) setUnitId(first.ID);
    },
  });
  const createCategory = useMutation({
    mutationFn: () => api.post<Category>("/catalogue/categories", { name: newCategoryName, parent_id: null }),
    onSuccess: (c) => {
      void queryClient.invalidateQueries({ queryKey: ["categories"] });
      setCategoryId(c.ID);
      setNewCategoryName("");
    },
  });
  const createBrand = useMutation({
    mutationFn: () => api.post<Brand>("/catalogue/brands", { name: newBrandName }),
    onSuccess: (b) => {
      void queryClient.invalidateQueries({ queryKey: ["brands"] });
      setBrandId(b.ID);
      setNewBrandName("");
    },
  });

  const createProduct = useMutation({
    mutationFn: async () => {
      const product = await api.post<Product>("/catalogue/products", {
        category_id: categoryId || null,
        brand_id: brandId || null,
        base_uom_id: unitId,
        name,
        description: "",
        hsn_sac_code: hsn,
      });
      const variant = await api.post<{ ID: string }>(`/catalogue/variants`, {
        product_id: product.ID,
        sku_code: skuCode || product.Name.toUpperCase().replace(/[^A-Z0-9]+/g, "-").slice(0, 24),
        attributes: {},
      });
      if (barcode.trim()) {
        // Scanning this same barcode at the counter now actually finds
        // this product — BillingLookup previously had no barcode path
        // at all, despite the billing screen's own placeholder text
        // promising one.
        await api.post("/catalogue/barcodes", { variant_id: variant.ID, unit_id: unitId, barcode: barcode.trim() });
      }
      if (Number(openingQty) > 0 && org.warehouse) {
        await api.post("/inventory/opening-stock", {
          warehouse_id: org.warehouse.ID,
          product_variant_id: variant.ID,
          unit_id: unitId,
          quantity: openingQty,
          unit_cost: openingCost || "0",
        });
      }
      // Set right here so the product is ready to sell the moment it's
      // saved, instead of only ever getting a price via CSV import or a
      // (now-removed) separate Pricing page visit.
      if (price.trim()) {
        await ensureAndSetPrice(variant.ID, unitId, price.trim());
      }
      // Optional: set this HSN code's GST rate right here instead of
      // sending the user to a separate GST page just to make a freshly
      // added product actually billable at the right tax rate.
      if (gstRate) {
        await api.post("/gst/tax-rates", {
          hsn_sac_code: hsn,
          classification: "TAXABLE",
          gst_rate: gstRate,
          cess_rate: "0",
          valid_from: new Date().toISOString().slice(0, 10),
          valid_to: null,
        });
      }
      return product;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      void queryClient.invalidateQueries({ queryKey: ["price-items"] });
      setName("");
      setHsn("");
      setSkuCode("");
      setCategoryId("");
      setBrandId("");
      setGstRate("");
      setPrice("");
      setOpeningQty("");
      setOpeningCost("");
      setBarcode("");
      setShowForm(false);
    },
  });

  const updateProduct = useMutation({
    mutationFn: async () => {
      const updated = await api.put<Product>(`/catalogue/products/${editingId}`, {
        category_id: categoryId || null,
        brand_id: brandId || null,
        base_uom_id: unitId,
        name,
        description: "",
        hsn_sac_code: hsn,
      });
      if (price.trim() && editingVariantId) {
        await ensureAndSetPrice(editingVariantId, unitId, price.trim());
      }
      return updated;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      void queryClient.invalidateQueries({ queryKey: ["price-items"] });
      closeForm();
    },
  });

  function closeForm() {
    setShowForm(false);
    setEditingId(null);
    setEditingVariantId(null);
    setName("");
    setHsn("");
    setSkuCode("");
    setCategoryId("");
    setBrandId("");
    setGstRate("");
    setPrice("");
    setOpeningQty("");
    setOpeningCost("");
    setBarcode("");
  }

  function startEdit(p: Product) {
    setEditingId(p.ID);
    setEditingVariantId(p.DefaultVariantID ?? null);
    setName(p.Name);
    setHsn(p.HSNSACCode);
    setUnitId(p.BaseUOMID);
    setCategoryId(p.CategoryID ?? "");
    setBrandId(p.BrandID ?? "");
    setPrice(p.DefaultVariantID ? (priceByVariantId.get(p.DefaultVariantID)?.Price.amount ?? "") : "");
    setShowForm(true);
  }

  // "Delete" removes the product completely when it's never been used in
  // a sale/purchase/stock movement; a product with real history falls
  // back to deactivating it instead (kept out of search/billing, but its
  // past records still resolve) — see
  // catalogue.app.Service.DeleteProductsIfUnused's own doc comment. The
  // single-row button below shares this same endpoint with the bulk
  // action rather than the old always-soft-delete one, so "Delete" means
  // the same thing everywhere on this page.
  const deleteProducts = useMutation({
    mutationFn: (ids: string[]) => api.post<BulkDeleteResult>("/catalogue/products/bulk-delete", { ids }),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      void queryClient.invalidateQueries({ queryKey: ["price-items"] });
      setSelectedIds(new Set());
      setConfirmingBulkDelete(false);
      setConfirmingDeleteId(null);
      // Defensive: a bucket that stayed empty can come back as JSON null
      // rather than [] (a nil Go slice's JSON shape) — never trust it's
      // an array without checking.
      setLastDeleteResult({ hard_deleted: result.hard_deleted ?? [], deactivated: result.deactivated ?? [] });
    },
  });

  const restoreProducts = useMutation({
    mutationFn: (ids: string[]) => api.post("/catalogue/products/bulk-restore", { ids }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      setSelectedIds(new Set());
      setConfirmingBulkRestore(false);
    },
  });

  function toggleSelected(id: string) {
    setSelectedIds((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Catalogue</h1>
          <p className={layout.subtitle}>Products, sold as at least one variant each.</p>
        </div>
        <button
          type="button"
          className={ui.btnPrimary}
          onClick={() => {
            if (showForm && !editingId) closeForm();
            else {
              setEditingId(null);
              setEditingVariantId(null);
              setName("");
              setHsn("");
              setSkuCode("");
              setCategoryId("");
              setBrandId("");
              setGstRate("");
              setPrice("");
              setOpeningQty("");
              setOpeningCost("");
              setBarcode("");
              setShowForm(true);
            }
          }}
        >
          + New product
        </button>
      </div>

      <div style={{ marginBottom: 16, display: "flex", gap: 8 }}>
        <ScanBillButton />
        <ImportAiMarkdownButton />
      </div>

      {showForm ? (
        <div className={layout.panel}>
          <h2 style={{ marginTop: 0 }}>{editingId ? "Edit product" : "New product"}</h2>
          {units.data && units.data.length === 0 ? (
            <div style={{ marginBottom: 16 }}>
              <p className={ui.muted} style={{ marginBottom: 8 }}>
                You don't have any units of measure yet — this form needs at least one to save a product. (Bulk CSV
                import doesn't need this step — it creates whatever unit code each row asks for automatically.)
              </p>
              <button type="button" className={ui.btnPrimary} disabled={addDefaultUnits.isPending} onClick={() => addDefaultUnits.mutate()} style={{ marginBottom: 12 }}>
                {addDefaultUnits.isPending ? "Adding…" : "Add common units (PCS, KG, LTR, BOX, and more)"}
              </button>
              {addDefaultUnits.isError ? (
                <p role="alert" style={{ color: "var(--color-negative)", marginBottom: 12 }}>
                  {addDefaultUnits.error instanceof ApiError ? addDefaultUnits.error.message : "Could not add default units."}
                </p>
              ) : null}
              <div className={ui.formGrid}>
                <div className={ui.field}>
                  <label htmlFor="new-unit-code">Or add just one — code (e.g. PCS)</label>
                  <input id="new-unit-code" className={ui.input} value={newUnitCode} onChange={(e) => setNewUnitCode(e.target.value)} />
                </div>
                <div className={ui.field}>
                  <label htmlFor="new-unit-name">Unit name (e.g. Pieces)</label>
                  <input id="new-unit-name" className={ui.input} value={newUnitName} onChange={(e) => setNewUnitName(e.target.value)} />
                </div>
                <button
                  type="button"
                  className={ui.btnSecondary}
                  disabled={!newUnitCode || !newUnitName || createUnit.isPending}
                  onClick={() => createUnit.mutate()}
                >
                  Add unit
                </button>
              </div>
            </div>
          ) : null}
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="product-name">Product name</label>
              <input id="product-name" className={ui.input} value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className={ui.field}>
              <label htmlFor="product-hsn">HSN/SAC code</label>
              <input id="product-hsn" className={ui.input} value={hsn} onChange={(e) => setHsn(e.target.value)} />
            </div>
            <div className={ui.field}>
              <label htmlFor="product-gst">GST rate for this HSN (%, optional)</label>
              <input id="product-gst" className={ui.input} value={gstRate} onChange={(e) => setGstRate(e.target.value)} placeholder="e.g. 18" />
            </div>
            <div className={ui.field}>
              <label htmlFor="product-price">Price (optional)</label>
              <input
                id="product-price"
                className={ui.input}
                inputMode="decimal"
                value={price}
                onChange={(e) => setPrice(e.target.value)}
                placeholder="What does this sell for?"
              />
              <span className={ui.muted} style={{ display: "block", marginTop: 4 }}>
                Leave blank to set it later — a product with no price can't be sold yet.
              </span>
            </div>
            <div className={ui.field}>
              <label htmlFor="product-unit">Unit</label>
              <select id="product-unit" className={ui.select} value={unitId} onChange={(e) => setUnitId(e.target.value)}>
                <option value="">Select a unit…</option>
                {units.data?.map((u) => (
                  <option key={u.ID} value={u.ID}>
                    {u.Name} ({u.Code})
                  </option>
                ))}
              </select>
              {units.data && units.data.length > 0 ? (
                // Same inline "add without leaving the form" pattern as
                // Category/Brand below — previously a new unit could only
                // be added here while the org had ZERO units at all (the
                // onboarding block above), so anyone who already had e.g.
                // PCS but now also needed KG had to go to Settings first.
                <div style={{ display: "flex", gap: 6, marginTop: 6, minWidth: 0 }}>
                  <input
                    className={ui.input}
                    style={{ flex: "1 1 auto", minWidth: 0 }}
                    placeholder="New unit code (e.g. KG)"
                    value={newUnitCode}
                    onChange={(e) => setNewUnitCode(e.target.value)}
                  />
                  <input
                    className={ui.input}
                    style={{ flex: "1 1 auto", minWidth: 0 }}
                    placeholder="Unit name (e.g. Kilograms)"
                    value={newUnitName}
                    onChange={(e) => setNewUnitName(e.target.value)}
                  />
                  <button
                    type="button"
                    className={ui.btnSecondary}
                    style={{ flex: "0 0 auto" }}
                    disabled={!newUnitCode || !newUnitName || createUnit.isPending}
                    onClick={() => createUnit.mutate()}
                  >
                    Add
                  </button>
                </div>
              ) : null}
            </div>
            {!editingId ? (
              <>
                <div className={ui.field}>
                  <label htmlFor="product-sku">SKU (optional)</label>
                  <input id="product-sku" className={ui.input} value={skuCode} onChange={(e) => setSkuCode(e.target.value)} />
                </div>
                <div className={ui.field}>
                  <label htmlFor="product-barcode">Barcode (optional)</label>
                  <input id="product-barcode" className={ui.input} value={barcode} onChange={(e) => setBarcode(e.target.value)} placeholder="Scan or type it here" />
                </div>
                <div className={ui.field}>
                  <label htmlFor="product-opening-qty">Opening stock (optional)</label>
                  <input
                    id="product-opening-qty"
                    className={ui.input}
                    inputMode="decimal"
                    value={openingQty}
                    onChange={(e) => setOpeningQty(e.target.value)}
                    placeholder="How many do you have right now?"
                  />
                </div>
                {Number(openingQty) > 0 ? (
                  <div className={ui.field}>
                    <label htmlFor="product-opening-cost">Cost per unit (optional)</label>
                    <input id="product-opening-cost" className={ui.input} inputMode="decimal" value={openingCost} onChange={(e) => setOpeningCost(e.target.value)} placeholder="0.00" />
                  </div>
                ) : null}
              </>
            ) : editingVariantId && org.warehouse ? (
              <StockChangeSection variantId={editingVariantId} warehouseId={org.warehouse.ID} baseUOMID={unitId} />
            ) : null}
            <div className={ui.field}>
              <label htmlFor="product-category">Category (optional)</label>
              <select id="product-category" className={ui.select} value={categoryId} onChange={(e) => setCategoryId(e.target.value)}>
                <option value="">No category</option>
                {categories.data?.map((c) => (
                  <option key={c.ID} value={c.ID}>
                    {c.Name}
                  </option>
                ))}
              </select>
              <div style={{ display: "flex", gap: 6, marginTop: 6, minWidth: 0 }}>
                <input
                  className={ui.input}
                  style={{ flex: "1 1 auto", minWidth: 0 }}
                  placeholder="New category name"
                  value={newCategoryName}
                  onChange={(e) => setNewCategoryName(e.target.value)}
                />
                <button
                  type="button"
                  className={ui.btnSecondary}
                  style={{ flex: "0 0 auto" }}
                  disabled={!newCategoryName || createCategory.isPending}
                  onClick={() => createCategory.mutate()}
                >
                  Add
                </button>
              </div>
            </div>
            <div className={ui.field}>
              <label htmlFor="product-brand">Brand (optional)</label>
              <select id="product-brand" className={ui.select} value={brandId} onChange={(e) => setBrandId(e.target.value)}>
                <option value="">No brand</option>
                {brands.data?.map((b) => (
                  <option key={b.ID} value={b.ID}>
                    {b.Name}
                  </option>
                ))}
              </select>
              <div style={{ display: "flex", gap: 6, marginTop: 6, minWidth: 0 }}>
                <input
                  className={ui.input}
                  style={{ flex: "1 1 auto", minWidth: 0 }}
                  placeholder="New brand name"
                  value={newBrandName}
                  onChange={(e) => setNewBrandName(e.target.value)}
                />
                <button type="button" className={ui.btnSecondary} style={{ flex: "0 0 auto" }} disabled={!newBrandName || createBrand.isPending} onClick={() => createBrand.mutate()}>
                  Add
                </button>
              </div>
            </div>
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            {editingId ? (
              <>
                <button type="button" className={ui.btnPrimary} disabled={!name || !unitId || updateProduct.isPending} onClick={() => updateProduct.mutate()}>
                  {updateProduct.isPending ? "Saving…" : "Save changes"}
                </button>
                <button type="button" className={ui.btnSecondary} onClick={closeForm}>
                  Cancel
                </button>
              </>
            ) : (
              <button
                type="button"
                className={ui.btnPrimary}
                disabled={!name || !unitId || createProduct.isPending}
                onClick={() => createProduct.mutate()}
              >
                Save product
              </button>
            )}
          </div>
          {createProduct.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {createProduct.error instanceof ApiError ? createProduct.error.message : "Could not save this product."}
            </p>
          ) : null}
          {updateProduct.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {updateProduct.error instanceof ApiError ? updateProduct.error.message : "Could not save these changes."}
            </p>
          ) : null}
        </div>
      ) : null}

      <ImportPanel
        title="Bulk import products"
        path="/catalogue/products/import"
        columns={[
          "name",
          "hsn_sac_code (optional)",
          "base_uom_code (created automatically if it doesn't exist yet)",
          "sku_code (optional — generated from name if blank)",
          "price (optional — sets this product's price on your default price list)",
          "gst_rate (optional — sets the GST% for this HSN/SAC code)",
          "category_name (optional — created automatically if it doesn't exist yet)",
          "brand_name (optional — created automatically if it doesn't exist yet)",
          "barcode (optional — must be unique)",
          "opening_qty (optional — adds starting stock in your current warehouse)",
          "opening_cost (optional — per-unit cost for opening_qty, defaults to 0)",
        ]}
        // base_uom_code no longer needs to already exist —
        // catalogue.Service.ImportProducts auto-creates a unit from the
        // code if nothing matches (same as category_name/brand_name) —
        // so the sample always shows a real, working example (the org's
        // own first unit if it has one, otherwise a plain "PCS" that'll
        // be created on import) instead of only appearing once a unit
        // already exists. price/gst_rate/category_name/brand_name/
        // barcode/opening_qty are every field the single "New product"
        // form lets you set inline, now all available in bulk too —
        // every one of them optional, so a minimal 3-column file (name/
        // hsn_sac_code/base_uom_code) still imports cleanly.
        // opening_qty is recorded against the currently selected
        // warehouse (extraQuery below) — same single-warehouse
        // assumption the manual "New product" form's own opening-stock
        // field already makes (org.warehouse, no separate picker).
        sampleRows={[
          {
            name: "Amul Butter 500g",
            hsn_sac_code: "0405",
            base_uom_code: units.data?.[0]?.Code ?? "PCS",
            sku_code: "AMUL-BTR-500",
            price: "55.00",
            gst_rate: "5",
            category_name: "Dairy",
            brand_name: "Amul",
            barcode: "8901234567890",
            opening_qty: "24",
            opening_cost: "48.00",
          },
        ]}
        extraQuery={org.warehouse ? { warehouse_id: org.warehouse.ID } : undefined}
        onImported={() => void queryClient.invalidateQueries({ queryKey: ["products"] })}
      />

      <div className={layout.panel}>
        <input
          className={ui.input}
          placeholder="Search products…"
          aria-label="Search products"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ marginBottom: 12, maxWidth: 360 }}
        />
        {products.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load products.
          </p>
        ) : products.isPending ? (
          <div className={layout.skeleton} style={{ height: 200 }} aria-hidden="true" />
        ) : products.data.length === 0 ? (
          <p className={layout.emptyState}>No products yet — add your first one above.</p>
        ) : (
          <>
            {lastDeleteResult ? (
              <p className={ui.muted} style={{ marginBottom: 8 }} role="status">
                {lastDeleteResult.hard_deleted.length > 0 ? `Removed ${lastDeleteResult.hard_deleted.length} product(s) completely. ` : ""}
                {lastDeleteResult.deactivated.length > 0
                  ? `${lastDeleteResult.deactivated.length} product(s) have sales/purchase history, so they were deactivated instead of deleted.`
                  : ""}
                <button type="button" className={ui.btnGhost} style={{ marginLeft: 8 }} onClick={() => setLastDeleteResult(null)}>
                  Dismiss
                </button>
              </p>
            ) : null}
            {selectedIds.size > 0 ? (
              <div className={ui.toolbar} style={{ marginBottom: 8, gap: 8 }}>
                <span className={ui.muted}>{selectedIds.size} selected</span>
                {confirmingBulkDelete ? (
                  <>
                    <span>Delete {selectedIds.size} product(s)?</span>
                    <button type="button" className={ui.btnDanger} disabled={deleteProducts.isPending} onClick={() => deleteProducts.mutate([...selectedIds])}>
                      {deleteProducts.isPending ? "Deleting…" : "Confirm delete"}
                    </button>
                    <button type="button" className={ui.btnGhost} onClick={() => setConfirmingBulkDelete(false)}>
                      Cancel
                    </button>
                  </>
                ) : confirmingBulkRestore ? (
                  <>
                    <span>Restore {selectedIds.size} product(s)?</span>
                    <button type="button" className={ui.btnPrimary} disabled={restoreProducts.isPending} onClick={() => restoreProducts.mutate([...selectedIds])}>
                      {restoreProducts.isPending ? "Restoring…" : "Confirm restore"}
                    </button>
                    <button type="button" className={ui.btnGhost} onClick={() => setConfirmingBulkRestore(false)}>
                      Cancel
                    </button>
                  </>
                ) : (
                  <>
                    <button type="button" className={ui.btnSecondary} onClick={() => setConfirmingBulkRestore(true)}>
                      Active {selectedIds.size} product(s)
                    </button>
                    <button type="button" className={ui.btnSecondary} onClick={() => setConfirmingBulkDelete(true)}>
                      Delete {selectedIds.size} product(s)
                    </button>
                  </>
                )}
                <button type="button" className={ui.btnGhost} onClick={() => setSelectedIds(new Set())}>
                  Clear selection
                </button>
              </div>
            ) : null}
            {deleteProducts.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", marginBottom: 8 }}>
                {deleteProducts.error instanceof ApiError ? deleteProducts.error.message : "Could not delete these products."}
              </p>
            ) : null}
            {restoreProducts.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", marginBottom: 8 }}>
                {restoreProducts.error instanceof ApiError ? restoreProducts.error.message : "Could not restore these products."}
              </p>
            ) : null}
            <div className={ui.tableScroll}>
              <table className={ui.table}>
                <thead>
                  <tr>
                    <th scope="col">
                      <input
                        type="checkbox"
                        aria-label="Select all products"
                        checked={selectedIds.size > 0 && selectedIds.size === products.data.length}
                        onChange={(e) => setSelectedIds(e.target.checked ? new Set(products.data.map((p) => p.ID)) : new Set())}
                      />
                    </th>
                    <th scope="col">Name</th>
                    <th scope="col">HSN/SAC</th>
                    <th scope="col">Price</th>
                    <th scope="col">Category</th>
                    <th scope="col">Brand</th>
                    <th scope="col">Status</th>
                    <th scope="col" />
                  </tr>
                </thead>
                <tbody>
                  {products.data.map((p) => {
                    const priceItem = p.DefaultVariantID ? priceByVariantId.get(p.DefaultVariantID) : undefined;
                    return (
                      <tr key={p.ID} style={p.Status === "INACTIVE" ? { opacity: 0.6 } : undefined}>
                        <td>
                          <input type="checkbox" aria-label={`Select ${p.Name}`} checked={selectedIds.has(p.ID)} onChange={() => toggleSelected(p.ID)} />
                        </td>
                        <td>{p.Name}</td>
                        <td>{p.HSNSACCode}</td>
                        <td>{priceItem ? formatMoney(priceItem.Price) : <span className={ui.muted}>Not set</span>}</td>
                        <td>{p.CategoryID ? (categoryNameById.get(p.CategoryID) ?? "—") : "—"}</td>
                        <td>{p.BrandID ? (brandNameById.get(p.BrandID) ?? "—") : "—"}</td>
                        <td>
                          <span className={ui.badge} data-tone={p.Status === "ACTIVE" ? "positive" : "neutral"}>
                            {p.Status}
                          </span>
                        </td>
                        <td>
                          <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
                            <button type="button" className={ui.btnGhost} onClick={() => startEdit(p)}>
                              Edit
                            </button>
                            {p.Status === "ACTIVE" ? (
                              confirmingDeleteId === p.ID ? (
                                <>
                                  <button type="button" className={ui.btnGhost} disabled={deleteProducts.isPending} onClick={() => deleteProducts.mutate([p.ID])}>
                                    Confirm
                                  </button>
                                  <button type="button" className={ui.btnGhost} onClick={() => setConfirmingDeleteId(null)}>
                                    Cancel
                                  </button>
                                </>
                              ) : (
                                <button type="button" className={ui.btnGhost} onClick={() => setConfirmingDeleteId(p.ID)}>
                                  Delete
                                </button>
                              )
                            ) : (
                              <button type="button" className={ui.btnGhost} disabled={restoreProducts.isPending} onClick={() => restoreProducts.mutate([p.ID])}>
                                Restore
                              </button>
                            )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
