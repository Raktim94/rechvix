import { StatusBadge, useDocAssignments } from "../../components/DocumentStatuses";
import { RecurringList } from "../../components/RecurringPanel";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { WhatsAppIcon } from "../../components/icons";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import { withLegalEntity } from "../../lib/useReportTable";
import { useShareSalesDocumentOnWhatsApp, useShareSalesDocumentPdf } from "../../lib/whatsapp";
import layout from "../DashboardPage.module.css";
import { DOCUMENT_TYPE_LABELS, PAYMENT_LABELS, PAYMENT_TONES, paymentState, type DocumentStatus, type DocumentType, type PaymentState, type SalesDocument } from "./types";

/** Row-level counterpart to SalesDetailPage's "Share via WhatsApp" —
 * lets a counter operator send a finalized bill straight from the list
 * without opening it first, the fastest path when they already know
 * which row they want. Drafts have no finalized total/number to share
 * yet, so this only ever renders for non-draft rows. */
function ShareRowButton({ document, customer }: { document: SalesDocument; customer: Party | undefined }) {
  const share = useShareSalesDocumentOnWhatsApp();
  const phone = customer?.Phone;
  if (!phone) return null;
  return (
    <button
      type="button"
      className={ui.btnGhost}
      disabled={share.isPending}
      title="Share via WhatsApp"
      aria-label={`Share ${document.DocumentNumber || "this sale"} via WhatsApp`}
      onClick={() =>
        share.mutate({
          documentId: document.ID,
          phone,
          message: `Hi ${customer.LegalName}, your ${DOCUMENT_TYPE_LABELS[document.DocumentType].toLowerCase()} ${document.DocumentNumber} for ${
            document.GrandTotalAmount ? formatMoney(document.GrandTotalAmount) : "—"
          } is ready. Thank you for your business!`,
        })
      }
    >
      <WhatsAppIcon />
    </button>
  );
}

/** Row-level counterpart to SalesDetailPage's "Share PDF" — shares the
 * actual PDF file (not just a wa.me link) via the native share sheet;
 * see useShareSalesDocumentPdf's doc comment. */
function SharePdfRowButton({ document }: { document: SalesDocument }) {
  const share = useShareSalesDocumentPdf();
  return (
    <button
      type="button"
      className={ui.btnGhost}
      disabled={share.isPending}
      title="Share PDF"
      aria-label={`Share ${document.DocumentNumber || "this sale"} as PDF`}
      onClick={() => share.mutate({ documentId: document.ID, documentNumber: document.DocumentNumber })}
    >
      PDF
    </button>
  );
}

function statusTone(status: SalesDocument["Status"]) {
  if (status === "FINALIZED") return "positive";
  if (status === "CANCELLED") return "negative";
  return "warning";
}

type StatusFilter = "ALL" | DocumentStatus;
type TypeFilter = "ALL" | DocumentType;
type PayFilter = "ANY" | "OPEN" | "OVERDUE";

/** One-click views, ordered by how often a counter operator needs them.
 * Quotations and orders mirror the old "Open quotations/orders" shortcuts:
 * finalized ones are the ones still awaiting "Convert to…". */
const VIEWS: { key: string; label: string; status: StatusFilter; type: TypeFilter; pay?: PayFilter }[] = [
  { key: "all", label: "All", status: "ALL", type: "ALL" },
  { key: "invoices", label: "Invoices", status: "FINALIZED", type: "TAX_INVOICE" },
  { key: "unpaid", label: "Unpaid", status: "FINALIZED", type: "ALL", pay: "OPEN" },
  { key: "overdue", label: "Overdue", status: "FINALIZED", type: "ALL", pay: "OVERDUE" },
  { key: "drafts", label: "Drafts", status: "DRAFT", type: "ALL" },
  { key: "quotes", label: "Quotations", status: "FINALIZED", type: "QUOTATION" },
  { key: "orders", label: "Orders", status: "FINALIZED", type: "SALES_ORDER" },
];

const STATUS_LABELS: Record<DocumentStatus, string> = { DRAFT: "Draft", FINALIZED: "Finalized", CANCELLED: "Cancelled" };

function matchesView(d: SalesDocument, v: { status: StatusFilter; type: TypeFilter; pay?: PayFilter }) {
  if (v.status !== "ALL" && d.Status !== v.status) return false;
  if (v.type !== "ALL" && d.DocumentType !== v.type) return false;
  if (v.pay) {
    const ps = paymentState(d);
    return v.pay === "OPEN" ? ps !== null && ps !== "PAID" : ps === "OVERDUE";
  }
  return true;
}

function PaymentBadge({ document }: { document: SalesDocument }) {
  const ps: PaymentState | null = paymentState(document);
  if (!ps) return null;
  return (
    <span className={ui.badge} data-tone={PAYMENT_TONES[ps]}>
      {PAYMENT_LABELS[ps]}
    </span>
  );
}

export function SalesListPage() {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<StatusFilter>("ALL");
  const [type, setType] = useState<TypeFilter>("ALL");
  const [pay, setPay] = useState<PayFilter>("ANY");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const org = useOrgContext();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const billNotes = useMutation({
    mutationFn: () => api.post<{ document: { ID: string } }>("/sales/documents/bill-delivery-notes", { document_ids: [...picked] }),
    onSuccess: (r) => {
      setPicked(new Set());
      void queryClient.invalidateQueries({ queryKey: ["sales-documents"] });
      void navigate({ to: "/sales/new", search: { resume: r.document.ID } });
    },
  });

  const documents = useQuery({
    queryKey: ["sales-documents", org.legalEntity?.ID],
    queryFn: () => api.getListField<SalesDocument>(withLegalEntity("/sales/documents", org.legalEntity?.ID), "documents"),
  });
  // Only used to resolve CustomerPartyID -> a display name and to let the
  // search box match by customer name, not just document number — the
  // sales document list endpoint has no server-side search/filter beyond
  // document_type (see internal/modules/sales/httpapi's listDocuments),
  // so this is client-side, same scale assumption as ContactsPage's own
  // unpaginated party list.
  const customers = useQuery({
    queryKey: ["parties"],
    queryFn: () => api.getListField<Party>("/contacts/parties", "parties"),
  });
  const customerById = new Map(customers.data?.map((p) => [p.ID, p]));
  const customerNameById = new Map(customers.data?.map((p) => [p.ID, p.LegalName]));

  const q = query.trim().toLowerCase();
  const filtered = (documents.data ?? []).filter((d) => {
    if (status !== "ALL" && d.Status !== status) return false;
    if (type !== "ALL" && d.DocumentType !== type) return false;
    if (pay !== "ANY") {
      const ps = paymentState(d);
      if (pay === "OPEN" ? ps === null || ps === "PAID" : ps !== "OVERDUE") return false;
    }
    if (from && d.IssueDate.slice(0, 10) < from) return false;
    if (to && d.IssueDate.slice(0, 10) > to) return false;
    if (q) {
      const customerName = (customerNameById.get(d.CustomerPartyID) ?? "").toLowerCase();
      if (!d.DocumentNumber.toLowerCase().includes(q) && !customerName.includes(q)) return false;
    }
    return true;
  });

  const assignments = useDocAssignments(filtered.slice(0, 200).map((d) => d.ID));
  const workflowByDoc = new Map((assignments.data ?? []).map((a) => [a.document_id, a]));

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Sales</h1>
          <p className={layout.subtitle}>Every quotation, order, and invoice — draft or finalized.</p>
        </div>
        <Link to="/sales/new" className={ui.btnPrimary}>
          + New sale
        </Link>
      </div>

      <div className={layout.panel}>
        <div className={ui.segmented} role="group" aria-label="Sales views">
          {VIEWS.map((v) => {
            const count = (documents.data ?? []).filter((d) => matchesView(d, v)).length;
            const active = status === v.status && type === v.type && pay === (v.pay ?? "ANY");
            return (
              <button
                key={v.key}
                type="button"
                className={ui.segment}
                aria-pressed={active}
                onClick={() => {
                  setStatus(v.status);
                  setType(v.type);
                  setPay(v.pay ?? "ANY");
                }}
              >
                {v.label}
                {documents.data ? <span className={ui.segmentCount}>{count}</span> : null}
              </button>
            );
          })}
        </div>
        <div className={ui.toolbar} style={{ marginBottom: 12 }}>
          <input
            className={ui.input}
            placeholder="Search sales…"
            aria-label="Search sales"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ maxWidth: 280 }}
          />
        </div>
        <details style={{ marginBottom: 12 }}>
          <summary style={{ cursor: "pointer", minHeight: 36, display: "flex", alignItems: "center", fontSize: "var(--text-sm)", fontWeight: 600, color: "var(--color-text-muted)" }}>
            More filters{from || to || !VIEWS.some((v) => v.status === status && v.type === type && (v.pay ?? "ANY") === pay) ? " (active)" : ""}
          </summary>
          <div className={ui.toolbar} style={{ marginTop: 8 }}>
          <select className={ui.select} aria-label="Filter by status" value={status} onChange={(e) => setStatus(e.target.value as StatusFilter)}>
              <option value="ALL">All statuses</option>
              <option value="DRAFT">Draft</option>
              <option value="FINALIZED">Finalized</option>
              <option value="CANCELLED">Cancelled</option>
            </select>
            <select className={ui.select} aria-label="Filter by type" value={type} onChange={(e) => setType(e.target.value as TypeFilter)}>
              <option value="ALL">All types</option>
              {Object.entries(DOCUMENT_TYPE_LABELS).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
            <label className={ui.muted} style={{ display: "flex", alignItems: "center", gap: 6 }}>
              From
              <input type="date" className={ui.input} aria-label="From date" value={from} onChange={(e) => setFrom(e.target.value)} />
            </label>
            <label className={ui.muted} style={{ display: "flex", alignItems: "center", gap: 6 }}>
              To
              <input type="date" className={ui.input} aria-label="To date" value={to} onChange={(e) => setTo(e.target.value)} />
            </label>
          </div>
        </details>

        {documents.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load sales documents.
          </p>
        ) : documents.isPending ? (
          <div className={layout.skeleton} style={{ height: 240 }} aria-hidden="true" />
        ) : filtered.length === 0 ? (
          <div className={layout.emptyState}>
            {documents.data.length === 0 ? (
              <>
                <p style={{ margin: "0 0 var(--space-3)" }}>No sales yet. Create your first invoice in under a minute.</p>
                <Link to="/sales/new" className={ui.btnPrimary}>
                  + New sale
                </Link>
              </>
            ) : (
              <>
                <p style={{ margin: "0 0 var(--space-3)" }}>No sales match these filters.</p>
                <button
                  type="button"
                  className={ui.btnSecondary}
                  onClick={() => {
                    setStatus("ALL");
                    setType("ALL");
                    setPay("ANY");
                    setQuery("");
                    setFrom("");
                    setTo("");
                  }}
                >
                  Clear filters
                </button>
              </>
            )}
          </div>
        ) : (
          <div>
            {picked.size > 0 ? (
              <div className={ui.formActions} style={{ justifyContent: "flex-start", margin: "8px 0" }}>
                <button type="button" className={ui.btnPrimary} disabled={billNotes.isPending} onClick={() => billNotes.mutate()}>
                  {billNotes.isPending ? "Creating…" : `Bill ${picked.size} delivery note${picked.size === 1 ? "" : "s"} in one invoice`}
                </button>
                <button type="button" className={ui.btnSecondary} onClick={() => setPicked(new Set())}>Clear</button>
              </div>
            ) : null}
            {billNotes.isError ? <p role="alert">{billNotes.error instanceof ApiError ? billNotes.error.message : "Could not create the invoice."}</p> : null}
          <div className={ui.tableScroll} tabIndex={0}>
            <table className={`${ui.table} ${ui.tableStack}`}>
              <thead>
                <tr>
                  <th scope="col"><span className="srOnly">Select delivery notes to bill</span></th>
                  <th scope="col">Number</th>
                  <th scope="col">Customer</th>
                  <th scope="col">Type</th>
                  <th scope="col">Status</th>
                  <th scope="col">Date</th>
                  <th scope="col">Total</th>
                  <th scope="col"><span className="srOnly">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((d) => (
                  <tr key={d.ID}>
                    <td>
                      {d.DocumentType === "DELIVERY_CHALLAN" && d.Status === "FINALIZED" ? (
                        <input
                          type="checkbox"
                          aria-label={`Bill delivery note ${d.DocumentNumber}`}
                          checked={picked.has(d.ID)}
                          onChange={(e) => {
                            const next = new Set(picked);
                            if (e.target.checked) next.add(d.ID);
                            else next.delete(d.ID);
                            setPicked(next);
                          }}
                        />
                      ) : null}
                    </td>
                    <td data-primary>
                      <Link
                        to={d.Status === "DRAFT" ? "/sales/new" : "/sales/$id"}
                        params={d.Status === "DRAFT" ? undefined : { id: d.ID }}
                        search={d.Status === "DRAFT" ? { resume: d.ID } : undefined}
                        className={ui.linkRow}
                      >
                        {d.DocumentNumber || "(draft)"}
                      </Link>
                    </td>
                    <td data-label="Customer">{customerNameById.get(d.CustomerPartyID) ?? "—"}</td>
                    <td data-label="Type">{DOCUMENT_TYPE_LABELS[d.DocumentType]}</td>
                    <td>
                      {/* A finalized invoice's useful state is whether it is paid, so the
                          payment badge replaces the plain "Finalized" one. */}
                      {paymentState(d) ? (
                        <PaymentBadge document={d} />
                      ) : (
                        <span className={ui.badge} data-tone={statusTone(d.Status)}>
                          {STATUS_LABELS[d.Status]}
                        </span>
                      )}
                      {workflowByDoc.get(d.ID) ? <> <StatusBadge name={workflowByDoc.get(d.ID)!.name} color={workflowByDoc.get(d.ID)!.color} /></> : null}
                    </td>
                    <td data-label="Date">{new Date(d.IssueDate).toLocaleDateString()}</td>
                    <td className="num" data-end>{d.GrandTotalAmount ? formatMoney(d.GrandTotalAmount) : "—"}</td>
                    <td>
                      {d.Status !== "DRAFT" ? (
                        <div style={{ display: "flex", gap: 4 }}>
                          <ShareRowButton document={d} customer={customerById.get(d.CustomerPartyID)} />
                          <SharePdfRowButton document={d} />
                        </div>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          </div>
        )}
      </div>
      <RecurringList />
    </div>
  );
}
