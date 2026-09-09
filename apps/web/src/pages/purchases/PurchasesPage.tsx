import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { PaymentPanel } from "../../components/PaymentPanel";
import { QuickAddPartyModal } from "../../components/QuickAddPartyModal";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { parseBillText, type ParsedBill } from "../../lib/billParser";
import { formatMoney } from "../../lib/money";
import { getOcrProvider, runOcr } from "../../lib/ocr";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";
import { CancelPurchaseModal } from "./CancelPurchaseModal";
import { ImportAiMarkdownButton } from "./ImportAiMarkdownButton";
import { PurchaseScanReviewModal, type ResolvedScanLine } from "./PurchaseScanReviewModal";

type PurchaseStatus = "DRAFT" | "FINALIZED" | "CANCELLED";
type StatusFilter = "ALL" | PurchaseStatus;

interface PurchaseDocument {
  ID: string;
  DocumentNumber: string;
  Status: PurchaseStatus;
  DocumentType: string;
  DocumentDate: string;
  SupplierPartyID: string;
}
interface PurchaseLine {
  ID: string;
  LineNumber: number;
  Quantity: string;
  UnitPrice: { amount: string; currency: string };
  LineTotal: { amount: string; currency: string };
}
interface Product {
  ID: string;
  Name: string;
  BaseUOMID: string;
}
interface ProductVariant {
  ID: string;
  SKUCode: string;
}

export function PurchasesPage() {
  const queryClient = useQueryClient();
  const org = useOrgContext();
  const [creating, setCreating] = useState(false);
  const [activeDocId, setActiveDocId] = useState<string | null>(null);
  const [supplierQuery, setSupplierQuery] = useState("");
  const [supplier, setSupplier] = useState<Party | null>(null);
  const [quickAddSupplierOpen, setQuickAddSupplierOpen] = useState(false);
  const [productQuery, setProductQuery] = useState("");
  const [productResults, setProductResults] = useState<Product[]>([]);
  const [qty, setQty] = useState("1");
  const [price, setPrice] = useState("0");
  const [listQuery, setListQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("ALL");
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");

  const scanInputRef = useRef<HTMLInputElement>(null);
  const [scanning, setScanning] = useState(false);
  const [scanProgress, setScanProgress] = useState(0);
  const [scanError, setScanError] = useState<string | null>(null);
  const [parsedBill, setParsedBill] = useState<ParsedBill | null>(null);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [cancelModalOpen, setCancelModalOpen] = useState(false);

  const documents = useQuery({
    queryKey: ["purchase-documents"],
    queryFn: () => api.getListField<PurchaseDocument>("/purchases/documents", "documents"),
  });

  // Only used to resolve SupplierPartyID -> a display name in the list
  // below — the purchase document itself doesn't carry the supplier's
  // name, only its id. Fine at the scale this screen targets (see
  // ContactsPage's own unpaginated party list for the same assumption).
  const suppliers = useQuery({
    queryKey: ["parties"],
    queryFn: () => api.getListField<Party>("/contacts/parties", "parties"),
  });
  const supplierNameById = new Map(suppliers.data?.map((p) => [p.ID, p.LegalName]));

  const lq = listQuery.trim().toLowerCase();
  const filteredDocuments = (documents.data ?? []).filter((d) => {
    if (statusFilter !== "ALL" && d.Status !== statusFilter) return false;
    if (fromDate && d.DocumentDate.slice(0, 10) < fromDate) return false;
    if (toDate && d.DocumentDate.slice(0, 10) > toDate) return false;
    if (lq) {
      const supplierName = (supplierNameById.get(d.SupplierPartyID) ?? "").toLowerCase();
      if (!d.DocumentNumber.toLowerCase().includes(lq) && !supplierName.includes(lq)) return false;
    }
    return true;
  });

  const activeDoc = useQuery({
    queryKey: ["purchase-document", activeDocId],
    queryFn: () => api.get<{ document: PurchaseDocument; lines: PurchaseLine[] }>(`/purchases/documents/${activeDocId}`),
    enabled: !!activeDocId,
  });

  const supplierSearch = useQuery({
    queryKey: ["supplier-search", supplierQuery],
    queryFn: () => api.getListField<Party>(`/contacts/parties?q=${encodeURIComponent(supplierQuery)}`, "parties"),
    enabled: supplierQuery.length >= 2,
  });

  const startPurchase = useMutation({
    mutationFn: () => {
      if (!supplier || !org.branch || !org.warehouse) throw new Error("Missing organisation context.");
      return api.post<PurchaseDocument>("/purchases/documents", {
        branch_id: org.branch.ID,
        warehouse_id: org.warehouse.ID,
        supplier_party_id: supplier.ID,
        document_type: "PURCHASE_INVOICE",
        currency_code: org.organisation?.DefaultCurrencyCode || "INR",
        notes: "",
      });
    },
    onSuccess: (d) => {
      setActiveDocId(d.ID);
      queryClient.invalidateQueries({ queryKey: ["purchase-documents"] });
    },
  });

  async function handleScanFile(file: File) {
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

  // Everything the review modal decided (which supplier, which lines at
  // what price) lands here as plain data — this is the one place that
  // actually calls the same /purchases/documents(+/lines) endpoints
  // startPurchase/addLine use below, just supplier-first instead of
  // one-field-at-a-time, since the modal already resolved every field.
  const commitScan = useMutation({
    mutationFn: async (result: { supplierId: string; lines: ResolvedScanLine[] }) => {
      if (!org.branch || !org.warehouse) throw new Error("Missing organisation context.");
      const doc = await api.post<PurchaseDocument>("/purchases/documents", {
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
    onSuccess: (doc) => {
      queryClient.invalidateQueries({ queryKey: ["purchase-documents"] });
      setReviewOpen(false);
      setParsedBill(null);
      setCreating(false);
      setActiveDocId(doc.ID);
    },
    onError: (err) => {
      setScanError(err instanceof ApiError ? err.message : "Could not create the purchase from this scan.");
    },
  });

  async function searchProducts(q: string) {
    setProductQuery(q);
    if (q.trim().length < 2) {
      setProductResults([]);
      return;
    }
    const res = await api.get<{ products: Product[] | null }>(`/catalogue/products?q=${encodeURIComponent(q)}`);
    setProductResults(res.products ?? []);
  }

  const addLine = useMutation({
    mutationFn: async (product: Product) => {
      const variants = await api.getListField<ProductVariant>(`/catalogue/products/${product.ID}/variants`, "variants");
      const variant = variants[0];
      if (!variant) throw new Error("This product has no variant yet.");
      return api.post(`/purchases/documents/${activeDocId}/lines`, {
        product_variant_id: variant.ID,
        unit_id: product.BaseUOMID,
        quantity: qty,
        unit_price: price,
        batch_code: "",
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["purchase-document", activeDocId] });
      setProductQuery("");
      setProductResults([]);
      setQty("1");
      setPrice("0");
    },
  });

  const finalize = useMutation({
    mutationFn: () => api.post(`/purchases/documents/${activeDocId}/finalize`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["purchase-document", activeDocId] });
      queryClient.invalidateQueries({ queryKey: ["purchase-documents"] });
    },
  });

  if (creating || activeDocId) {
    const lines = activeDoc.data?.lines ?? [];
    const finalized = activeDoc.data?.document.Status === "FINALIZED";
    const cancelled = activeDoc.data?.document.Status === "CANCELLED";
    // Editable state — a document is only still a work-in-progress DRAFT
    // when it's neither finalized nor cancelled. `!finalized` alone used
    // to also mean "draft" back when CANCELLED was unreachable; now that
    // CancelDocument can actually produce one, a cancelled document must
    // not fall back into the add-line/finalize form.
    const editable = !finalized && !cancelled;
    return (
      <div className={layout.page}>
        <div className={layout.heading}>
          <div>
            <h1>New purchase</h1>
            <p className={layout.subtitle}>Record what you bought from a supplier.</p>
          </div>
          <button
            type="button"
            className={ui.btnSecondary}
            onClick={() => {
              setCreating(false);
              setActiveDocId(null);
              setSupplier(null);
            }}
          >
            Back to list
          </button>
        </div>

        {!activeDocId ? (
          <div className={layout.panel}>
            <div className={ui.field} style={{ maxWidth: 360, position: "relative" }}>
              <label htmlFor="supplier-search">Supplier</label>
              {supplier ? (
                <div>
                  <strong>{supplier.LegalName}</strong>{" "}
                  <button type="button" className={ui.btnSecondary} onClick={() => setSupplier(null)}>
                    Change
                  </button>
                </div>
              ) : (
                <>
                  <div style={{ display: "flex", gap: 8 }}>
                    <input
                      id="supplier-search"
                      className={ui.input}
                      value={supplierQuery}
                      onChange={(e) => setSupplierQuery(e.target.value)}
                      placeholder="Search supplier…"
                      style={{ flex: 1 }}
                    />
                    <button type="button" className={ui.btnSecondary} onClick={() => setQuickAddSupplierOpen(true)}>
                      + New
                    </button>
                  </div>
                  {supplierQuery.trim().length >= 2 ? (
                    <ul style={{ listStyle: "none", margin: "8px 0 0", padding: 0 }}>
                      {(supplierSearch.data ?? []).map((p) => (
                        <li key={p.ID}>
                          <button type="button" className={ui.btnSecondary} style={{ margin: "0 4px 4px 0" }} onClick={() => setSupplier(p)}>
                            {p.LegalName}
                          </button>
                        </li>
                      ))}
                      <li>
                        <button type="button" className={ui.btnSecondary} style={{ margin: "0 4px 4px 0", color: "var(--color-accent)" }} onClick={() => setQuickAddSupplierOpen(true)}>
                          + New supplier "{supplierQuery.trim()}"
                        </button>
                      </li>
                    </ul>
                  ) : null}
                </>
              )}
            </div>
            <QuickAddPartyModal
              open={quickAddSupplierOpen}
              onOpenChange={setQuickAddSupplierOpen}
              partyType="SUPPLIER"
              currencyCode={org.organisation?.DefaultCurrencyCode || "INR"}
              initialLegalName={supplierQuery.trim()}
              onCreated={(party) => {
                setSupplier(party);
                setSupplierQuery("");
              }}
            />
            <div className={ui.formActions} style={{ marginTop: 12 }}>
              <button type="button" className={ui.btnPrimary} disabled={!supplier || startPurchase.isPending} onClick={() => startPurchase.mutate()}>
                Start purchase
              </button>
            </div>
            {startPurchase.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)" }}>
                {startPurchase.error instanceof ApiError ? startPurchase.error.message : "Could not start this purchase."}
              </p>
            ) : null}
          </div>
        ) : (
          <>
            {editable ? (
              <div className={layout.panel}>
                <div className={ui.formGrid}>
                  <div className={ui.field} style={{ gridColumn: "span 2" }}>
                    <label htmlFor="purchase-product-search">Product</label>
                    <input
                      id="purchase-product-search"
                      className={ui.input}
                      value={productQuery}
                      onChange={(e) => void searchProducts(e.target.value)}
                    />
                  </div>
                  <div className={ui.field}>
                    <label htmlFor="purchase-qty">Quantity</label>
                    <input id="purchase-qty" className={ui.input} value={qty} onChange={(e) => setQty(e.target.value)} />
                  </div>
                  <div className={ui.field}>
                    <label htmlFor="purchase-price">Unit cost</label>
                    <input id="purchase-price" className={ui.input} value={price} onChange={(e) => setPrice(e.target.value)} />
                  </div>
                </div>
                {productResults.length > 0 ? (
                  <ul style={{ listStyle: "none", margin: "8px 0 0", padding: 0 }}>
                    {productResults.map((p) => (
                      <li key={p.ID}>
                        <button type="button" className={ui.btnPrimary} style={{ margin: "4px 4px 0 0" }} onClick={() => addLine.mutate(p)}>
                          Add {p.Name}
                        </button>
                      </li>
                    ))}
                  </ul>
                ) : null}
              </div>
            ) : null}

            <div className={layout.panel}>
              <h2>Items</h2>
              {lines.length === 0 ? (
                <p className={layout.emptyState}>No items yet.</p>
              ) : (
                <div className={ui.tableScroll}>
                  <table className={ui.table}>
                    <thead>
                      <tr>
                        <th scope="col">#</th>
                        <th scope="col">Qty</th>
                        <th scope="col">Cost</th>
                        <th scope="col">Total</th>
                      </tr>
                    </thead>
                    <tbody>
                      {lines.map((l) => (
                        <tr key={l.ID}>
                          <td className="num">{l.LineNumber}</td>
                          <td className="num">{l.Quantity}</td>
                          <td className="num">{formatMoney(l.UnitPrice)}</td>
                          <td className="num">{formatMoney(l.LineTotal)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {editable ? (
                <div className={ui.formActions} style={{ marginTop: 12 }}>
                  <button type="button" className={ui.btnPrimary} disabled={lines.length === 0 || finalize.isPending} onClick={() => finalize.mutate()}>
                    Finalize purchase
                  </button>
                </div>
              ) : cancelled ? (
                <p className={ui.badge} data-tone="negative" style={{ marginTop: 12 }}>
                  Cancelled
                </p>
              ) : (
                <div style={{ display: "flex", alignItems: "center", gap: 12, marginTop: 12 }}>
                  <p className={ui.badge} data-tone="positive" style={{ margin: 0 }}>
                    Finalized
                  </p>
                  <button type="button" className={ui.btnSecondary} onClick={() => setCancelModalOpen(true)}>
                    Cancel purchase
                  </button>
                </div>
              )}
            </div>

            {finalized && activeDoc.data ? (
              <PaymentPanel
                documentId={activeDoc.data.document.ID}
                partyId={activeDoc.data.document.SupplierPartyID}
                // Purchase documents carry no GrandTotalAmount of their
                // own (internal/modules/purchases/domain.Document has no
                // such field) — summed from lines client-side, same as
                // the total already shown per-line just above.
                grandTotal={
                  lines[0]
                    ? { amount: String(lines.reduce((sum, l) => sum + Number(l.LineTotal.amount), 0)), currency: lines[0].LineTotal.currency }
                    : null
                }
                direction="PAY"
              />
            ) : null}

            {activeDoc.data ? (
              <CancelPurchaseModal
                open={cancelModalOpen}
                onOpenChange={setCancelModalOpen}
                documentId={activeDoc.data.document.ID}
                documentNumber={activeDoc.data.document.DocumentNumber}
                currencyCode={lines[0]?.LineTotal.currency ?? org.organisation?.DefaultCurrencyCode ?? "INR"}
              />
            ) : null}
          </>
        )}
      </div>
    );
  }

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Purchases</h1>
          <p className={layout.subtitle}>What you've bought from suppliers.</p>
        </div>
        <div style={{ display: "flex", gap: 8 }}>
          <input
            ref={scanInputRef}
            type="file"
            accept="image/*"
            capture="environment"
            style={{ display: "none" }}
            onChange={(e) => {
              const file = e.target.files?.[0];
              e.target.value = "";
              if (file) void handleScanFile(file);
            }}
          />
          <button type="button" className={ui.btnSecondary} disabled={scanning} onClick={() => scanInputRef.current?.click()}>
            {scanning ? `Scanning… ${Math.round(scanProgress * 100)}%` : "Scan bill"}
          </button>
          <ImportAiMarkdownButton />
          <button type="button" className={ui.btnPrimary} onClick={() => setCreating(true)}>
            + New purchase
          </button>
        </div>
      </div>
      {scanError ? (
        <p role="alert" style={{ color: "var(--color-negative)" }}>
          {scanError}
        </p>
      ) : null}
      <div className={layout.panel}>
        <div className={ui.toolbar} style={{ marginBottom: 12 }}>
          <input
            className={ui.input}
            placeholder="Search by number or supplier…"
            aria-label="Search purchases"
            value={listQuery}
            onChange={(e) => setListQuery(e.target.value)}
            style={{ maxWidth: 280 }}
          />
          <select className={ui.select} aria-label="Filter by status" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}>
            <option value="ALL">All statuses</option>
            <option value="DRAFT">Draft</option>
            <option value="FINALIZED">Finalized</option>
            <option value="CANCELLED">Cancelled</option>
          </select>
          <label className={ui.muted} style={{ display: "flex", alignItems: "center", gap: 6 }}>
            From
            <input type="date" className={ui.input} aria-label="From date" value={fromDate} onChange={(e) => setFromDate(e.target.value)} />
          </label>
          <label className={ui.muted} style={{ display: "flex", alignItems: "center", gap: 6 }}>
            To
            <input type="date" className={ui.input} aria-label="To date" value={toDate} onChange={(e) => setToDate(e.target.value)} />
          </label>
        </div>
        {documents.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load purchases.
          </p>
        ) : documents.isPending ? (
          <div className={layout.skeleton} style={{ height: 200 }} aria-hidden="true" />
        ) : filteredDocuments.length === 0 ? (
          <p className={layout.emptyState}>{documents.data.length === 0 ? "No purchases yet." : "No purchases match these filters."}</p>
        ) : (
          <div className={ui.tableScroll}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Number</th>
                  <th scope="col">Supplier</th>
                  <th scope="col">Type</th>
                  <th scope="col">Status</th>
                  <th scope="col">Date</th>
                </tr>
              </thead>
              <tbody>
                {filteredDocuments.map((d) => (
                  <tr key={d.ID}>
                    <td>
                      {/* A real <button> (SalesListPage's own row pattern
                          uses <Link> — this screen has no URL route per
                          document yet, so button is the equivalent
                          focusable/keyboard-operable control) instead of
                          the previous unfocusable `<tr onClick>`. */}
                      <button type="button" className={ui.linkRowButton} onClick={() => setActiveDocId(d.ID)}>
                        {d.DocumentNumber || "(draft)"}
                      </button>
                    </td>
                    <td>{supplierNameById.get(d.SupplierPartyID) ?? "—"}</td>
                    <td>{d.DocumentType}</td>
                    <td>
                      <span className={ui.badge} data-tone={d.Status === "FINALIZED" ? "positive" : d.Status === "CANCELLED" ? "negative" : "warning"}>
                        {d.Status}
                      </span>
                    </td>
                    <td>{new Date(d.DocumentDate).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <PurchaseScanReviewModal
        open={reviewOpen}
        onOpenChange={setReviewOpen}
        parsedBill={parsedBill}
        currencyCode={org.organisation?.DefaultCurrencyCode || "INR"}
        committing={commitScan.isPending}
        onCommitted={(result) => commitScan.mutate(result)}
      />
    </div>
  );
}
