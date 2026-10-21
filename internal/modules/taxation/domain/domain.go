// Package domain is the generic (country-agnostic) tax calculation
// contract (docs/architecture.md §5). Nothing here knows about GST,
// CGST/SGST/IGST, or any other regime-specific concept — that lives in
// internal/modules/gstindia, which implements TaxEngine. No I/O, no
// framework imports.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/platform/money"
)

// PricingMode says whether a TaxableLine's Amount already contains tax
// (INCLUSIVE — the "sticker price") or excludes it (EXCLUSIVE — tax is
// added on top). The engine backs out the taxable value for INCLUSIVE
// lines and uses the amount as-is for EXCLUSIVE lines (brief §6).
type PricingMode string

const (
	PricingInclusive PricingMode = "INCLUSIVE"
	PricingExclusive PricingMode = "EXCLUSIVE"
)

// SupplyType classifies who a supply is to, for reporting purposes (Stage
// 7 GSTR-1/3B-oriented exports). It does not itself change the tax
// arithmetic — a business wanting SEZ/export supplies zero-rated
// configures that HSN's tax_rate_master row with classification
// ZERO_RATED; this engine does not silently override a rate based on
// SupplyType, because that would be inventing a government rule rather
// than applying a configured one (brief Rule 2).
type SupplyType string

const (
	SupplyB2B    SupplyType = "B2B"
	SupplyB2C    SupplyType = "B2C"
	SupplyExport SupplyType = "EXPORT"
	SupplySEZ    SupplyType = "SEZ"
)

// PlaceOfSupply is where a supply is deemed to occur for tax purposes —
// for India GST this is a gst_state_codes.code; a future country engine
// may key this differently (a plain string keeps this package regime-
// agnostic).
type PlaceOfSupply struct {
	StateCode string
}

// TaxableLine is one line of a document being taxed — the generic input
// shape docs/architecture.md §5 sketches. HSNSACCode is deliberately just
// a string, not a foreign key into any specific country's rate table: the
// generic taxation package doesn't know how a country resolves a code to
// a rate.
type TaxableLine struct {
	LineRef     string
	HSNSACCode  string
	Amount      money.Money
	PricingMode PricingMode
}

// TaxCalculationInput is everything a TaxEngine needs to calculate tax for
// a document. SupplierStateCode/SupplyPlace are GST-shaped fields today
// (a future VAT engine would ignore them) but kept on the generic input
// rather than pushed into a regime-specific wrapper type, since every
// engine this project currently plans to support (India GST first, a
// future country pack later) needs *some* notion of "where is this
// supply happening" — see docs/architecture.md §18 for the general
// principle this follows.
type TaxCalculationInput struct {
	OrganisationID    uuid.UUID
	Lines             []TaxableLine
	SupplierStateCode string
	SupplyPlace       PlaceOfSupply
	DocumentDate      time.Time
	SupplyType        SupplyType
	ReverseCharge     bool
	CurrencyCode      string

	// Multi-jurisdiction fields (additive; zero values keep the original India
	// GST behaviour). SupplierCountry selects the tax regime: "" or "IN" is
	// India GST; "ES", "GB" and the EU member states use VAT; "US" uses state
	// sales tax. See internal/modules/taxregimes.
	SupplierCountry string // ISO 3166-1 alpha-2 of the selling legal entity
	CustomerCountry string // ISO 3166-1 alpha-2 of the customer; "" means domestic
	// SupplyRegion is the destination subdivision as an ISO 3166-2 code, e.g.
	// "US-CA", "ES-CN", "GB-NIR". Required for US sales tax; used by the VAT
	// packs to reject territories whose rules are not implemented.
	SupplyRegion string
	// CustomerTaxRegistered is true when the customer has a business tax
	// identifier (e.g. an EU VAT number). The identifier is NOT validated
	// against VIES here; the caller is responsible for that.
	CustomerTaxRegistered bool
	// CustomerTaxExempt is true when the customer holds a valid exemption or
	// resale certificate (US sales tax).
	CustomerTaxExempt bool
	// EquivalenceSurcharge is true when the customer is under Spain's
	// "recargo de equivalencia" regime, adding the surcharge to Spanish IVA.
	EquivalenceSurcharge bool
	// CollectionRegions lists the US states ("US-CA") where the seller is
	// registered to collect sales tax. Tax is charged only in these states.
	CollectionRegions []string
}

// TaxComponent is one named amount within a line's total tax (CGST, SGST,
// IGST, UTGST, CESS today — an open string, not an enum, so a future
// regime's component types don't require a schema/type change; brief §18).
type TaxComponent struct {
	Type   string
	Rate   decimal.Decimal // a percentage, e.g. 9 for "9%" — not a Money value, a rate has no currency
	Amount money.Money
}

// TaxLineResult is one TaxableLine's calculated outcome.
type TaxLineResult struct {
	LineRef        string
	HSNSACCode     string
	PricingMode    PricingMode
	GrossAmount    money.Money
	TaxableAmount  money.Money
	TotalTax       money.Money
	Classification string
	Components     []TaxComponent
	// RateMasterID identifies which rate-master row (a gstindia concept,
	// but the id itself is regime-agnostic) this line resolved to — needed
	// by the app layer to persist the tax_lines.tax_rate_master_id
	// snapshot reference (brief §7).
	RateMasterID uuid.UUID
	// Regime and RatePackVersion identify, for non-India regimes, which tax
	// regime and which dated rate table produced this line (stored with the
	// snapshot because such lines have no tax_rate_master row).
	Regime          string
	RatePackVersion string
}

// TaxCalculationResult is a full document's calculated outcome.
type TaxCalculationResult struct {
	Lines              []TaxLineResult
	TotalTaxableAmount money.Money
	TotalTaxAmount     money.Money
	GrandTotal         money.Money
}

// TaxEngine is the one interface every tax regime implements
// (docs/architecture.md §5). IndiaGSTEngine (internal/modules/gstindia) is
// the only implementation today.
type TaxEngine interface {
	Calculate(ctx context.Context, in TaxCalculationInput) (TaxCalculationResult, error)
}

// TaxDocument/TaxLine/TaxComponentRow are the persisted snapshot of a
// TaxCalculationResult (docs/architecture.md §5, brief §55). Separate from
// the calculation-result types above because these carry storage-only
// fields (IDs, the tax_document header's reference back to whatever
// business document triggered the calculation) that a pure calculation
// doesn't need.
type TaxDocument struct {
	ID                uuid.UUID
	OrganisationID    uuid.UUID
	ReferenceType     string
	ReferenceID       *uuid.UUID
	DocumentDate      time.Time
	CurrencyCode      string
	SupplierStateCode string
	PlaceOfSupplyCode string
	// Non-India regimes: where the supply is from/to (empty for India GST).
	SupplierCountry    string
	CustomerCountry    string
	SupplyRegion       string
	SupplyType         SupplyType
	ReverseCharge      bool
	TotalTaxableAmount money.Money
	TotalTaxAmount     money.Money
	GrandTotal         money.Money
	CreatedAt          time.Time
}

type TaxLine struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	TaxDocumentID  uuid.UUID
	LineRef        string
	HSNSACCode     string
	PricingMode    PricingMode
	GrossAmount    money.Money
	TaxableAmount  money.Money
	TotalTaxAmount money.Money
	Classification string
	// TaxRateMasterID is uuid.Nil for non-India regimes, which record
	// Regime and RatePackVersion instead (migration 0045).
	TaxRateMasterID uuid.UUID
	Regime          string
	RatePackVersion string
	CreatedAt       time.Time
}

type TaxComponentRow struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	TaxLineID      uuid.UUID
	ComponentType  string
	Rate           decimal.Decimal
	Amount         money.Money
	CreatedAt      time.Time
}

// --- Repository interfaces (persistence for the calculation snapshot) ---

type TaxDocumentRepository interface {
	Create(ctx context.Context, d *TaxDocument) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*TaxDocument, error)
	GetByReference(ctx context.Context, orgID uuid.UUID, referenceType string, referenceID uuid.UUID) (*TaxDocument, error)
}

type TaxLineRepository interface {
	Create(ctx context.Context, l *TaxLine) error
	ListByDocument(ctx context.Context, orgID, taxDocumentID uuid.UUID) ([]*TaxLine, error)
}

type TaxComponentRepository interface {
	Create(ctx context.Context, c *TaxComponentRow) error
	ListByLine(ctx context.Context, orgID, taxLineID uuid.UUID) ([]*TaxComponentRow, error)
}
