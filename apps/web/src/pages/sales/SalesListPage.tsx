import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { WhatsAppIcon } from "../../components/icons";
import ui from "../../components/ui.module.css";
import { api } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import { withLegalEntity } from "../../lib/useReportTable";
import { useShareSalesDocumentOnWhatsApp } from "../../lib/whatsapp";
import layout from "../DashboardPage.module.css";
import { DOCUMENT_TYPE_LABELS, type DocumentStatus, type DocumentType, type SalesDocument } from "./types";

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

function statusTone(status: SalesDocument["Status"]) {
  if (status === "FINALIZED") return "positive";
  if (status === "CANCELLED") return "negative";
  return "warning";
}

type StatusFilter = "ALL" | DocumentStatus;
type TypeFilter = "ALL" | DocumentType;

export function SalesListPage() {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<StatusFilter>("ALL");
  const [type, setType] = useState<TypeFilter>("ALL");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const org = useOrgContext();

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
    if (from && d.IssueDate.slice(0, 10) < from) return false;
    if (to && d.IssueDate.slice(0, 10) > to) return false;
    if (q) {
      const customerName = (customerNameById.get(d.CustomerPartyID) ?? "").toLowerCase();
      if (!d.DocumentNumber.toLowerCase().includes(q) && !customerName.includes(q)) return false;
    }
    return true;
  });

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
        <div className={ui.toolbar} style={{ marginBottom: 8, gap: 8 }}>
          {/* Finalized-but-not-yet-converted quotations/orders are exactly
              what SalesDetailPage's new "Convert to…" action acts on —
              these one-click filters are the fastest way to find them,
              since the list has no server-side way to tell "still open"
              apart from a finalized document simply not having a document
              referencing it yet (not modeled client-side, so this is a
              proxy: finalized quotations/orders are usually awaiting
              conversion in practice). */}
          <button
            type="button"
            className={ui.btnGhost}
            onClick={() => {
              setType("QUOTATION");
              setStatus("FINALIZED");
            }}
          >
            Open quotations
          </button>
          <button
            type="button"
            className={ui.btnGhost}
            onClick={() => {
              setType("SALES_ORDER");
              setStatus("FINALIZED");
            }}
          >
            Open orders
          </button>
          {status !== "ALL" || type !== "ALL" ? (
            <button
              type="button"
              className={ui.btnGhost}
              onClick={() => {
                setStatus("ALL");
                setType("ALL");
              }}
            >
              Clear filters
            </button>
          ) : null}
        </div>
        <div className={ui.toolbar} style={{ marginBottom: 12 }}>
          <input
            className={ui.input}
            placeholder="Search by number or customer…"
            aria-label="Search sales"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ maxWidth: 280 }}
          />
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

        {documents.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load sales documents.
          </p>
        ) : documents.isPending ? (
          <div className={layout.skeleton} style={{ height: 240 }} aria-hidden="true" />
        ) : filtered.length === 0 ? (
          <p className={layout.emptyState}>
            {documents.data.length === 0 ? "No sales yet — start your first sale above." : "No sales match these filters."}
          </p>
        ) : (
          <div className={ui.tableScroll}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Number</th>
                  <th scope="col">Customer</th>
                  <th scope="col">Type</th>
                  <th scope="col">Status</th>
                  <th scope="col">Date</th>
                  <th scope="col">Total</th>
                  <th scope="col" />
                </tr>
              </thead>
              <tbody>
                {filtered.map((d) => (
                  <tr key={d.ID}>
                    <td>
                      <Link
                        to={d.Status === "DRAFT" ? "/sales/new" : "/sales/$id"}
                        params={d.Status === "DRAFT" ? undefined : { id: d.ID }}
                        search={d.Status === "DRAFT" ? { resume: d.ID } : undefined}
                        className={ui.linkRow}
                      >
                        {d.DocumentNumber || "(draft)"}
                      </Link>
                    </td>
                    <td>{customerNameById.get(d.CustomerPartyID) ?? "—"}</td>
                    <td>{DOCUMENT_TYPE_LABELS[d.DocumentType]}</td>
                    <td>
                      <span className={ui.badge} data-tone={statusTone(d.Status)}>
                        {d.Status}
                      </span>
                    </td>
                    <td>{new Date(d.IssueDate).toLocaleDateString()}</td>
                    <td className="num">{d.GrandTotalAmount ? formatMoney(d.GrandTotalAmount) : "—"}</td>
                    <td>{d.Status !== "DRAFT" ? <ShareRowButton document={d} customer={customerById.get(d.CustomerPartyID)} /> : null}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
