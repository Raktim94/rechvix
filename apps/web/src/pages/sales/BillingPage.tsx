import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { QuickAddPartyModal } from "../../components/QuickAddPartyModal";
import { SearchIcon } from "../../components/icons";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";
import styles from "./BillingPage.module.css";
import { DOCUMENT_TYPE_LABELS, type DocumentType, type SalesDocument, type SalesDocumentLine } from "./types";

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

/** One cart line's quantity, editable in place — committed on blur or
 * Enter, reverted on Escape. Local, uncontrolled-feeling state so
 * typing "5" doesn't fight the server round-trip on every keystroke;
 * the line's real quantity (from the server) is what it resets to
 * whenever the row itself changes underneath it. */
function EditableQty({ line, onCommit, disabled }: { line: SalesDocumentLine; onCommit: (quantity: string) => void; disabled: boolean }) {
  const [value, setValue] = useState(line.Quantity);
  useEffect(() => setValue(line.Quantity), [line.Quantity, line.ID]);

  function commit() {
    const trimmed = value.trim();
    if (trimmed && trimmed !== line.Quantity && Number(trimmed) > 0) {
      onCommit(trimmed);
    } else {
      setValue(line.Quantity); // invalid/unchanged — snap back rather than leaving a bad value showing
    }
  }

  return (
    <input
      className={ui.input}
      style={{ width: 70, textAlign: "right" }}
      inputMode="decimal"
      value={value}
      disabled={disabled}
      onChange={(e) => setValue(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          (e.target as HTMLInputElement).blur();
        } else if (e.key === "Escape") {
          setValue(line.Quantity);
          (e.target as HTMLInputElement).blur();
        }
      }}
    />
  );
}

/** Same commit-on-blur/Enter, revert-on-Escape shape as EditableQty above,
 * for the one other per-line field the API already accepts on the same PUT
 * (line_discount_amount) but the table never exposed a way to actually
 * edit — a discount used to mean deleting the line and re-adding it at a
 * lower price, which just corrupts the "what was this item's real price"
 * record instead of recording an actual discount. Blank reads as "0", not
 * "unset" — there's no separate not-discounted state to preserve. */
function EditableDiscount({ line, onCommit, disabled }: { line: SalesDocumentLine; onCommit: (discount: string) => void; disabled: boolean }) {
  const [value, setValue] = useState(line.LineDiscountAmount.amount);
  useEffect(() => setValue(line.LineDiscountAmount.amount), [line.LineDiscountAmount.amount, line.ID]);

  function commit() {
    const trimmed = value.trim();
    const normalized = trimmed === "" ? "0" : trimmed;
    if (normalized !== line.LineDiscountAmount.amount && Number(normalized) >= 0) {
      onCommit(normalized);
    } else {
      setValue(line.LineDiscountAmount.amount);
    }
  }

  return (
    <input
      className={ui.input}
      style={{ width: 84, textAlign: "right" }}
      inputMode="decimal"
      placeholder="0"
      value={value === "0" ? "" : value}
      disabled={disabled}
      onFocus={(e) => e.target.select()}
      onChange={(e) => setValue(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          (e.target as HTMLInputElement).blur();
        } else if (e.key === "Escape") {
          setValue(line.LineDiscountAmount.amount);
          (e.target as HTMLInputElement).blur();
        }
      }}
    />
  );
}

/** Mirrors gstindia.httpapi's tax-rate list shape (same one GstPage's own
 * TaxRatesSection already trusts) — only the fields this per-line display
 * needs. */
interface HSNTaxRate {
  GSTRate: string;
}

/** A line's applicable GST% during billing — until now, nothing on this
 * screen showed what tax rate would apply; a line's real tax only became
 * visible after finalize, on the printed invoice. Looks the HSN/SAC code
 * up against GET /gst/tax-rates/{hsn} (the same admin-configured
 * tax_rate_master rows FinalizeDocument's real TaxEngine reads from,
 * already ordered newest-ValidFrom-first server-side — same "just take
 * the first row" simplicity GstPage's own TaxRatesSection already uses,
 * not stricter). Purely informational: FinalizeDocument's own
 * server-side calculation remains the actual source of truth for what
 * gets charged. */
function LineGstRate({ hsnSacCode }: { hsnSacCode: string }) {
  const trimmed = hsnSacCode.trim();
  const rates = useQuery({
    queryKey: ["gst-rate-for-hsn", trimmed],
    queryFn: () => api.getListField<HSNTaxRate>(`/gst/tax-rates/${encodeURIComponent(trimmed)}`, "tax_rates"),
    enabled: trimmed.length > 0,
    staleTime: 60_000,
  });
  if (!trimmed) return <span className={ui.muted}>—</span>;
  if (rates.isPending) return <span className={ui.muted}>…</span>;
  const current = rates.data?.[0];
  return current ? <span>{current.GSTRate}%</span> : <span className={ui.muted}>—</span>;
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

  const [quickAddOpen, setQuickAddOpen] = useState(false);
  const [productQuery, setProductQuery] = useState("");
  const [productResults, setProductResults] = useState<BillingLookupResult[]>([]);
  // A sales line carries only its ProductVariantID — there's no
  // variant-by-id lookup endpoint, so the cart could only ever show each
  // line's HSN/SAC code, i.e. a cashier mid-sale sees "84193200" where
  // the customer sees "Paper Bundle". Every item added here came from a
  // search result that DID carry the name, so remember it as we go.
  // Lines restored from a held draft (added in an earlier session) fall
  // back to the HSN code, same as before.
  const [nameByVariant, setNameByVariant] = useState<Record<string, string>>({});
  const searchInputRef = useRef<HTMLInputElement>(null);
  const customerSearchRef = useRef<HTMLInputElement>(null);

  // There's only ever one price list per organisation now (Pricing page
  // removed — price lives directly on the product, brief simplification).
  // ensure-default is idempotent: it creates the org's "Default" list on
  // first call and just returns it on every call after, so this is safe
  // to call from every billing session with no separate setup step.
  const defaultPriceList = useQuery({
    queryKey: ["default-price-list", org.organisation?.DefaultCurrencyCode],
    queryFn: () => api.post<{ ID: string }>("/pricing/price-lists/ensure-default", { currency_code: org.organisation?.DefaultCurrencyCode || "INR" }),
    enabled: !!org.organisation,
  });
  const priceListId = defaultPriceList.data?.ID ?? "";

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

  // Shared by the debounced-as-you-type search below AND the barcode-
  // scan Enter handler, which can't just read productResults state — a
  // scanner types its whole code and sends Enter fast enough that the
  // 150ms debounce below often hasn't resolved yet, so Enter needs its
  // own immediate, undebounced fetch rather than trusting whatever's
  // currently in state.
  async function fetchProductResults(query: string): Promise<BillingLookupResult[]> {
    const params = new URLSearchParams({ q: query });
    if (org.warehouse) params.set("warehouse_id", org.warehouse.ID);
    if (priceListId) params.set("price_list_id", priceListId);
    const res = await api.get<{ results: BillingLookupResult[] | null }>(`/sales/billing-lookup?${params.toString()}`);
    return res.results ?? [];
  }

  // No minimum query length: an empty productQuery still runs this (as
  // "" against the backend, which browses the whole active catalogue —
  // see SearchByName) so the panel below is a scrollable browse list from
  // the moment a sale starts, not just a search box. A shop with a few
  // hundred products can't rely on staff remembering exact names.
  useEffect(() => {
    if (!documentId) {
      setProductResults([]);
      return;
    }
    const handle = setTimeout(() => {
      fetchProductResults(productQuery)
        .then(setProductResults)
        .catch(() => setProductResults([]));
    }, 150);
    return () => clearTimeout(handle);
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  // Fixing a quantity or discount, or removing an item scanned by mistake,
  // used to mean abandoning the whole draft and starting over — there was
  // no line-update/delete endpoint at all. Both now exist; whichever of
  // quantity/discount isn't being changed is resent as-is so the other
  // never silently resets.
  const updateLine = useMutation({
    mutationFn: async (vars: { line: SalesDocumentLine; quantity?: string; discount?: string }) =>
      api.put(`/sales/documents/${documentId}/lines/${vars.line.ID}`, {
        quantity: vars.quantity ?? vars.line.Quantity,
        unit_price: vars.line.UnitPrice.amount,
        line_discount_amount: vars.discount ?? vars.line.LineDiscountAmount.amount,
      }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["sales-document", documentId] }),
  });

  const removeLine = useMutation({
    mutationFn: (lineId: string) => api.delete(`/sales/documents/${documentId}/lines/${lineId}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["sales-document", documentId] }),
  });

  // The org's own generic customer for a walk-in/cash sale that doesn't
  // warrant looking up or creating a real contact — reuses one shared
  // "Walk-in Customer" party across every such sale (found by exact
  // name, created once on first use) rather than the counter needing a
  // real customer picked for every single quick sale.
  const startWalkIn = useMutation({
    mutationFn: async () => {
      const matches = await api.getListField<Party>(`/contacts/parties?q=${encodeURIComponent("Walk-in Customer")}`, "parties");
      const existing = matches.find((p) => p.LegalName.toLowerCase() === "walk-in customer");
      if (existing) return existing;
      return api.post<Party>("/contacts/parties", {
        party_type: "CUSTOMER",
        legal_name: "Walk-in Customer",
        currency_code: org.organisation?.DefaultCurrencyCode || "INR",
      });
    },
    onSuccess: (party) => {
      setCustomer(party);
      setCustomerQuery("");
      setShowCustomerResults(false);
    },
  });

  const finalize = useMutation({
    mutationFn: () => api.post<SalesDocument>(`/sales/documents/${documentId}/finalize`),
    onSuccess: (d) => navigate({ to: "/sales/$id", params: { id: d.ID } }),
  });

  async function handleAddProduct(result: BillingLookupResult) {
    // Adding the same product a second time (a repeat "Add" click, or the
    // same barcode scanned twice) used to always POST a brand-new line —
    // two separate rows both reading quantity 1, instead of one row at
    // quantity 2. Merge into the existing line instead, same as any real
    // billing counter would.
    const existing = lines.find((l) => l.ProductVariantID === result.ProductVariantID);
    if (existing) {
      updateLine.mutate({ line: existing, quantity: String(Number(existing.Quantity) + 1) });
      setProductQuery("");
      setProductResults([]);
      searchInputRef.current?.focus();
      return;
    }
    let unitId: string;
    try {
      const product = await api.get<{ ID: string; BaseUOMID: string }>(`/catalogue/products/${result.ProductID}`);
      unitId = product.BaseUOMID;
    } catch {
      return;
    }
    setNameByVariant((cur) => ({ ...cur, [result.ProductVariantID]: result.ProductName }));
    addLine.mutate({
      productVariantId: result.ProductVariantID,
      unitId,
      quantity: "1",
      unitPrice: result.UnitPrice?.amount ?? "0",
    });
  }

  // A barcode scanner types the whole code then sends Enter itself —
  // waiting for the debounced dropdown and a manual "Add" click on
  // every single scan is exactly the friction a scanner is supposed to
  // remove. Enter re-fetches immediately (not trusting productResults,
  // which the 150ms debounce above may not have resolved yet) and adds
  // straight away when there's exactly one match — an ambiguous name
  // search with several results still falls through to the normal
  // dropdown-and-click flow rather than guessing which one was meant.
  const [scanning, setScanning] = useState(false);
  async function handleSearchEnter() {
    const query = productQuery.trim();
    if (query.length < 2 || scanning) return;
    setScanning(true);
    try {
      const results = await fetchProductResults(query);
      setProductResults(results);
      if (results.length === 1 && results[0]) {
        await handleAddProduct(results[0]);
      }
    } catch {
      // Leave whatever's already shown — same "don't blank the screen
      // on a transient failure" as the debounced search's own catch.
    } finally {
      setScanning(false);
    }
  }

  const lines = doc.data?.lines ?? [];
  const grandTotal = doc.data?.document.GrandTotalAmount;
  // GrandTotalAmount is only ever computed at finalize (tax calculation
  // happens then, not per-line) — a DRAFT document's own total is
  // always null. Without this, the counter showed nothing at all for
  // "how much does this add up to so far" while still adding items,
  // which is the one number a cashier and a customer both actually
  // want to see mid-sale. Sum of line totals only (pre-tax) — never
  // presented as the final amount, which is what "Grand total" alone
  // (once it exists, post-finalize) still means.
  const runningSubtotal = lines.reduce((sum, l) => sum + Number(l.LineTotal.amount), 0);
  const currencyCode = grandTotal?.currency ?? doc.data?.document.CurrencyCode ?? "INR";

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
              <div className={styles.customerSearchRow}>
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
                  {showCustomerResults && customerQuery.trim().length >= 2 ? (
                    <ul className={styles.dropdown} role="menu" aria-label="Customer results">
                      {customerSearch.data && customerSearch.data.length > 0 ? (
                        customerSearch.data.map((p) => (
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
                        ))
                      ) : (
                        <li className={ui.muted} style={{ padding: "8px 12px" }}>
                          No match for "{customerQuery}".
                        </li>
                      )}
                      <li>
                        <button
                          type="button"
                          role="menuitem"
                          className={styles.dropdownItem}
                          onClick={() => {
                            setShowCustomerResults(false);
                            setQuickAddOpen(true);
                          }}
                        >
                          <strong style={{ color: "var(--color-accent)" }}>+ New customer{customerQuery.trim() ? ` "${customerQuery.trim()}"` : ""}</strong>
                        </button>
                      </li>
                    </ul>
                  ) : null}
                </div>
                <button type="button" className={ui.btnSecondary} onClick={() => setQuickAddOpen(true)}>
                  + New
                </button>
                <button type="button" className={ui.btnSecondary} disabled={startWalkIn.isPending} onClick={() => startWalkIn.mutate()} title="Skip picking a customer — for a quick retail sale">
                  {startWalkIn.isPending ? "…" : "Walk-in / Cash sale"}
                </button>
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
        {startWalkIn.isError ? (
          <p className={styles.errorText} role="alert">
            {startWalkIn.error instanceof ApiError ? startWalkIn.error.message : "Could not start a walk-in sale."}
          </p>
        ) : null}
        {org.isError ? (
          <p className={styles.errorText} role="alert">
            Could not load your branch/warehouse setup. Check Settings.
          </p>
        ) : null}
      </div>

      {/* The item search and the cart render from the moment this screen
          opens, not only once a customer has been picked and "Start sale"
          clicked — the shape of the bill you're about to write should be
          visible immediately (and it's most of what made this screen read
          as an empty page before). Both are inert until a draft exists;
          the search input says so rather than silently doing nothing. */}
      <>
          <div className={layout.panel} data-inert={!documentId ? "true" : undefined}>
            <label htmlFor="product-search" className={styles.searchLabel}>
              Search or scan a product
            </label>
            <div className={styles.searchInputWrap}>
              <SearchIcon className={styles.searchInputIcon} aria-hidden="true" />
              <input
                id="product-search"
                ref={searchInputRef}
                className={`${ui.input} ${styles.searchInput}`}
                placeholder={documentId ? "Type a product name, scan a barcode, or scroll below to browse…" : "Pick a customer above to start billing…"}
                value={productQuery}
                disabled={!documentId}
                onChange={(e) => setProductQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    void handleSearchEnter();
                  }
                }}
                autoComplete="off"
              />
            </div>
            {productResults.length > 0 ? (
              <div className={styles.resultsTable}>
                <div className={styles.resultsHeader}>
                  <span>Item</span>
                  <span className={styles.resultsHeaderNum}>Stock</span>
                  <span className={styles.resultsHeaderNum}>Price</span>
                  <span />
                </div>
                <div className={styles.resultsBody}>
                  {productResults.map((r) => {
                    const stock = Number(r.QuantityAvailable || "0");
                    return (
                      <div key={r.ProductVariantID} className={styles.resultRow}>
                        <div className={styles.resultName}>
                          <strong>{r.ProductName}</strong>
                          <span className={ui.muted}>SKU {r.SKUCode || "—"}</span>
                        </div>
                        <span className={ui.badge} data-tone={stock <= 0 ? "negative" : stock < 5 ? "warning" : "neutral"}>
                          {r.QuantityAvailable || "0"}
                        </span>
                        <span className={styles.resultPrice}>{r.UnitPrice ? formatMoney(r.UnitPrice) : "—"}</span>
                        <button type="button" className={ui.btnPrimary} disabled={addLine.isPending} onClick={() => handleAddProduct(r)}>
                          Add
                        </button>
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : null}
          </div>

          <div className={layout.panel}>
            <h2>Items ({lines.length})</h2>
            {lines.length === 0 ? (
              <div className={ui.tableScroll}>
                <table className={`${ui.table} ${styles.lineGrid}`}>
                  <thead>
                    <tr>
                      <th scope="col" className={styles.colNum}>
                        #
                      </th>
                      <th scope="col">Item</th>
                      <th scope="col">HSN/SAC</th>
                      <th scope="col" className={styles.colRight}>
                        GST %
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Qty
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Rate
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Discount
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Amount
                      </th>
                      <th scope="col" />
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td colSpan={9} className={styles.gridEmpty}>
                        {documentId ? "Scan a barcode or search above to add the first item." : "Pick a customer above, then scan or search to add items."}
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            ) : (
              <div className={ui.tableScroll}>
                <table className={`${ui.table} ${styles.lineGrid}`}>
                  <thead>
                    <tr>
                      <th scope="col" className={styles.colNum}>
                        #
                      </th>
                      <th scope="col">Item</th>
                      <th scope="col">HSN/SAC</th>
                      <th scope="col" className={styles.colRight}>
                        GST %
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Qty
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Rate
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Discount
                      </th>
                      <th scope="col" className={styles.colRight}>
                        Amount
                      </th>
                      <th scope="col" />
                    </tr>
                  </thead>
                  <tbody>
                    {lines.map((l) => (
                      <tr key={l.ID}>
                        <td className={`num ${styles.colNum}`}>{l.LineNumber}</td>
                        <td className={styles.itemCell}>{nameByVariant[l.ProductVariantID] ?? <span className={ui.muted}>Item {l.LineNumber}</span>}</td>
                        <td className={styles.hsnCell}>{l.HSNSACCode || "—"}</td>
                        <td className={`num ${styles.colRight}`}>
                          <LineGstRate hsnSacCode={l.HSNSACCode} />
                        </td>
                        <td className={`num ${styles.colRight}`}>
                          <EditableQty line={l} disabled={updateLine.isPending} onCommit={(quantity) => updateLine.mutate({ line: l, quantity })} />
                        </td>
                        <td className={`num ${styles.colRight}`}>{formatMoney(l.UnitPrice)}</td>
                        <td className={`num ${styles.colRight}`}>
                          <EditableDiscount line={l} disabled={updateLine.isPending} onCommit={(discount) => updateLine.mutate({ line: l, discount })} />
                        </td>
                        <td className={`num ${styles.colRight} ${styles.amountCell}`}>{formatMoney(l.LineTotal)}</td>
                        <td className={styles.colAction}>
                          <button
                            type="button"
                            className={styles.removeButton}
                            disabled={removeLine.isPending}
                            onClick={() => removeLine.mutate(l.ID)}
                            aria-label={`Remove ${nameByVariant[l.ProductVariantID] ?? `line ${l.LineNumber}`}`}
                            title="Remove this item"
                          >
                            ×
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                  <tfoot>
                    <tr className={styles.gridTotalRow}>
                      <td />
                      <td>Total</td>
                      <td />
                      <td />
                      <td className={`num ${styles.colRight}`}>{lines.reduce((sum, l) => sum + Number(l.Quantity), 0)}</td>
                      <td />
                      <td />
                      <td className={`num ${styles.colRight} ${styles.amountCell}`}>
                        {formatMoney({ amount: String(runningSubtotal), currency: currencyCode })}
                      </td>
                      <td />
                    </tr>
                  </tfoot>
                </table>
              </div>
            )}
            <div className={styles.summaryBar}>
              <span className={styles.summaryCount}>
                {lines.length} item{lines.length === 1 ? "" : "s"}
              </span>
              <div className={styles.summaryTotal}>
                <span>{grandTotal ? "Grand total" : "Subtotal · tax added on finalize"}</span>
                <strong className="num">{grandTotal ? formatMoney(grandTotal) : formatMoney({ amount: String(runningSubtotal), currency: currencyCode })}</strong>
              </div>
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
                {/* Always just "Finalize sale" here, regardless of
                    document type — whether an e-Way Bill is actually
                    needed depends on the finalized invoice's value vs.
                    the GST threshold (org-configurable, GstPage), which
                    isn't known until FinalizeDocument runs its real tax
                    calculation server-side. Claiming "…continue to
                    e-Way Bill" on every eligible document type
                    regardless of value overpromised a step most small
                    sales never need. The invoice detail page's
                    EwayBillCard already does the real, accurate
                    threshold check and only offers e-Way Bill actions
                    when eligibility.Requirement isn't NOT_REQUIRED. */}
                {finalize.isPending ? "Saving…" : "Finalize sale"}
              </button>
            </div>
            {finalize.isError ? (
              <p className={styles.errorText} role="alert">
                {finalize.error instanceof ApiError ? finalize.error.message : "Could not finalize this sale."}
              </p>
            ) : null}
            {updateLine.isError ? (
              <p className={styles.errorText} role="alert">
                {updateLine.error instanceof ApiError ? updateLine.error.message : "Could not update that line."}
              </p>
            ) : null}
            {removeLine.isError ? (
              <p className={styles.errorText} role="alert">
                {removeLine.error instanceof ApiError ? removeLine.error.message : "Could not remove that item."}
              </p>
            ) : null}
          </div>
        </>

      <QuickAddPartyModal
        open={quickAddOpen}
        onOpenChange={setQuickAddOpen}
        partyType="CUSTOMER"
        currencyCode={org.organisation?.DefaultCurrencyCode || "INR"}
        initialLegalName={customerQuery.trim()}
        onCreated={(party) => {
          setCustomer(party);
          setCustomerQuery("");
          setShowCustomerResults(false);
        }}
      />
    </div>
  );
}
