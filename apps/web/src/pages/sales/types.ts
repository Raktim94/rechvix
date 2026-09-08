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
  GrandTotalAmount: Money | null;
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
