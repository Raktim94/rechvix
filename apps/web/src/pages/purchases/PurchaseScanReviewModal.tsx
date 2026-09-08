import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import modal from "../../components/Modal.module.css";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import type { ParsedBill, ParsedBillLine } from "../../lib/billParser";
import { createPartyWithDetails } from "../../lib/parties";
import type { Party } from "../../lib/partyTypes";
import styles from "./PurchaseScanReviewModal.module.css";

interface ProductOption {
  ID: string;
  Name: string;
  BaseUOMID: string;
}

interface ReviewLine {
  key: string;
  description: string;
  quantity: string;
  unitPrice: string;
  matched: ProductOption | null;
  excluded: boolean;
}

export interface ResolvedScanLine {
  productVariantId: string;
  unitId: string;
  quantity: string;
  unitPrice: string;
}

/** One row's own product-search combobox — kept as its own component so
 * each row's debounce/dropdown state doesn't need to live in the parent
 * array (which would mean re-rendering and re-keying every row on every
 * keystroke in any one of them). */
function ProductMatchCell({ line, onMatch }: { line: ReviewLine; onMatch: (product: ProductOption | null) => void }) {
  const [query, setQuery] = useState(line.matched ? line.matched.Name : line.description);
  const [results, setResults] = useState<ProductOption[]>([]);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (line.matched || query.trim().length < 2) {
      setResults([]);
      return;
    }
    const handle = setTimeout(() => {
      api
        .get<{ products: ProductOption[] | null }>(`/catalogue/products?q=${encodeURIComponent(query)}`)
        .then((res) => setResults(res.products ?? []))
        .catch(() => setResults([]));
    }, 200);
    return () => clearTimeout(handle);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, line.matched]);

  return (
    <div className={styles.productCell}>
      <input
        className={ui.input}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setOpen(true);
          if (line.matched) onMatch(null);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        placeholder="Match to a catalogue product…"
      />
      {open && results.length > 0 ? (
        <ul className={styles.productDropdown} role="menu">
          {results.map((p) => (
            <li key={p.ID}>
              <button
                type="button"
                className={styles.productDropdownItem}
                onClick={() => {
                  setQuery(p.Name);
                  setOpen(false);
                  onMatch(p);
                }}
              >
                {p.Name}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export function PurchaseScanReviewModal({
  open,
  onOpenChange,
  parsedBill,
  currencyCode,
  committing = false,
  onCommitted,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  parsedBill: ParsedBill | null;
  currencyCode: string;
  /** True while the caller is still turning a committed result into an
   * actual purchase document + lines — kept separate from this modal's
   * own `commit` mutation (which only covers resolving the supplier)
   * so the primary button stays disabled/labelled through that second
   * phase too, instead of flipping back to normal the instant this
   * modal's own work is done. */
  committing?: boolean;
  onCommitted: (result: { supplierId: string; lines: ResolvedScanLine[] }) => void;
}) {
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [gstin, setGstin] = useState("");
  const [supplierMode, setSupplierMode] = useState<"existing" | "new">("new");
  const [selectedSupplier, setSelectedSupplier] = useState<Party | null>(null);
  const [lines, setLines] = useState<ReviewLine[]>([]);
  const [showRaw, setShowRaw] = useState(false);

  useEffect(() => {
    if (!open || !parsedBill) return;
    setName(parsedBill.distributorName);
    setPhone(parsedBill.phone);
    setGstin(parsedBill.gstin);
    setSupplierMode("new");
    setSelectedSupplier(null);
    setLines(
      parsedBill.lines.map((l: ParsedBillLine, i) => ({
        key: `${i}-${l.description}`,
        description: l.description,
        quantity: l.quantity,
        unitPrice: l.unitPrice,
        matched: null,
        excluded: false,
      })),
    );
  }, [open, parsedBill]);

  const nameQuery = name.trim();
  const nameMatches = useQuery({
    queryKey: ["supplier-name-match", nameQuery],
    queryFn: () => api.getListField<Party>(`/contacts/parties?q=${encodeURIComponent(nameQuery)}`, "parties"),
    enabled: open && nameQuery.length >= 2,
  });

  const trimmedGstin = gstin.trim().toUpperCase();
  const gstinMatch = useQuery({
    queryKey: ["supplier-gstin-match", trimmedGstin],
    queryFn: async () => {
      try {
        return await api.get<{ party_id: string }>(`/contacts/tax-registrations/${encodeURIComponent(trimmedGstin)}`);
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
      }
    },
    enabled: open && trimmedGstin.length === 15,
  });

  // Fetched by id directly rather than assumed to already be present in
  // nameMatches below — the OCR'd name is exactly the unreliable part of
  // this bill, so a GSTIN hit (unambiguous: one GSTIN, one legal entity)
  // must surface as a selectable candidate on its own even when the
  // scanned name didn't happen to fuzzy-match it. Getting this wrong is
  // exactly how a distributor ends up duplicated.
  const gstinPartyId = gstinMatch.data?.party_id;
  const gstinPartyQuery = useQuery({
    queryKey: ["party-by-id", gstinPartyId],
    queryFn: () => api.get<Party>(`/contacts/parties/${gstinPartyId}`),
    enabled: open && !!gstinPartyId,
  });
  const gstinMatchedParty = gstinPartyQuery.data;

  const candidates = useMemo(() => {
    const out: Party[] = [];
    const seen = new Set<string>();
    if (gstinMatchedParty) {
      out.push(gstinMatchedParty);
      seen.add(gstinMatchedParty.ID);
    }
    for (const p of nameMatches.data ?? []) {
      if (seen.has(p.ID)) continue;
      seen.add(p.ID);
      out.push(p);
    }
    return out;
  }, [gstinMatchedParty, nameMatches.data]);

  useEffect(() => {
    if (gstinMatchedParty && !selectedSupplier) {
      setSupplierMode("existing");
      setSelectedSupplier(gstinMatchedParty);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gstinMatchedParty]);

  function updateLine(key: string, patch: Partial<ReviewLine>) {
    setLines((cur) => cur.map((l) => (l.key === key ? { ...l, ...patch } : l)));
  }

  const includedLines = lines.filter((l) => !l.excluded);
  const readyCount = includedLines.filter((l) => l.matched).length;

  const commit = useMutation({
    mutationFn: async () => {
      let supplierId: string;
      if (supplierMode === "existing" && selectedSupplier) {
        supplierId = selectedSupplier.ID;
      } else {
        const party = await createPartyWithDetails({
          partyType: "SUPPLIER",
          legalName: name,
          phone,
          currencyCode,
          gstin,
        });
        supplierId = party.ID;
      }

      // Resolve each matched product down to the variant/unit id the
      // purchases API actually needs — the same
      // "product -> first variant -> its BaseUOMID" lookup
      // PurchasesPage's own addLine already does for a manually-typed
      // line, just done here for every matched row up front instead of
      // one at a time.
      const resolvedLines: ResolvedScanLine[] = [];
      for (const line of includedLines) {
        if (!line.matched) continue;
        const variants = await api.getListField<{ ID: string }>(`/catalogue/products/${line.matched.ID}/variants`, "variants");
        const variant = variants[0];
        if (!variant) continue;
        resolvedLines.push({ productVariantId: variant.ID, unitId: line.matched.BaseUOMID, quantity: line.quantity, unitPrice: line.unitPrice });
      }

      return { supplierId, lines: resolvedLines };
    },
    onSuccess: (result) => onCommitted(result),
  });

  if (!open || !parsedBill) return null;

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={`${modal.dialog} ${modal.dialogWide}`} role="dialog" aria-modal="true" aria-label="Review scanned bill" onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>Review scanned bill</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <div className={modal.body}>
          <div className={styles.section}>
            <p className={styles.sectionTitle}>Distributor</p>
            {candidates.length > 0 ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                {candidates.map((p) => (
                  <button
                    key={p.ID}
                    type="button"
                    className={styles.matchCard}
                    data-selected={supplierMode === "existing" && selectedSupplier?.ID === p.ID}
                    onClick={() => {
                      setSupplierMode("existing");
                      setSelectedSupplier(p);
                    }}
                  >
                    <div className={styles.matchCardBody}>
                      <div className={styles.matchCardTitle}>{p.LegalName}</div>
                      <div className={styles.matchCardMeta}>
                        Existing supplier{p.Phone ? ` · ${p.Phone}` : ""}
                        {gstinMatchedParty?.ID === p.ID ? " · GSTIN matches exactly" : ""}
                      </div>
                    </div>
                  </button>
                ))}
                <button
                  type="button"
                  className={styles.matchCard}
                  data-selected={supplierMode === "new"}
                  onClick={() => {
                    setSupplierMode("new");
                    setSelectedSupplier(null);
                  }}
                >
                  <div className={styles.matchCardBody}>
                    <div className={styles.matchCardTitle}>Create a new distributor</div>
                    <div className={styles.matchCardMeta}>None of the above — add "{name || "this distributor"}" as a new supplier.</div>
                  </div>
                </button>
              </div>
            ) : null}

            <div className={ui.formGrid}>
              <div className={ui.field}>
                <label htmlFor="scan-name">Name</label>
                <input
                  id="scan-name"
                  className={ui.input}
                  value={name}
                  onChange={(e) => {
                    setName(e.target.value);
                    setSupplierMode("new");
                    setSelectedSupplier(null);
                  }}
                  disabled={supplierMode === "existing"}
                />
              </div>
              <div className={ui.field}>
                <label htmlFor="scan-phone">Phone</label>
                <input id="scan-phone" className={ui.input} value={phone} onChange={(e) => setPhone(e.target.value)} disabled={supplierMode === "existing"} />
              </div>
              <div className={ui.field}>
                <label htmlFor="scan-gstin">GSTIN</label>
                <input id="scan-gstin" className={ui.input} value={gstin} onChange={(e) => setGstin(e.target.value)} maxLength={15} disabled={supplierMode === "existing"} />
              </div>
            </div>
            {supplierMode === "existing" && selectedSupplier ? (
              <p className={ui.muted}>
                Using existing supplier <strong>{selectedSupplier.LegalName}</strong>.{" "}
                <button type="button" className={modal.disclosure} onClick={() => setSupplierMode("new")}>
                  Use a new one instead
                </button>
              </p>
            ) : null}
          </div>

          <div className={styles.section}>
            <p className={styles.sectionTitle}>
              Line items ({readyCount}/{includedLines.length} matched to a catalogue product)
            </p>
            {lines.length === 0 ? (
              <p className={ui.muted}>No line items were detected in this scan — you can still create the purchase and add items manually.</p>
            ) : (
              <div className={ui.tableScroll}>
                <table className={ui.table}>
                  <thead>
                    <tr>
                      <th scope="col">Description (as scanned)</th>
                      <th scope="col">Match</th>
                      <th scope="col">Qty</th>
                      <th scope="col">Unit price</th>
                      <th scope="col" />
                    </tr>
                  </thead>
                  <tbody>
                    {lines.map((l) => (
                      <tr key={l.key} className={l.excluded ? styles.excludedRow : undefined}>
                        <td>{l.description}</td>
                        <td>
                          <ProductMatchCell line={l} onMatch={(product) => updateLine(l.key, { matched: product })} />
                        </td>
                        <td>
                          <input
                            className={`${ui.input} ${styles.qtyInput}`}
                            value={l.quantity}
                            onChange={(e) => updateLine(l.key, { quantity: e.target.value })}
                          />
                        </td>
                        <td>
                          <input
                            className={`${ui.input} ${styles.priceInput}`}
                            value={l.unitPrice}
                            onChange={(e) => updateLine(l.key, { unitPrice: e.target.value })}
                            title="Edit if this differs from what you actually negotiated with the distributor"
                          />
                        </td>
                        <td>
                          <button type="button" className={ui.btnGhost} onClick={() => updateLine(l.key, { excluded: !l.excluded })}>
                            {l.excluded ? "Include" : "Exclude"}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          <button type="button" className={`${modal.disclosure} ${styles.rawTextToggle}`} onClick={() => setShowRaw((v) => !v)}>
            <span className={modal.disclosureChevron} data-open={showRaw} aria-hidden="true">
              ›
            </span>
            {showRaw ? "Hide" : "Show"} raw scanned text
          </button>
          {showRaw ? <pre className={styles.rawText}>{parsedBill.rawText}</pre> : null}

          {commit.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)" }}>
              {commit.error instanceof ApiError ? commit.error.message : "Could not create this supplier."}
            </p>
          ) : null}
        </div>
        <div className={modal.footer}>
          <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
            Cancel
          </button>
          <button
            type="button"
            className={ui.btnPrimary}
            disabled={(supplierMode === "new" && !name.trim()) || (supplierMode === "existing" && !selectedSupplier) || commit.isPending || committing}
            onClick={() => commit.mutate()}
          >
            {commit.isPending || committing ? "Creating…" : "Create purchase from this bill"}
          </button>
        </div>
      </div>
    </div>
  );
}
