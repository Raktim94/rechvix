import type { Money } from "../../lib/money";

/** Mirrors internal/modules/sales/domain.Document/DocumentLine — no json
 * tags on those structs, so Go's exported field names serialize verbatim. */
export type DocumentType =
  | "QUOTATION"
  | "PROFORMA_INVOICE"
  | "SALES_ORDER"
  | "DELIVERY_CHALLAN"
  | "TAX_INVOICE"
  | "POS_INVOICE"
  | "CREDIT_NOTE"
  | "DEBIT_NOTE"
  | "SALES_RETURN"
  | "RECURRING_INVOICE";

export type DocumentStatus = "DRAFT" | "FINALIZED" | "CANCELLED";

export interface SalesDocument {
  ID: string;
  OrganisationID: string;
  LegalEntityID: string;
  BranchID: string;
  WarehouseID: string;
  CustomerPartyID: string;
  DocumentType: DocumentType;
  DocumentNumber: string;
  Status: DocumentStatus;
  IssueDate: string;
  PlaceOfSupplyStateCode: string;
  CurrencyCode: string;
  BaseCurrencyCode: string;
  ExchangeRate: string;
  InvoiceLanguage: string;
  GrandTotalAmount: Money | null;
  DueDate: string | null;
  /** Sum of receipts recorded against the document (read-only, from the API). */
  PaidAmount: string;
  CreatedAt: string;
  FinalizedAt: string | null;
}

export interface SalesDocumentLine {
  ID: string;
  SalesDocumentID: string;
  LineNumber: number;
  ProductVariantID: string;
  UnitID: string;
  Quantity: string;
  UnitPrice: Money;
  LineDiscountAmount: Money;
  HSNSACCode: string;
  LineTotal: Money;
  BatchCode: string;
  SerialCode: string;
}

/** Mirrors ewaybill/httpapi.EWB_ELIGIBLE_TYPES's real-world equivalent —
 * the document types SalesDetailPage shows an EwayBillCard for. Shared
 * here so BillingPage can label its finalize action accordingly instead
 * of duplicating the set. */
export const EWB_ELIGIBLE_TYPES = new Set<DocumentType>(["TAX_INVOICE", "POS_INVOICE", "DELIVERY_CHALLAN", "SALES_RETURN"]);

/** Mirrors sales/domain.RevenueAffecting, narrowed to the two types
 * BillingPage can actually create (a QUOTATION/SALES_ORDER carries no
 * real receivable to be paid against — no journal is posted for either
 * on finalize) — the set SalesDetailPage shows a PaymentPanel for. */
export const PAYABLE_TYPES = new Set<DocumentType>(["TAX_INVOICE", "POS_INVOICE"]);

/** Which target types a FINALIZED document of a given type can be
 * converted into via POST /sales/documents/{id}/convert (sales.Service.
 * ConvertDocument — backend has supported every source/target
 * combination generically since it was added for the return/credit-note
 * flow; this map is just which of those the "Convert to…" action on
 * SalesDetailPage actually surfaces, mirroring the brief §5 lifecycle:
 * quotation -> sales order -> invoice, or quotation/delivery challan
 * straight to invoice). Omitted here = no convert action shown, not a
 * backend restriction. */
export const CONVERTIBLE_TARGETS: Partial<Record<DocumentType, DocumentType[]>> = {
  QUOTATION: ["SALES_ORDER", "TAX_INVOICE"],
  SALES_ORDER: ["TAX_INVOICE"],
  DELIVERY_CHALLAN: ["TAX_INVOICE"],
};

export const DOCUMENT_TYPE_LABELS: Record<DocumentType, string> = {
  QUOTATION: "Quotation",
  PROFORMA_INVOICE: "Proforma invoice",
  SALES_ORDER: "Sales order",
  DELIVERY_CHALLAN: "Delivery challan",
  TAX_INVOICE: "Tax invoice",
  POS_INVOICE: "POS invoice",
  CREDIT_NOTE: "Credit note",
  DEBIT_NOTE: "Debit note",
  SALES_RETURN: "Sales return",
  RECURRING_INVOICE: "Recurring invoice",
};

export type PaymentState = "PAID" | "PARTIAL" | "OVERDUE" | "UNPAID";

const RECEIVABLE_TYPES: DocumentType[] = ["TAX_INVOICE", "POS_INVOICE", "DEBIT_NOTE"];

/** Paid / part-paid / overdue / unpaid for a finalized receivable document;
 * null for anything that is not owed money (drafts, quotes, orders, credit notes…). */
export function paymentState(d: SalesDocument, today = new Date()): PaymentState | null {
  if (d.Status !== "FINALIZED" || !RECEIVABLE_TYPES.includes(d.DocumentType) || !d.GrandTotalAmount) return null;
  const total = Number(d.GrandTotalAmount.amount);
  const paid = Number(d.PaidAmount ?? "0");
  if (total <= 0) return null;
  if (paid >= total) return "PAID";
  if (d.DueDate && d.DueDate.slice(0, 10) < today.toISOString().slice(0, 10)) return "OVERDUE";
  return paid > 0 ? "PARTIAL" : "UNPAID";
}

export const PAYMENT_LABELS: Record<PaymentState, string> = { PAID: "Paid", PARTIAL: "Part paid", OVERDUE: "Overdue", UNPAID: "Unpaid" };
export const PAYMENT_TONES: Record<PaymentState, string> = { PAID: "positive", PARTIAL: "warning", OVERDUE: "negative", UNPAID: "warning" };
