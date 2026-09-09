import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ImportPanel } from "../../components/ImportPanel";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";

interface Product {
  ID: string;
  Name: string;
  HSNSACCode: string;
  BaseUOMID: string;
  CategoryID: string | null;
  BrandID: string | null;
  Status: "ACTIVE" | "INACTIVE";
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
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);
  const [confirmingBulkDelete, setConfirmingBulkDelete] = useState(false);
  const [name, setName] = useState("");
  const [hsn, setHsn] = useState("");
  const [unitId, setUnitId] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("");
  const [gstRate, setGstRate] = useState("");
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

  const createUnit = useMutation({
    mutationFn: () => api.post<Unit>("/catalogue/units", { code: newUnitCode, name: newUnitName }),
    onSuccess: (u) => {
      void queryClient.invalidateQueries({ queryKey: ["units"] });
      setUnitId(u.ID);
      setNewUnitCode("");
      setNewUnitName("");
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
      setName("");
      setHsn("");
      setSkuCode("");
      setCategoryId("");
      setBrandId("");
      setGstRate("");
      setOpeningQty("");
      setOpeningCost("");
      setBarcode("");
      setShowForm(false);
    },
  });

  const updateProduct = useMutation({
    mutationFn: () =>
      api.put<Product>(`/catalogue/products/${editingId}`, {
        category_id: categoryId || null,
        brand_id: brandId || null,
        base_uom_id: unitId,
        name,
        description: "",
        hsn_sac_code: hsn,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      closeForm();
    },
  });

  function closeForm() {
    setShowForm(false);
    setEditingId(null);
    setName("");
    setHsn("");
    setSkuCode("");
    setCategoryId("");
    setBrandId("");
    setGstRate("");
    setOpeningQty("");
    setOpeningCost("");
    setBarcode("");
  }

  function startEdit(p: Product) {
    setEditingId(p.ID);
    setName(p.Name);
    setHsn(p.HSNSACCode);
    setUnitId(p.BaseUOMID);
    setCategoryId(p.CategoryID ?? "");
    setBrandId(p.BrandID ?? "");
    setShowForm(true);
  }

  // Products are never hard-deleted — "Delete" flips Status to INACTIVE
  // (see catalogue.domain.ProductRepository.SetStatus's doc comment) so
  // historical sales/purchase lines and stock movements keep resolving.
  // "Restore" flips it back.
  const setStatus = useMutation({
    mutationFn: ({ id, status }: { id: string; status: "ACTIVE" | "INACTIVE" }) =>
      status === "INACTIVE" ? api.delete(`/catalogue/products/${id}`) : api.post(`/catalogue/products/${id}/restore`, {}),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      setConfirmingDeleteId(null);
    },
  });

  const bulkDelete = useMutation({
    mutationFn: (ids: string[]) => api.post("/catalogue/products/bulk-delete", { ids }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["products"] });
      setSelectedIds(new Set());
      setConfirmingBulkDelete(false);
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
              setName("");
              setHsn("");
              setSkuCode("");
              setCategoryId("");
              setBrandId("");
              setGstRate("");
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

      {showForm ? (
        <div className={layout.panel}>
          <h2 style={{ marginTop: 0 }}>{editingId ? "Edit product" : "New product"}</h2>
          {units.data && units.data.length === 0 ? (
            <div className={ui.formGrid} style={{ marginBottom: 16 }}>
              <div className={ui.field}>
                <label htmlFor="new-unit-code">First, add a unit of measure — code (e.g. PCS)</label>
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
              <label htmlFor="product-unit">Unit</label>
              <select id="product-unit" className={ui.select} value={unitId} onChange={(e) => setUnitId(e.target.value)}>
                <option value="">Select a unit…</option>
                {units.data?.map((u) => (
                  <option key={u.ID} value={u.ID}>
                    {u.Name} ({u.Code})
                  </option>
                ))}
              </select>
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
        columns={["name", "hsn_sac_code (optional)", "base_uom_code", "sku_code (optional — generated from name if blank)"]}
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
            {selectedIds.size > 0 ? (
              <div className={ui.toolbar} style={{ marginBottom: 8, gap: 8 }}>
                <span className={ui.muted}>{selectedIds.size} selected</span>
                {confirmingBulkDelete ? (
                  <>
                    <span>Delete {selectedIds.size} product(s)?</span>
                    <button type="button" className={ui.btnDanger} disabled={bulkDelete.isPending} onClick={() => bulkDelete.mutate([...selectedIds])}>
                      {bulkDelete.isPending ? "Deleting…" : "Confirm delete"}
                    </button>
                    <button type="button" className={ui.btnGhost} onClick={() => setConfirmingBulkDelete(false)}>
                      Cancel
                    </button>
                  </>
                ) : (
                  <button type="button" className={ui.btnSecondary} onClick={() => setConfirmingBulkDelete(true)}>
                    Delete {selectedIds.size} product(s)
                  </button>
                )}
                <button type="button" className={ui.btnGhost} onClick={() => setSelectedIds(new Set())}>
                  Clear selection
                </button>
              </div>
            ) : null}
            {bulkDelete.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", marginBottom: 8 }}>
                {bulkDelete.error instanceof ApiError ? bulkDelete.error.message : "Could not delete these products."}
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
                    <th scope="col">Category</th>
                    <th scope="col">Brand</th>
                    <th scope="col">Status</th>
                    <th scope="col" />
                  </tr>
                </thead>
                <tbody>
                  {products.data.map((p) => (
                    <tr key={p.ID} style={p.Status === "INACTIVE" ? { opacity: 0.6 } : undefined}>
                      <td>
                        <input type="checkbox" aria-label={`Select ${p.Name}`} checked={selectedIds.has(p.ID)} onChange={() => toggleSelected(p.ID)} />
                      </td>
                      <td>{p.Name}</td>
                      <td>{p.HSNSACCode}</td>
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
                                <button
                                  type="button"
                                  className={ui.btnGhost}
                                  disabled={setStatus.isPending}
                                  onClick={() => setStatus.mutate({ id: p.ID, status: "INACTIVE" })}
                                >
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
                            <button type="button" className={ui.btnGhost} disabled={setStatus.isPending} onClick={() => setStatus.mutate({ id: p.ID, status: "ACTIVE" })}>
                              Restore
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
