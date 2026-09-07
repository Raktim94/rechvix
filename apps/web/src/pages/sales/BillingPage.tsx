import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";
import styles from "./BillingPage.module.css";
import { DOCUMENT_TYPE_LABELS, EWB_ELIGIBLE_TYPES, type DocumentType, type SalesDocument, type SalesDocumentLine } from "./types";

interface BillingLookupResult {
  ProductID: string;
  ProductName: string;
  HSNSACCode: string;
  ProductVariantID: string;
  SKUCode: string;
  QuantityOnHand: string;
  QuantityAvailable: string;
  UnitPrice: { amount: string; currency: string } | null;
}

interface AgeingBucket {
  Total: { amount: string; currency: string };
}

interface PriceList {
  ID: string;
  Name: string;
  CurrencyCode: string;
  IsDefault: boolean;
}

/** The billing counter — brief's "exceptional attention" screen. A sale is
 * a real DRAFT sales_documents row from the moment the customer is picked
 * (not client-side-only state until some later "save"): every add-line
 * call is a real, immediately-persisted API call. That is what makes
 * "hold" free — navigating away just leaves a DRAFT sitting in the Sales
 * list, and "restore" is just opening that same document again
 * (SalesDetailPage's "Continue billing" button routes back here with the
 * existing id).
 */
export function BillingPage({ resumeDocumentId }: { resumeDocumentId?: string }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const org = useOrgContext();

  const [documentId, setDocumentId] = useState<string | undefined>(resumeDocumentId);
  const [documentType, setDocumentType] = useState<DocumentType>("TAX_INVOICE");
  const [customerQuery, setCustomerQuery] = useState("");
  const [customer, setCustomer] = useState<Party | null>(null);
  const [showCustomerResults, setShowCustomerResults] = useState(false);

  const [productQuery, setProductQuery] = useState("");
  const [productResults, setProductResults] = useState<BillingLookupResult[]>([]);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const customerSearchRef = useRef<HTMLInputElement>(null);
  const [priceListId, setPriceListId] = useState<string>("");

  const priceLists = useQuery({
    queryKey: ["price-lists"],
    queryFn: () => api.getListField<PriceList>("/pricing/price-lists", "price_lists"),
  });

  // Default to the org's default price list (or its only one) the first
  // time the list loads, but leave the cashier's own choice alone after
  // that — this effect only ever fires while priceListId is still unset.
  useEffect(() => {
    if (priceListId || !priceLists.data || priceLists.data.length === 0) return;
    const def = priceLists.data.find((pl) => pl.IsDefault) ?? priceLists.data[0];
    if (def) setPriceListId(def.ID);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [priceLists.data]);

  useEffect(() => {
    if (!showCustomerResults) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setShowCustomerResults(false);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [showCustomerResults]);

  const doc = useQuery({
    queryKey: ["sales-document", documentId],
    queryFn: () => api.get<{ document: SalesDocument; lines: SalesDocumentLine[] }>(`/sales/documents/${documentId}`),
    enabled: !!documentId,
  });

  // Resuming a held draft: hydrate the customer field from the loaded
  // document once, so the header reads correctly without a second lookup
  // UI — the customer picker above is naturally disabled once a document
  // exists (see below). Fetches the real party record (not just its ID)
  // so the header shows the customer's name, not a raw UUID.
  useEffect(() => {
    if (!resumeDocumentId || !doc.data || customer) return;
    const partyId = doc.data.document.CustomerPartyID;
    api
      .get<Party>(`/contacts/parties/${partyId}`)
      .then(setCustomer)
      .catch(() => setCustomer({ ID: partyId } as Party));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doc.data]);

  const customerSearch = useQuery({
    queryKey: ["party-search", customerQuery],
    queryFn: () => api.getListField<Party>(`/contacts/parties?q=${encodeURIComponent(customerQuery)}`, "parties"),
    enabled: customerQuery.length >= 2 && !documentId,
  });

  const customerAgeing = useQuery({
    queryKey: ["party-ageing", customer?.ID],
    queryFn: () => api.get<AgeingBucket>(`/accounting/parties/${customer?.ID}/ageing`),
    enabled: !!customer && customer.ID !== resumeDocumentId,
  });

  useEffect(() => {
    if (productQuery.trim().length < 2 || !documentId) {
      setProductResults([]);
      return;
    }
    const handle = setTimeout(() => {
      const params = new URLSearchParams({ q: productQuery });
      if (org.warehouse) params.set("warehouse_id", org.warehouse.ID);
      if (priceListId) params.set("price_list_id", priceListId);
      api
        .get<{ results: BillingLookupResult[] | null }>(`/sales/billing-lookup?${params.toString()}`)
        .then((res) => setProductResults(res.results ?? []))
        .catch(() => setProductResults([]));
    }, 150);
    return () => clearTimeout(handle);
  }, [productQuery, documentId, org.warehouse, priceListId]);

  const startSale = useMutation({
    mutationFn: async () => {
      if (!customer || !org.legalEntity || !org.branch || !org.warehouse || !org.organisation) {
        throw new Error("Missing organisation context.");
      }
      return api.post<SalesDocument>("/sales/documents", {
        legal_entity_id: org.legalEntity.ID,
        branch_id: org.branch.ID,
        warehouse_id: org.warehouse.ID,
        customer_party_id: customer.ID,
        document_type: documentType,
        place_of_supply_state_code: org.legalEntity.GSTStateCode || "00",
        currency_code: org.organisation.DefaultCurrencyCode || "INR",
        base_currency_code: org.organisation.DefaultCurrencyCode || "INR",
        exchange_rate: "1",
        pricing_mode: "EXCLUSIVE",
      });
    },
    onSuccess: (d) => {
      setDocumentId(d.ID);
      setTimeout(() => searchInputRef.current?.focus(), 0);
    },
  });

  const addLine = useMutation({
    mutationFn: async (vars: { productVariantId: string; unitId: string; quantity: string; unitPrice: string }) =>
      api.post(`/sales/documents/${documentId}/lines`, {
        product_variant_id: vars.productVariantId,
        unit_id: vars.unitId,
        quantity: vars.quantity,
        unit_price: vars.unitPrice,
        line_discount_amount: "0",
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["sales-document", documentId] });
      setProductQuery("");
      setProductResults([]);
      searchInputRef.current?.focus();
    },
  });

  const finalize = useMutation({
    mutationFn: () => api.post<SalesDocument>(`/sales/documents/${documentId}/finalize`),
    onSuccess: (d) => navigate({ to: "/sales/$id", params: { id: d.ID } }),
  });

  async function handleAddProduct(result: BillingLookupResult) {
    let unitId: string;
    try {
      const product = await api.get<{ ID: string; BaseUOMID: string }>(`/catalogue/products/${result.ProductID}`);
      unitId = product.BaseUOMID;
    } catch {
      return;
    }
    addLine.mutate({
      productVariantId: result.ProductVariantID,
      unitId,
      quantity: "1",
      unitPrice: result.UnitPrice?.amount ?? "0",
    });
  }

  const lines = doc.data?.lines ?? [];
  const grandTotal = doc.data?.document.GrandTotalAmount;

  // Counter shortcuts (brief §18): F2 product search, F3 customer search,
  // Ctrl+Enter finalize — the three real actions this screen actually
  // has, rather than mapping the brief's full F1-F9 list onto controls
  // that don't exist here. Each ref-focus is a safe no-op when that
  // field isn't currently mounted (e.g. F2 before a document/customer
  // exists yet), so no extra guard condition is needed beyond that.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "F2") {
        e.preventDefault();
        searchInputRef.current?.focus();
      } else if (e.key === "F3") {
        e.preventDefault();
        customerSearchRef.current?.focus();
      } else if (e.ctrlKey && e.key === "Enter") {
        e.preventDefault();
        if (lines.length > 0 && !finalize.isPending) finalize.mutate();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [lines.length, finalize]);

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>{resumeDocumentId ? "Continue sale" : "New sale"}</h1>
          <p className={layout.subtitle}>Scan a barcode or search by product name — stock and price show instantly.</p>
          <p className={ui.muted} style={{ marginTop: 2 }}>
            <kbd>F2</kbd> search product · <kbd>F3</kbd> search customer · <kbd>Ctrl</kbd>+<kbd>Enter</kbd> finalize
          </p>
        </div>
      </div>

      <div className={layout.panel}>
        <div className={styles.headerGrid}>
          <div className={ui.field}>
            <label htmlFor="doc-type">Document type</label>
            <select
              id="doc-type"
              className={ui.select}
              value={documentType}
              disabled={!!documentId}
              onChange={(e) => setDocumentType(e.target.value as DocumentType)}
            >
              {(["TAX_INVOICE", "POS_INVOICE", "QUOTATION", "SALES_ORDER"] as DocumentType[]).map((t) => (
                <option key={t} value={t}>
                  {DOCUMENT_TYPE_LABELS[t]}
                </option>
              ))}
            </select>
          </div>

          <div className={ui.field}>
            <label htmlFor="price-list-select">Price list</label>
            {priceLists.data && priceLists.data.length > 0 ? (
              <select id="price-list-select" className={ui.select} value={priceListId} onChange={(e) => setPriceListId(e.target.value)}>
                {priceLists.data.map((pl) => (
                  <option key={pl.ID} value={pl.ID}>
                    {pl.Name}
                  </option>
                ))}
              </select>
            ) : (
              <Link to="/pricing" className={ui.btnSecondary}>
                Set up pricing
              </Link>
            )}
          </div>

          <div className={`${ui.field} ${styles.customerField}`}>
            <label htmlFor="customer-search">Customer</label>
            {customer && documentId ? (
              <div className={styles.customerBadge}>
                <strong>{customer.LegalName || customer.ID}</strong>
              </div>
            ) : customer ? (
              <div className={styles.customerBadge}>
                <strong>{customer.LegalName}</strong>
                {customerAgeing.data ? (
                  <span className={styles.customerBalance}>
                    Outstanding: {formatMoney(customerAgeing.data.Total)}
                    {customer.CreditLimitAmount ? ` · Credit limit: ₹${customer.CreditLimitAmount}` : ""}
                  </span>
                ) : null}
                <button type="button" className={ui.btnSecondary} onClick={() => setCustomer(null)}>
                  Change
                </button>
              </div>
            ) : (
              <div className={styles.customerSearchWrap}>
                <input
                  id="customer-search"
                  ref={customerSearchRef}
                  className={ui.input}
                  placeholder="Search customer by name or phone…"
                  value={customerQuery}
                  onChange={(e) => {
                    setCustomerQuery(e.target.value);
                    setShowCustomerResults(true);
                  }}
                  onFocus={() => setShowCustomerResults(true)}
                  autoComplete="off"
                />
                {showCustomerResults && customerSearch.data?.length ? (
                  <ul className={styles.dropdown} role="menu" aria-label="Customer results">
                    {customerSearch.data.map((p) => (
                      <li key={p.ID}>
                        <button
                          type="button"
                          role="menuitem"
                          className={styles.dropdownItem}
                          onClick={() => {
                            setCustomer(p);
                            setShowCustomerResults(false);
                          }}
                        >
                          {p.LegalName} {p.Phone ? <span className={ui.muted}>· {p.Phone}</span> : null}
                        </button>
                      </li>
                    ))}
                  </ul>
                ) : null}
              </div>
            )}
          </div>

          {!documentId ? (
            <button
              type="button"
              className={ui.btnPrimary}
              disabled={!customer || org.isPending || startSale.isPending}
              onClick={() => startSale.mutate()}
            >
              {startSale.isPending ? "Starting…" : "Start sale"}
            </button>
          ) : null}
        </div>
        {startSale.isError ? (
          <p className={styles.errorText} role="alert">
            {startSale.error instanceof ApiError ? startSale.error.message : "Could not start this sale."}
          </p>
        ) : null}
        {org.isError ? (
          <p className={styles.errorText} role="alert">
            Could not load your branch/warehouse setup. Check Settings.
          </p>
        ) : null}
      </div>

      {documentId ? (
        <>
          <div className={layout.panel}>
            <label htmlFor="product-search" className={styles.searchLabel}>
              Search or scan a product
            </label>
            <input
              id="product-search"
              ref={searchInputRef}
              className={ui.input}
              placeholder="Type a product name, or scan a barcode…"
              value={productQuery}
              onChange={(e) => setProductQuery(e.target.value)}
              autoComplete="off"
            />
            {productResults.length > 0 ? (
              <ul className={styles.productList}>
                {productResults.map((r) => (
                  <li key={r.ProductVariantID} className={styles.productRow}>
                    <div>
                      <strong>{r.ProductName}</strong>
                      <div className={ui.muted}>
                        SKU {r.SKUCode} · In stock: {r.QuantityAvailable || "0"}
                      </div>
                    </div>
                    <div className={styles.productPrice}>{r.UnitPrice ? formatMoney(r.UnitPrice) : "—"}</div>
                    <button
                      type="button"
                      className={ui.btnPrimary}
                      disabled={addLine.isPending}
                      onClick={() => handleAddProduct(r)}
                    >
                      Add
                    </button>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>

          <div className={layout.panel}>
            <h2>Items ({lines.length})</h2>
            {lines.length === 0 ? (
              <p className={layout.emptyState}>No items yet — search above to add the first one.</p>
            ) : (
              <div className={ui.tableScroll}>
                <table className={ui.table}>
                  <thead>
                    <tr>
                      <th scope="col">#</th>
                      <th scope="col">HSN/SAC</th>
                      <th scope="col">Qty</th>
                      <th scope="col">Rate</th>
                      <th scope="col">Total</th>
                    </tr>
                  </thead>
                  <tbody>
                    {lines.map((l) => (
                      <tr key={l.ID}>
                        <td className="num">{l.LineNumber}</td>
                        <td>{l.HSNSACCode}</td>
                        <td className="num">{l.Quantity}</td>
                        <td className="num">{formatMoney(l.UnitPrice)}</td>
                        <td className="num">{formatMoney(l.LineTotal)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <div className={styles.totalRow}>
              <span>Grand total</span>
              <span className="num">{grandTotal ? formatMoney(grandTotal) : "—"}</span>
            </div>
            <div className={ui.formActions}>
              <button type="button" className={ui.btnSecondary} onClick={() => navigate({ to: "/sales" })}>
                Hold for later
              </button>
              <button
                type="button"
                className={ui.btnPrimary}
                disabled={lines.length === 0 || finalize.isPending}
                onClick={() => finalize.mutate()}
              >
                {finalize.isPending
                  ? "Saving…"
                  : EWB_ELIGIBLE_TYPES.has(documentType)
                    ? "Save & continue to e-Way Bill"
                    : "Finalize sale"}
              </button>
            </div>
            {finalize.isError ? (
              <p className={styles.errorText} role="alert">
                {finalize.error instanceof ApiError ? finalize.error.message : "Could not finalize this sale."}
              </p>
            ) : null}
          </div>
        </>
      ) : null}
    </div>
  );
}
