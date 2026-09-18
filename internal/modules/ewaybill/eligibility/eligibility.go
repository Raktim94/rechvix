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
	}
	if c.Transport.DistanceKM.IsZero() {
		missing = append(missing, MissingInfo{Field: "distance_km", Reason: "transport distance not entered"})
	}
	for _, item := range c.Items {
		if item.HSNSACCode == "" {
			missing = append(missing, MissingInfo{Field: "items[" + item.LineRef + "].hsn_sac_code", Reason: "product is missing an HSN/SAC code"})
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
	}
	if c.Supplier.PostalCode == "" {
		missing = append(missing, MissingInfo{Field: "supplier.postal_code", Reason: "your business has no PIN code configured (Settings → Invoice branding)"})
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
