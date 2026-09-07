// Package printing is the sales module's document rendering engine (brief
// §19): PDF (via github.com/go-pdf/fpdf, the actively-maintained
// community successor to the now-quiet jung-kurt/gofpdf — a pure-Go
// library, no external binary/toolchain dependency, matching this
// project's "no external connection required to run your business" goal,
// docs/architecture.md's license section) and HTML (via the standard
// library's html/template, for an email/preview path) renderers for every
// print template brief §19 lists.
//
// This package only RENDERS numbers another module already computed — it
// never recalculates tax or totals itself (docs/architecture.md §5's
// "server recalculates and is authoritative" principle applies equally
// to "the print layer never recalculates").
package printing

import (
	"time"

	"rechvix/internal/modules/sales/domain"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/money"
)

// Template selects which of brief §19's document layouts to render.
type Template string

const (
	TemplateA4GSTInvoice    Template = "A4_GST_INVOICE"
	TemplateCompactInvoice  Template = "COMPACT_INVOICE"
	TemplateThermal80mm     Template = "THERMAL_80MM"
	TemplateThermal58mm     Template = "THERMAL_58MM"
	TemplateQuotation       Template = "QUOTATION"
	TemplatePurchaseOrder   Template = "PURCHASE_ORDER"
	TemplateDeliveryChallan Template = "DELIVERY_CHALLAN"
	TemplateReceipt         Template = "RECEIPT"
	TemplateStatement       Template = "STATEMENT"
	TemplateCreditNote      Template = "CREDIT_NOTE"
	TemplateDebitNote       Template = "DEBIT_NOTE"
)

// SellerInfo is the supplier-side header block — sourced from the legal
// entity/organisation, not the sales_documents row.
type SellerInfo struct {
	LegalName    string
	GSTIN        string
	AddressLines []string
	Phone        string
	Email        string
	Website      string
	LogoPNG      []byte // optional; nil renders no logo, not a broken image
	BankName     string
	BankAccount  string
	BankIFSC     string
	// UPIID is rendered as plain text next to the bank block ("UPI:
	// name@bank") — a scannable payment QR is a real, separate follow-up
	// (needs a QR-encoding library this package doesn't depend on yet),
	// not attempted here; text is still strictly more useful than the
	// blank line every invoice showed before this field existed.
	UPIID string
}

// PartyInfo is a customer/supplier-side block (billing or shipping).
type PartyInfo struct {
	Name         string
	GSTIN        string
	AddressLines []string
}

// LineItem is one rendered line — plain rendering-friendly fields, not
// domain.DocumentLine directly, so this package has no dependency on how
// the tax breakdown is actually computed, only on the already-computed
// result.
type LineItem struct {
	SNo           int
	Description   string
	HSNSAC        string
	Quantity      string
	UnitOfMeasure string
	Rate          string
	TaxableValue  string
	CGSTRate      string
	CGSTValue     string
	SGSTRate      string
	SGSTValue     string
	IGSTRate      string
	IGSTValue     string
	LineTotal     string
}

// InvoiceData is everything a template needs to render — assembled by
// app.BuildInvoiceData from a finalized sales.Document, its lines, and
// its taxation.TaxDocument/TaxLine/TaxComponent snapshot. Every value
// here is a pre-formatted string (already rounded/StringFixed by the
// caller): this package renders, it does not compute.
type InvoiceData struct {
	Seller            SellerInfo
	BillTo            PartyInfo
	ShipTo            PartyInfo
	DocumentTypeLabel string
	DocumentNumber    string
	IssueDate         time.Time
	DueDate           *time.Time
	PlaceOfSupply     string
	CustomerReference string
	Transporter       string
	VehicleNumber     string
	Lines             []LineItem
	SubtotalTaxable   string
	TotalCGST         string
	TotalSGST         string
	TotalIGST         string
	TotalCess         string
	RoundOff          string
	GrandTotal        string
	AmountInWords     string
	// PreviousBalance is a print-layer stub — no customer ledger exists
	// until Stage 6 (accounting). nil renders the "Previous Balance" row
	// as blank/omitted rather than a fabricated ₹0.00, so the printed
	// invoice never implies a ledger figure this system doesn't actually
	// track yet. Stage 6 supplies a real *money.Money here once the
	// ledger exists — no template change needed, just this field getting
	// populated by the caller.
	PreviousBalance    *money.Money
	Notes              string
	TermsAndConditions string
	// AuthorizedSignatoryName is printed under the "For Authorized
	// Signatory" line when set (organisation.LegalEntity's own field) —
	// blank renders exactly as before this field existed, just the
	// unsigned line.
	AuthorizedSignatoryName string
	// IRN/QR are e-Invoice fields (Stage 8) — nil today. Rendered only
	// when present, never a fabricated QR code (brief §19: "Never
	// generate a fake government QR code").
	IRN          string
	AckNumber    string
	SignedQRData []byte
	// EWBNumber/EWBValidUntil are e-Way Bill fields (Stage 8/8c) — nil/""
	// today unless a caller populates them, same "rendered only when
	// present, never fabricated" rule as IRN above. A printed invoice
	// with EWBNumber set is what a driver actually needs at a roadside
	// check, so this is not cosmetic.
	EWBNumber     string
	EWBValidUntil *time.Time
}

// PricingModeLabel is a small display helper — not business logic.
func PricingModeLabel(m taxdomain.PricingMode) string {
	if m == taxdomain.PricingInclusive {
		return "Inclusive of tax"
	}
	return "Exclusive of tax"
}

// DocumentTypeLabel renders a domain.DocumentType as the printed heading
// (e.g. "TAX INVOICE", "QUOTATION") — purely cosmetic string mapping.
func DocumentTypeLabel(t domain.DocumentType) string {
	switch t {
	case domain.DocQuotation:
		return "QUOTATION"
	case domain.DocProformaInvoice:
		return "PROFORMA INVOICE"
	case domain.DocSalesOrder:
		return "SALES ORDER"
	case domain.DocDeliveryChallan:
		return "DELIVERY CHALLAN"
	case domain.DocTaxInvoice, domain.DocPOSInvoice:
		return "TAX INVOICE"
	case domain.DocCreditNote:
		return "CREDIT NOTE"
	case domain.DocDebitNote:
		return "DEBIT NOTE"
	case domain.DocSalesReturn:
		return "SALES RETURN"
	case domain.DocRecurringInvoice:
		return "RECURRING INVOICE"
	default:
		return string(t)
	}
}
