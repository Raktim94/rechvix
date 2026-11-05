import { StatusBadge, useDocAssignments } from "../../components/DocumentStatuses";
import { RecurringList } from "../../components/RecurringPanel";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { WhatsAppIcon } from "../../components/icons";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatLocale } from "../../i18n";
import { formatMoney } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import { useOrgContext } from "../../lib/useOrgContext";
import { withLegalEntity } from "../../lib/useReportTable";
import { useShareSalesDocumentOnWhatsApp, useShareSalesDocumentPdf } from "../../lib/whatsapp";
import layout from "../DashboardPage.module.css";
import { DOCUMENT_TYPE_LABELS, PAYMENT_TONES, paymentState, type DocumentStatus, type DocumentType, type PaymentState, type SalesDocument } from "./types";

/** Row-level counterpart to SalesDetailPage's "Share via WhatsApp" —
 * lets a counter operator send a finalized bill straight from the list
 * without opening it first, the fastest path when they already know
 * which row they want. Drafts have no finalized total/number to share
 * yet, so this only ever renders for non-draft rows. */
function ShareRowButton({ document, customer }: { document: SalesDocument; customer: Party | undefined }) {
  const { t } = useTranslation();
  const share = useShareSalesDocumentOnWhatsApp();
  const phone = customer?.Phone;
  if (!phone) return null;
  return (
    <button
      type="button"
      className={ui.btnGhost}
      disabled={share.isPending}
      title={t("salesList.share.whatsapp")}
      aria-label={t("salesList.share.whatsappAria", { doc: document.DocumentNumber || t("salesList.share.thisSale") })}
      onClick={() =>
        share.mutate({
          documentId: document.ID,
          phone,
          message: t("salesList.share.message", {
            name: customer.LegalName,
            type: t(`salesList.type.${document.DocumentType}`).toLowerCase(),
            number: document.DocumentNumber,
            total: document.GrandTotalAmount ? formatMoney(document.GrandTotalAmount) : "—",
          }),
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
  const { t } = useTranslation();
  const share = useShareSalesDocumentPdf();
  return (
    <button
      type="button"
      className={ui.btnGhost}
      disabled={share.isPending}
      title={t("salesList.share.pdf")}
      aria-label={t("salesList.share.pdfAria", { doc: document.DocumentNumber || t("salesList.share.thisSale") })}
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
const VIEWS: { key: string; status: StatusFilter; type: TypeFilter; pay?: PayFilter }[] = [
  { key: "all", status: "ALL", type: "ALL" },
  { key: "invoices", status: "FINALIZED", type: "TAX_INVOICE" },
  { key: "unpaid", status: "FINALIZED", type: "ALL", pay: "OPEN" },
  { key: "overdue", status: "FINALIZED", type: "ALL", pay: "OVERDUE" },
  { key: "drafts", status: "DRAFT", type: "ALL" },
  { key: "quotes", status: "FINALIZED", type: "QUOTATION" },
  { key: "orders", status: "FINALIZED", type: "SALES_ORDER" },
];

const VIEW_LABEL_KEYS: Record<string, string> = { all: "all", invoices: "invoices", unpaid: "unpaid", overdue: "overdue", drafts: "drafts", quotes: "quotations", orders: "orders" };

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
  const { t } = useTranslation();
  const ps: PaymentState | null = paymentState(document);
  if (!ps) return null;
  return (
    <span className={ui.badge} data-tone={PAYMENT_TONES[ps]}>
      {t(`salesList.payment.${ps}`)}
    </span>
  );
}

export function SalesListPage() {
  const { t } = useTranslation();
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
          <h1>{t("salesList.title")}</h1>
          <p className={layout.subtitle}>{t("salesList.subtitle")}</p>
        </div>
        <Link to="/sales/new" className={ui.btnPrimary}>
          {t("salesList.newSale")}
        </Link>
      </div>

      <div className={layout.panel}>
        <div className={ui.segmented} role="group" aria-label={t("salesList.viewsLabel")}>
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
                {t(`salesList.views.${VIEW_LABEL_KEYS[v.key]}`)}
                {documents.data ? <span className={ui.segmentCount}>{count}</span> : null}
              </button>
            );
          })}
        </div>
        <div className={ui.toolbar} style={{ marginBottom: 12 }}>
          <input
            className={ui.input}
            placeholder={t("salesList.searchPlaceholder")}
            aria-label={t("salesList.searchLabel")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ maxWidth: 280 }}
          />
        </div>
        <details style={{ marginBottom: 12 }}>
          <summary style={{ cursor: "pointer", minHeight: 36, display: "flex", alignItems: "center", fontSize: "var(--text-sm)", fontWeight: 600, color: "var(--color-text-muted)" }}>
            {from || to || !VIEWS.some((v) => v.status === status && v.type === type && (v.pay ?? "ANY") === pay) ? t("salesList.moreFiltersActive") : t("salesList.moreFilters")}
          </summary>
          <div className={ui.toolbar} style={{ marginTop: 8 }}>
          <select className={ui.select} aria-label={t("salesList.filterStatus")} value={status} onChange={(e) => setStatus(e.target.value as StatusFilter)}>
              <option value="ALL">{t("salesList.allStatuses")}</option>
              <option value="DRAFT">{t("salesList.status.DRAFT")}</option>
              <option value="FINALIZED">{t("salesList.status.FINALIZED")}</option>
              <option value="CANCELLED">{t("salesList.status.CANCELLED")}</option>
            </select>
            <select className={ui.select} aria-label={t("salesList.filterType")} value={type} onChange={(e) => setType(e.target.value as TypeFilter)}>
              <option value="ALL">{t("salesList.allTypes")}</option>
              {Object.keys(DOCUMENT_TYPE_LABELS).map((value) => (
                <option key={value} value={value}>
                  {t(`salesList.type.${value}`)}
                </option>
              ))}
            </select>
            <label className={ui.muted} style={{ display: "flex", alignItems: "center", gap: 6 }}>
              {t("salesList.from")}
              <input type="date" className={ui.input} aria-label={t("salesList.fromDate")} value={from} onChange={(e) => setFrom(e.target.value)} />
            </label>
            <label className={ui.muted} style={{ display: "flex", alignItems: "center", gap: 6 }}>
              {t("salesList.to")}
              <input type="date" className={ui.input} aria-label={t("salesList.toDate")} value={to} onChange={(e) => setTo(e.target.value)} />
            </label>
          </div>
        </details>

        {documents.isError ? (
          <p className={layout.errorState} role="alert">
            {t("salesList.loadFailed")}
          </p>
        ) : documents.isPending ? (
          <div className={layout.skeleton} style={{ height: 240 }} aria-hidden="true" />
        ) : filtered.length === 0 ? (
          <div className={layout.emptyState}>
            {documents.data.length === 0 ? (
              <>
                <p style={{ margin: "0 0 var(--space-3)" }}>{t("salesList.emptyNone")}</p>
                <Link to="/sales/new" className={ui.btnPrimary}>
                  {t("salesList.newSale")}
                </Link>
              </>
            ) : (
              <>
                <p style={{ margin: "0 0 var(--space-3)" }}>{t("salesList.emptyFiltered")}</p>
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
                  {t("salesList.clearFilters")}
                </button>
              </>
            )}
          </div>
        ) : (
          <div>
            {picked.size > 0 ? (
              <div className={ui.formActions} style={{ justifyContent: "flex-start", margin: "8px 0" }}>
                <button type="button" className={ui.btnPrimary} disabled={billNotes.isPending} onClick={() => billNotes.mutate()}>
                  {billNotes.isPending ? t("salesList.creating") : t("salesList.billNotes", { count: picked.size })}
                </button>
                <button type="button" className={ui.btnSecondary} onClick={() => setPicked(new Set())}>{t("salesList.clear")}</button>
              </div>
            ) : null}
            {billNotes.isError ? <p role="alert">{billNotes.error instanceof ApiError ? billNotes.error.message : t("salesList.createFailed")}</p> : null}
          <div className={ui.tableScroll} tabIndex={0}>
            <table className={`${ui.table} ${ui.tableStack}`}>
              <thead>
                <tr>
                  <th scope="col"><span className="srOnly">{t("salesList.col.select")}</span></th>
                  <th scope="col">{t("salesList.col.number")}</th>
                  <th scope="col">{t("salesList.col.customer")}</th>
                  <th scope="col">{t("salesList.col.type")}</th>
                  <th scope="col">{t("salesList.col.status")}</th>
                  <th scope="col">{t("salesList.col.date")}</th>
                  <th scope="col">{t("salesList.col.total")}</th>
                  <th scope="col"><span className="srOnly">{t("salesList.col.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((d) => (
                  <tr key={d.ID}>
                    <td>
                      {d.DocumentType === "DELIVERY_CHALLAN" && d.Status === "FINALIZED" ? (
                        <input
                          type="checkbox"
                          aria-label={t("salesList.billNote", { number: d.DocumentNumber })}
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
                        {d.DocumentNumber || t("salesList.draftNumber")}
                      </Link>
                    </td>
                    <td data-label={t("salesList.col.customer")}>{customerNameById.get(d.CustomerPartyID) ?? "—"}</td>
                    <td data-label={t("salesList.col.type")}>{t(`salesList.type.${d.DocumentType}`)}</td>
                    <td>
                      {/* A finalized invoice's useful state is whether it is paid, so the
                          payment badge replaces the plain "Finalized" one. */}
                      {paymentState(d) ? (
                        <PaymentBadge document={d} />
                      ) : (
                        <span className={ui.badge} data-tone={statusTone(d.Status)}>
                          {t(`salesList.status.${d.Status}`)}
                        </span>
                      )}
                      {workflowByDoc.get(d.ID) ? <> <StatusBadge name={workflowByDoc.get(d.ID)!.name} color={workflowByDoc.get(d.ID)!.color} /></> : null}
                    </td>
                    <td data-label={t("salesList.col.date")}>{new Date(d.IssueDate).toLocaleDateString(formatLocale())}</td>
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
