// Package eligibility implements the versioned e-Way Bill applicability
// rule engine (docs/architecture.md §9b). Evaluate is pure — it takes
// already-loaded rules and a canonical invoice, no I/O — so the threshold
// logic itself is unit-testable without a database.
package eligibility

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"rechvix/internal/modules/ewaybill/canonical"
)

// Requirement is EvaluateEWayBillRequirement's result (docs/architecture.md
// §9b's exact four-value list).
type Requirement string

const (
	NotRequired      Requirement = "NOT_REQUIRED"
	Ready            Requirement = "READY"
	NeedsInformation Requirement = "NEEDS_INFORMATION"
	Required         Requirement = "REQUIRED"
)

// Rule is one versioned threshold row (ewaybill_eligibility_rules). A nil
// StateCode is the national default; a non-nil one overrides it for that
// place-of-supply state. NOT a hardcoded Go constant — see
// migrations/0028_ewaybill_free_portal.up.sql's seed-row comment: this
// starting default has not been verified against current live CBIC/GST
// notifications and businesses must confirm the applicable threshold
// themselves (brief Rule 2's "never invent tax rules" applies equally to
// this government-set logistics threshold).
type Rule struct {
	StateCode           *string
	MinConsignmentValue decimal.Decimal
	ValidFrom           time.Time
	ValidUntil          *time.Time
}

func (r Rule) appliesOn(date time.Time) bool {
	if date.Before(r.ValidFrom) {
		return false
	}
	if r.ValidUntil != nil && date.After(*r.ValidUntil) {
		return false
	}
	return true
}

// Repository loads the rule set an organisation's e-Way Bill evaluation
// should consider. Rules are global reference data (like Stage 5a's
// gst_state_codes), not per-organisation — see the migration's comment.
type Repository interface {
	ListActive(ctx context.Context) ([]Rule, error)
}

// missingField names one piece of information Evaluate found absent that
// would otherwise be required to prepare an e-Way Bill.
type MissingInfo struct {
	Field  string
	Reason string
}

// MaxInvoiceAgeForGeneration is the real government rule, effective
// 2026-01-01: the e-Way Bill portal refuses to generate an e-Way Bill
// against a base document (invoice/bill of supply/delivery challan) older
// than 180 days from its date. Verified against current (2026) sources
// during the deliverable review this constant was added for — not a
// number this codebase invented. A document past this age can still be
// evaluated (so the UI can explain *why* it's blocked), but Evaluate
// never reports it Ready.
const MaxInvoiceAgeForGeneration = 180 * 24 * time.Hour

// reconciliationTolerance is a heuristic, not a government-specified
// figure (none was found) — a deliberately generous ₹1 grace band for
// the taxable-value-plus-tax-equals-grand-total check below, matching
// the order of magnitude gstindia.Engine's own doc comment already
// documents as normal rounding drift between an intra-state split and
// an inter-state whole.
var reconciliationTolerance = decimal.NewFromInt(1)

// Evaluate implements EvaluateEWayBillRequirement(invoice) (docs/
// architecture.md §9b). rules should be every currently-loaded Rule
// (typically all of Repository.ListActive's result); Evaluate itself
// picks the one applicable to c.InvoiceDate and c.SupplyPlaceCode —
// state-specific rule first, national default as fallback. now is
// injected (not time.Now() called internally) so this stays pure and
// testable without a clock dependency, same convention as the rest of
// this package.
func Evaluate(rules []Rule, c canonical.CanonicalEWayBill, now time.Time) (Requirement, []MissingInfo) {
	rule, ok := selectRule(rules, c.SupplyPlaceCode, c.InvoiceDate)
	if !ok {
		// No applicable rule at all is a data problem, not "not required" —
		// fail toward asking a human rather than silently skipping a
		// legally-required e-Way Bill (brief Rule 2's spirit: never guess).
		return NeedsInformation, []MissingInfo{{Field: "eligibility_rule", Reason: "no e-Way Bill threshold rule is configured for this date/state"}}
	}

	if c.ConsignmentValue().LessThan(rule.MinConsignmentValue) {
		return NotRequired, nil
	}

	var missing []MissingInfo
	if age := now.Sub(c.InvoiceDate); age > MaxInvoiceAgeForGeneration {
		// A real portal rejection waiting to happen, not a soft warning —
		// surfaced as MissingInfo (not silently "Ready") so PrepareUpload's
		// existing "Requirement != Ready" guard blocks it, same as any
		// other incomplete field.
		missing = append(missing, MissingInfo{Field: "invoice_date", Reason: "this document is older than 180 days — the government portal will not generate an e-Way Bill against it"})
	}
	if c.Transport.VehicleNumber == "" {
		missing = append(missing, MissingInfo{Field: "vehicle_number", Reason: "no vehicle selected"})
	} else if !isValidVehicleNumber(c.Transport.VehicleNumber) {
		missing = append(missing, MissingInfo{Field: "vehicle_number", Reason: "vehicle number doesn't look like a valid Indian registration (e.g. MH12AB1234)"})
	}
	if c.Transport.DistanceKM.IsZero() {
		missing = append(missing, MissingInfo{Field: "distance_km", Reason: "transport distance not entered"})
	}
	for _, item := range c.Items {
		if item.HSNSACCode == "" {
			missing = append(missing, MissingInfo{Field: "items[" + item.LineRef + "].hsn_sac_code", Reason: "product is missing an HSN/SAC code"})
		} else if !isValidHSN(item.HSNSACCode) {
			missing = append(missing, MissingInfo{Field: "items[" + item.LineRef + "].hsn_sac_code", Reason: "HSN/SAC code must be 4–8 digits"})
		}
	}
	if c.ShipTo.StateCode == "" {
		missing = append(missing, MissingInfo{Field: "ship_to.state_code", Reason: "ship-to state is not resolved"})
	}
	// A blank fromGstin/fromPincode is a portal-rejection waiting to
	// happen, exactly like the stale invoice_date check above — surfaced
	// here so PrepareUpload's "Requirement != Ready" guard blocks it
	// instead of silently producing a file NIC's actual "Generate e-Way
	// Bill" schema requires these on.
	if c.Supplier.GSTIN == "" {
		missing = append(missing, MissingInfo{Field: "supplier.gstin", Reason: "your business has no GSTIN configured (Settings → Legal entity)"})
	} else if !isValidGSTIN(c.Supplier.GSTIN) {
		missing = append(missing, MissingInfo{Field: "supplier.gstin", Reason: "your business's GSTIN doesn't look valid — check Settings → Legal entity"})
	}
	if c.Supplier.PostalCode == "" {
		missing = append(missing, MissingInfo{Field: "supplier.postal_code", Reason: "your business has no PIN code configured (Settings → Invoice branding)"})
	} else if !isValidPincode(c.Supplier.PostalCode) {
		missing = append(missing, MissingInfo{Field: "supplier.postal_code", Reason: "your business's PIN code doesn't look valid — check Settings → Invoice branding"})
	}
	// Recipient/ship-to GSTIN are genuinely optional (a real B2C sale has
	// none) — only validated when actually present, never required.
	if c.Recipient.GSTIN != "" && !isValidGSTIN(c.Recipient.GSTIN) {
		missing = append(missing, MissingInfo{Field: "recipient.gstin", Reason: "customer's GSTIN doesn't look valid"})
	}
	if c.ShipTo.GSTIN != "" && !isValidGSTIN(c.ShipTo.GSTIN) {
		missing = append(missing, MissingInfo{Field: "ship_to.gstin", Reason: "ship-to party's GSTIN doesn't look valid"})
	}

	// Reconciliation: taxable value plus every tax component should equal
	// (or come very close to — a few paise/rupees of rounding drift
	// across many lines is normal, not a bug; gstindia.Engine's own doc
	// comment notes an intra-state split and an inter-state whole can
	// legitimately differ by about a currency unit) the grand total
	// already computed by the tax engine. A real mismatch means
	// something upstream is broken, not ordinary rounding — generating a
	// government filing document from inconsistent numbers is worse
	// than blocking it here.
	reconciled := c.Tax.TaxableValue.Add(c.Tax.CGST).Add(c.Tax.SGST).Add(c.Tax.IGST).Add(c.Tax.CESS)
	if !c.Tax.GrandTotal.IsZero() && reconciled.Sub(c.Tax.GrandTotal).Abs().GreaterThan(reconciliationTolerance) {
		missing = append(missing, MissingInfo{Field: "tax.grand_total", Reason: "taxable value plus tax doesn't add up to the grand total — this invoice's tax snapshot looks inconsistent"})
	}
	// CGST+SGST vs IGST must follow from comparing the supplier's state
	// to the place of supply (gstindia.Engine's own rule, engine.go:
	// intraState := SupplierStateCode == SupplyPlace.StateCode) — a
	// safety net for exactly the tax-treatment bug a real generated file
	// once surfaced (place of supply defaulting to the seller's own
	// state regardless of the actual customer, fixed at the source in
	// BillingPage): if the two ever disagree again for any reason, block
	// here rather than silently filing the wrong tax type.
	intraState := c.Supplier.StateCode != "" && c.Supplier.StateCode == c.SupplyPlaceCode
	if intraState && !c.Tax.IGST.IsZero() {
		missing = append(missing, MissingInfo{Field: "tax.igst", Reason: "IGST is set on what looks like an intra-state sale (same supplier and place-of-supply state) — expected CGST+SGST instead"})
	}
	if !intraState && c.Supplier.StateCode != "" && c.SupplyPlaceCode != "" && (!c.Tax.CGST.IsZero() || !c.Tax.SGST.IsZero()) {
		missing = append(missing, MissingInfo{Field: "tax.cgst_sgst", Reason: "CGST/SGST is set on what looks like an inter-state sale (different supplier and place-of-supply state) — expected IGST instead"})
	}

	if len(missing) > 0 {
		return NeedsInformation, missing
	}
	return Ready, nil
}

func selectRule(rules []Rule, stateCode string, on time.Time) (Rule, bool) {
	var stateMatch, nationalMatch *Rule
	for i := range rules {
		r := rules[i]
		if !r.appliesOn(on) {
			continue
		}
		if r.StateCode != nil && *r.StateCode == stateCode {
			stateMatch = &r
		}
		if r.StateCode == nil {
			nationalMatch = &r
		}
	}
	if stateMatch != nil {
		return *stateMatch, true
	}
	if nationalMatch != nil {
		return *nationalMatch, true
	}
	return Rule{}, false
}
