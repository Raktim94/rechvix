// Package domain holds the sales module's entity types and repository
// interfaces (docs/architecture.md §2, §4, brief §5). One document family
// (QUOTATION, PROFORMA_INVOICE, SALES_ORDER, DELIVERY_CHALLAN, TAX_INVOICE,
// POS_INVOICE, CREDIT_NOTE, DEBIT_NOTE, SALES_RETURN, RECURRING_INVOICE)
// parameterized by DocumentType, mirroring purchases (Stage 4). No I/O, no
// framework imports.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/money"
)

type DocumentType string

const (
	DocQuotation        DocumentType = "QUOTATION"
	DocProformaInvoice  DocumentType = "PROFORMA_INVOICE"
	DocSalesOrder       DocumentType = "SALES_ORDER"
	DocDeliveryChallan  DocumentType = "DELIVERY_CHALLAN"
	DocTaxInvoice       DocumentType = "TAX_INVOICE"
	DocPOSInvoice       DocumentType = "POS_INVOICE"
	DocCreditNote       DocumentType = "CREDIT_NOTE"
	DocDebitNote        DocumentType = "DEBIT_NOTE"
	DocSalesReturn      DocumentType = "SALES_RETURN"
	DocRecurringInvoice DocumentType = "RECURRING_INVOICE"
)

func ValidDocumentType(t DocumentType) bool {
	switch t {
	case DocQuotation, DocProformaInvoice, DocSalesOrder, DocDeliveryChallan, DocTaxInvoice,
		DocPOSInvoice, DocCreditNote, DocDebitNote, DocSalesReturn, DocRecurringInvoice:
		return true
	default:
		return false
	}
}

// StockAffecting reports whether finalizing a document of this type posts
// stock_movements. TAX_INVOICE/POS_INVOICE/DELIVERY_CHALLAN dispatch goods
// (stock out); SALES_RETURN brings goods back (stock in). QUOTATION/
// PROFORMA_INVOICE/SALES_ORDER/RECURRING_INVOICE are commitments or
// templates, not fulfillment — mirrors purchases.StockAffecting's
// PURCHASE_ORDER-is-a-commitment reasoning. CREDIT_NOTE/DEBIT_NOTE are
// modeled as pure billing adjustments here (Stage 6 accounting territory);
// a credit note that represents an actual physical return should use
// SALES_RETURN instead, which already exists for exactly that case.
func StockAffecting(t DocumentType) bool {
	switch t {
	case DocTaxInvoice, DocPOSInvoice, DocDeliveryChallan, DocSalesReturn:
		return true
	default:
		return false
	}
}

// MovementTypeFor returns the inventory movement type StockAffecting
// finalize should post for t.
func MovementTypeFor(t DocumentType) string {
	if t == DocSalesReturn {
		return "SALE_RETURN"
	}
	return "SALE"
}

// RevenueAffecting reports whether finalizing a document of this type
// posts an accounting journal (Stage 6, docs/adr/0003-accounting-integration-point.md).
// TAX_INVOICE/POS_INVOICE/CREDIT_NOTE/DEBIT_NOTE/SALES_RETURN/RECURRING_INVOICE
// are real billing events; QUOTATION/PROFORMA_INVOICE/SALES_ORDER/
// DELIVERY_CHALLAN are commitments or goods-movement documents with no
// revenue recognized yet — mirrors StockAffecting's classification
// reasoning but is a distinct set (a delivery challan moves stock without
// billing; a credit note bills without moving stock).
func RevenueAffecting(t DocumentType) bool {
	switch t {
	case DocTaxInvoice, DocPOSInvoice, DocCreditNote, DocDebitNote, DocSalesReturn, DocRecurringInvoice:
		return true
	default:
		return false
	}
}

// ReducesReceivable reports whether finalizing a document of this type
// should post with reversed polarity (Cr Accounts Receivable / Dr Sales +
// Dr Tax Payable) instead of the normal sale direction — a credit note or
// a sales return both reduce what the customer owes, unlike a tax invoice,
// POS sale, debit note, or recurring invoice, which all increase it.
func ReducesReceivable(t DocumentType) bool {
	return t == DocCreditNote || t == DocSalesReturn
}

type DocumentStatus string

const (
	StatusDraft     DocumentStatus = "DRAFT"
	StatusFinalized DocumentStatus = "FINALIZED"
	StatusCancelled DocumentStatus = "CANCELLED"
)

// Document is a sales_documents row — the full header shape brief §5
// specifies. Pointer fields are genuinely optional (e.g. a POS sale may
// have no PriceListID; a quotation has no DueDate).
type Document struct {
	ID                        uuid.UUID
	OrganisationID            uuid.UUID
	LegalEntityID             uuid.UUID
	BranchID                  uuid.UUID
	WarehouseID               uuid.UUID
	CustomerPartyID           uuid.UUID
	DocumentType              DocumentType
	DocumentNumber            string
	ReferenceDocumentID       *uuid.UUID
	Status                    DocumentStatus
	IssueDate                 time.Time
	DueDate                   *time.Time
	SupplyDate                *time.Time
	BillingAddressID          *uuid.UUID
	ShippingAddressID         *uuid.UUID
	CustomerTaxRegistrationID *uuid.UUID
	PlaceOfSupplyStateCode    string
	SalespersonUserID         *uuid.UUID
	PriceListID               *uuid.UUID
	CurrencyCode              string
	BaseCurrencyCode          string
	ExchangeRate              decimal.Decimal
	PricingMode               taxdomain.PricingMode
	CustomerReference         string
	Transporter               string
	VehicleNumber             string
	ShippingTerms             string
	Notes                     string
	TermsAndConditions        string
	PaymentTermsDays          int
	TaxDocumentID             *uuid.UUID
	GrandTotalAmount          *money.Money
	CreatedBy                 uuid.UUID
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	FinalizedAt               *time.Time
}

type DocumentLine struct {
	ID                 uuid.UUID
	OrganisationID     uuid.UUID
	SalesDocumentID    uuid.UUID
	LineNumber         int
	ProductVariantID   uuid.UUID
	UnitID             uuid.UUID
	Quantity           decimal.Decimal
	UnitPrice          money.Money
	LineDiscountAmount money.Money
	// HSNSACCode is snapshotted from the product at add-line time (brief
	// §55) — a later catalogue edit must not change a finalized document's
	// tax classification.
	HSNSACCode string
	LineTotal  money.Money
	BatchCode  string
	SerialCode string
	CreatedAt  time.Time
}

type DocumentRepository interface {
	Create(ctx context.Context, d *Document) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Document, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID, documentType *DocumentType) ([]*Document, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status DocumentStatus, finalizedAt *time.Time) error
	// UpdateFinalizedTotals stamps the tax snapshot pointer and headline
	// grand total onto a document being finalized, in the same
	// transaction as UpdateStatus.
	UpdateFinalizedTotals(ctx context.Context, id, taxDocumentID uuid.UUID, grandTotal decimal.Decimal) error
}

type DocumentLineRepository interface {
	Create(ctx context.Context, l *DocumentLine) error
	ListByDocument(ctx context.Context, documentID uuid.UUID) ([]*DocumentLine, error)
	// GetByID/Update/Delete only ever apply to a DRAFT document's line —
	// the billing counter's own "fix a quantity" / "remove an item I
	// scanned twice" actions, which had no path at all until these
	// existed (a cashier had to abandon the whole draft and start over
	// for even a one-line mistake). Service.UpdateLine/DeleteLine are
	// what enforce the DRAFT-only rule; these are just plain CRUD.
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*DocumentLine, error)
	Update(ctx context.Context, l *DocumentLine) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}
