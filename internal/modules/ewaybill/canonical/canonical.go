// Package canonical is the single data model both e-Way Bill production
// modes (FREE_PORTAL and AUTOMATIC_API) build from (docs/architecture.md
// §9b — "one canonical model, not two parallel schemas"). Build is pure:
// it takes already-fetched domain objects and returns a CanonicalEWayBill,
// no I/O, no database access — the app layer is responsible for fetching
// (from the finalized invoice's immutable snapshot, never live master
// data) and for persisting the result. This keeps the mapping logic
// itself trivially unit-testable.
package canonical

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Party is the supplier/recipient/dispatch/delivery shape shared across
// all four roles a canonical e-Way Bill carries.
//
// NOTE (known gap, documented rather than silently worked around):
// legal_entities only has a single free-text Address field, no
// structured line1/line2/city — migrations/0043 added a dedicated
// Pincode column specifically because the e-Way Bill portal validates it
// as a real 6-digit number, not reliably extractable from free text, so
// PostalCode IS populated for the Supplier/DispatchFrom roles (from
// LegalEntity.Pincode, Settings → Invoice branding). AddressLine1/
// AddressLine2/City remain best-effort/empty for those two roles until
// legal_entities gets a structured address (a real, bounded follow-up,
// not this stage's scope to redesign organisation's schema). StateCode
// is always populated where available and is what the eligibility/
// intra-vs-inter-state logic actually depends on functionally.
type Party struct {
	LegalName    string `json:"legal_name"`
	TradeName    string `json:"trade_name,omitempty"`
	GSTIN        string `json:"gstin,omitempty"`
	StateCode    string `json:"state_code"`
	AddressLine1 string `json:"address_line1,omitempty"`
	AddressLine2 string `json:"address_line2,omitempty"`
	City         string `json:"city,omitempty"`
	PostalCode   string `json:"postal_code,omitempty"`
	Phone        string `json:"phone,omitempty"`
}

type Item struct {
	LineRef       string          `json:"line_ref"`
	Description   string          `json:"description"`
	HSNSACCode    string          `json:"hsn_sac_code"`
	Quantity      decimal.Decimal `json:"quantity"`
	UnitCode      string          `json:"unit_code"`
	TaxableAmount decimal.Decimal `json:"taxable_amount"`
	// GSTRate is the COMBINED rate (e.g. 18 for 18% GST), kept for
	// callers that only need a single display figure — same convention
	// as gstindia.TaxRateMaster.GSTRate. CGSTRate/SGSTRate/IGSTRate below
	// are the actual per-component split the government e-Way Bill
	// portal's itemList schema requires (cgstRate/sgstRate/igstRate) —
	// the portal mapper (portal/v1) needs the split, not the combined
	// figure; a real bug this codebase shipped once already (see
	// portal/v1/mapper.go's history) was collapsing this split into one
	// field, which no real portal upload accepts.
	GSTRate  decimal.Decimal `json:"gst_rate"`
	CGSTRate decimal.Decimal `json:"cgst_rate"`
	SGSTRate decimal.Decimal `json:"sgst_rate"`
	IGSTRate decimal.Decimal `json:"igst_rate"`
	CessRate decimal.Decimal `json:"cess_rate"`
}

type TaxTotals struct {
	TaxableValue decimal.Decimal `json:"taxable_value"`
	CGST         decimal.Decimal `json:"cgst"`
	SGST         decimal.Decimal `json:"sgst"`
	IGST         decimal.Decimal `json:"igst"`
	CESS         decimal.Decimal `json:"cess"`
	GrandTotal   decimal.Decimal `json:"grand_total"`
}

type Transport struct {
	Mode            string          `json:"mode,omitempty"`
	VehicleNumber   string          `json:"vehicle_number,omitempty"`
	TransporterID   string          `json:"transporter_id,omitempty"`
	TransporterName string          `json:"transporter_name,omitempty"`
	DistanceKM      decimal.Decimal `json:"distance_km"`
}

// CanonicalEWayBill is the immutable-once-captured shape both the
// FREE_PORTAL and AUTOMATIC_API mappers consume. SnapshotTakenAt records
// when this was built — callers persisting it (ewaybill_records.
// canonical_snapshot) should treat that persisted copy, not a fresh
// Build() call, as authoritative for anything after the first capture.
type CanonicalEWayBill struct {
	SalesDocumentID uuid.UUID `json:"sales_document_id"`
	InvoiceNumber   string    `json:"invoice_number"`
	InvoiceDate     time.Time `json:"invoice_date"`
	DocumentType    string    `json:"document_type"`
	SupplyPlaceCode string    `json:"supply_place_code"`

	Supplier     Party `json:"supplier"`
	Recipient    Party `json:"recipient"`
	DispatchFrom Party `json:"dispatch_from"`
	ShipTo       Party `json:"ship_to"`

	Items     []Item    `json:"items"`
	Tax       TaxTotals `json:"tax"`
	Transport Transport `json:"transport"`

	SnapshotTakenAt time.Time `json:"snapshot_taken_at"`
}

// BuildInput bundles every already-fetched piece Build needs. Each field
// is data the caller must resolve from the invoice's own finalized
// snapshot (sales.GetDocumentForOtherModule, taxation.GetByReference) or
// from party/legal-entity master data AS OF THE MOMENT OF THIS CALL — it
// is Build's caller's responsibility to only call Build once, at first
// preparation, and persist the result; a later "regenerate" must
// re-serialize the persisted snapshot instead of calling Build again
// against what may now be different live data. See
// docs/architecture.md §9b.
type BuildInput struct {
	SalesDocumentID uuid.UUID
	InvoiceNumber   string
	InvoiceDate     time.Time
	DocumentType    string
	SupplyPlaceCode string

	Supplier     Party
	Recipient    Party
	DispatchFrom Party
	ShipTo       Party

	Items []Item
	Tax   TaxTotals

	TransportMode   string
	VehicleNumber   string
	TransporterID   string
	TransporterName string
	DistanceKM      decimal.Decimal

	Now func() time.Time
}

func Build(in BuildInput) CanonicalEWayBill {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	return CanonicalEWayBill{
		SalesDocumentID: in.SalesDocumentID,
		InvoiceNumber:   in.InvoiceNumber,
		InvoiceDate:     in.InvoiceDate,
		DocumentType:    in.DocumentType,
		SupplyPlaceCode: in.SupplyPlaceCode,
		Supplier:        in.Supplier,
		Recipient:       in.Recipient,
		DispatchFrom:    in.DispatchFrom,
		ShipTo:          in.ShipTo,
		Items:           in.Items,
		Tax:             in.Tax,
		Transport: Transport{
			Mode: in.TransportMode, VehicleNumber: in.VehicleNumber,
			TransporterID: in.TransporterID, TransporterName: in.TransporterName,
			DistanceKM: in.DistanceKM,
		},
		SnapshotTakenAt: now(),
	}
}

// ConsignmentValue is what eligibility rules and the portal export compare
// against a threshold — the invoice's grand total (docs/architecture.md
// §9b: eligibility considers "invoice value" among other factors).
func (c CanonicalEWayBill) ConsignmentValue() decimal.Decimal {
	return c.Tax.GrandTotal
}
