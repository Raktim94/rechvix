// Package domain holds the purchases module's entity types and
// repository interfaces (docs/architecture.md §2, §4, brief §13). One
// document family (PURCHASE_ORDER, GOODS_RECEIPT, PURCHASE_INVOICE,
// PURCHASE_RETURN, DEBIT_NOTE) parameterized by DocumentType, mirroring
// the pattern the architecture doc specifies for sales.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/platform/money"
)

type DocumentType string

const (
	DocPurchaseOrder   DocumentType = "PURCHASE_ORDER"
	DocGoodsReceipt    DocumentType = "GOODS_RECEIPT"
	DocPurchaseInvoice DocumentType = "PURCHASE_INVOICE"
	DocPurchaseReturn  DocumentType = "PURCHASE_RETURN"
	DocDebitNote       DocumentType = "DEBIT_NOTE"
)

func ValidDocumentType(t DocumentType) bool {
	switch t {
	case DocPurchaseOrder, DocGoodsReceipt, DocPurchaseInvoice, DocPurchaseReturn, DocDebitNote:
		return true
	default:
		return false
	}
}

// StockAffecting reports whether finalizing a document should post
// stock_movements. GOODS_RECEIPT and PURCHASE_RETURN always do.
// PURCHASE_INVOICE does too, unless referenceDocumentID is set — i.e. it
// is billing against goods already received through a separate
// GOODS_RECEIPT, so posting stock again here would double-count what the
// GRN already recorded (docs/adr/0003-accounting-integration-point.md
// §3's original 3-way-match reasoning). The shipped app's only
// purchase-creation screen (apps/web PurchasesPage) has no GRN step and
// always creates a standalone PURCHASE_INVOICE with no reference — for
// that real, reachable flow this means simply "the purchase you just
// recorded puts stock on the shelf." A PURCHASE_ORDER is a commitment,
// not a receipt, and never affects stock.
func StockAffecting(t DocumentType, referenceDocumentID *uuid.UUID) bool {
	if t == DocGoodsReceipt || t == DocPurchaseReturn {
		return true
	}
	return t == DocPurchaseInvoice && referenceDocumentID == nil
}

// AccountingAffecting reports whether finalizing a document of this type
// posts an accounting journal (Stage 6, docs/adr/0003-accounting-integration-point.md).
// PURCHASE_INVOICE/PURCHASE_RETURN/DEBIT_NOTE are billed amounts — a
// PURCHASE_ORDER is a commitment and a GOODS_RECEIPT records physical
// receipt at cost (already captured in stock_balances since Stage 4), not
// a confirmed payable; booking the liability twice (once at GRN, once at
// invoice) would double-count if the two amounts ever differ, so the
// payable books only when the actual bill is finalized.
func AccountingAffecting(t DocumentType) bool {
	return t == DocPurchaseInvoice || t == DocPurchaseReturn || t == DocDebitNote
}

// ReducesPayable reports whether finalizing a document of this type
// should post with reversed polarity (Dr Accounts Payable / Cr Purchases)
// instead of the normal purchase direction — a purchase return or a
// supplier debit note both reduce what's owed to the supplier.
func ReducesPayable(t DocumentType) bool {
	return t == DocPurchaseReturn || t == DocDebitNote
}

type DocumentStatus string

const (
	StatusDraft     DocumentStatus = "DRAFT"
	StatusFinalized DocumentStatus = "FINALIZED"
	StatusCancelled DocumentStatus = "CANCELLED"
)

type Document struct {
	ID                    uuid.UUID
	OrganisationID        uuid.UUID
	BranchID              uuid.UUID
	WarehouseID           uuid.UUID
	SupplierPartyID       uuid.UUID
	DocumentType          DocumentType
	DocumentNumber        string
	ReferenceDocumentID   *uuid.UUID
	Status                DocumentStatus
	SupplierInvoiceNumber string
	SupplierInvoiceDate   *time.Time
	DocumentDate          time.Time
	CurrencyCode          string
	Notes                 string
	// TaxDocumentID points at the taxation module's tax_documents row
	// (reference_type='purchase_document') once FinalizeDocument computes
	// one — nil for a document whose type doesn't book a tax liability/
	// ITC (PURCHASE_ORDER) or whose supplier has no GST registration on
	// file (migrations/0038's own comment: a denormalized quick-access
	// pointer, not the authoritative store).
	TaxDocumentID *uuid.UUID
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	FinalizedAt   *time.Time
}

type DocumentLine struct {
	ID                 uuid.UUID
	OrganisationID     uuid.UUID
	PurchaseDocumentID uuid.UUID
	LineNumber         int
	ProductVariantID   uuid.UUID
	UnitID             uuid.UUID
	Quantity           decimal.Decimal
	// UnitPrice/LineTotal share the parent Document's CurrencyCode — see
	// internal/modules/pricing's identical pattern for why the amount is
	// stored bare and Money is reconstituted with the joined currency at
	// scan time, rather than duplicating currency_code on every line.
	UnitPrice money.Money
	LineTotal money.Money
	BatchCode string
	// HSNSACCode is snapshotted from the product at add-line time
	// (migrations/0038), same rationale as sales_document_lines'
	// identical field: a finalized document's tax calculation must not
	// silently change if the catalogue's HSN/SAC is edited later.
	HSNSACCode string
	CreatedAt  time.Time
}

type DocumentRepository interface {
	Create(ctx context.Context, d *Document) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Document, error)
	// legalEntityIDs is a company filter: nil means no restriction (every
	// company), a non-nil (possibly empty) slice restricts to exactly
	// those legal entities — see permissions.ResolveLegalEntityFilter.
	ListByOrganisation(ctx context.Context, orgID uuid.UUID, documentType *DocumentType, legalEntityIDs []uuid.UUID) ([]*Document, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status DocumentStatus, finalizedAt *time.Time) error
	// UpdateTaxDocument stamps the tax snapshot pointer onto a document
	// being finalized, in the same transaction as UpdateStatus — mirrors
	// sales.DocumentRepository.UpdateFinalizedTotals, minus the grand
	// total half (purchase_documents has no such denormalized column;
	// PurchasesPage.tsx already sums LineTotal client-side for display).
	UpdateTaxDocument(ctx context.Context, id, taxDocumentID uuid.UUID) error
	// NextNumber atomically allocates and returns the next sequence value
	// for (orgID, docType) via an UPDATE ... RETURNING on
	// purchase_document_counters, so two concurrent document creations
	// for the same organisation and type can never receive the same
	// number (Scenario I's building block).
	NextNumber(ctx context.Context, orgID uuid.UUID, docType DocumentType) (int64, error)
}

type DocumentLineRepository interface {
	Create(ctx context.Context, l *DocumentLine) error
	ListByDocument(ctx context.Context, documentID uuid.UUID) ([]*DocumentLine, error)
}
