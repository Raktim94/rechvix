package eligibility

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"rechvix/internal/modules/ewaybill/canonical"
)

func mkRule(stateCode *string, minValue string, from string, until *string) Rule {
	f, _ := time.Parse("2006-01-02", from)
	var u *time.Time
	if until != nil {
		t, _ := time.Parse("2006-01-02", *until)
		u = &t
	}
	return Rule{StateCode: stateCode, MinConsignmentValue: decimal.RequireFromString(minValue), ValidFrom: f, ValidUntil: u}
}

func mkBill(grandTotal string, supplyState string, vehicle, distance string, hsn string) canonical.CanonicalEWayBill {
	dist := decimal.Zero
	if distance != "" {
		dist = decimal.RequireFromString(distance)
	}
	items := []canonical.Item{{LineRef: "1", HSNSACCode: hsn, TaxableAmount: decimal.RequireFromString(grandTotal)}}
	return canonical.CanonicalEWayBill{
		InvoiceDate: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), SupplyPlaceCode: supplyState,
		Items: items, ShipTo: canonical.Party{StateCode: supplyState},
		// A real GSTIN/PIN code by default — every test below that isn't
		// specifically about a missing supplier field gets a "complete"
		// supplier, same as ShipTo above already being populated by
		// default so ship-to-state tests could stay focused on their own
		// one missing field.
		Supplier:  canonical.Party{GSTIN: "22AAAAA0000A1Z5", PostalCode: "700001"},
		Tax:       canonical.TaxTotals{GrandTotal: decimal.RequireFromString(grandTotal)},
		Transport: canonical.Transport{VehicleNumber: vehicle, DistanceKM: dist},
	}
}

func TestEvaluate_BelowThreshold_NotRequired(t *testing.T) {
	rules := []Rule{mkRule(nil, "50000", "2018-04-01", nil)}
	bill := mkBill("49999.99", "27", "KA01AB1234", "50", "998877")
	req, missing := Evaluate(rules, bill, bill.InvoiceDate)
	if req != NotRequired || missing != nil {
		t.Fatalf("got req=%s missing=%v, want NOT_REQUIRED/nil", req, missing)
	}
}

func TestEvaluate_AtThreshold_RequiresGeneration(t *testing.T) {
	// Exactly at the threshold: consignment value >= min should be treated
	// as requiring an e-Way Bill (a boundary the brief's "exceeding" wording
	// could read either way — this codebase's Evaluate treats >= as
	// requiring, the safer legal-compliance direction: never under-flag).
	rules := []Rule{mkRule(nil, "50000", "2018-04-01", nil)}
	bill := mkBill("50000", "27", "KA01AB1234", "50", "998877")
	req, _ := Evaluate(rules, bill, bill.InvoiceDate)
	if req == NotRequired {
		t.Fatal("at-threshold consignment value must NOT be treated as NOT_REQUIRED")
	}
}

func TestEvaluate_AboveThreshold_ReadyWhenComplete(t *testing.T) {
	rules := []Rule{mkRule(nil, "50000", "2018-04-01", nil)}
	bill := mkBill("100000", "27", "KA01AB1234", "50", "998877")
	req, missing := Evaluate(rules, bill, bill.InvoiceDate)
	if req != Ready || missing != nil {
		t.Fatalf("got req=%s missing=%v, want READY/nil", req, missing)
	}
}

func TestEvaluate_AboveThreshold_MissingVehicle_NeedsInformation(t *testing.T) {
	rules := []Rule{mkRule(nil, "50000", "2018-04-01", nil)}
	bill := mkBill("100000", "27", "", "50", "998877") // no vehicle
	req, missing := Evaluate(rules, bill, bill.InvoiceDate)
	if req != NeedsInformation {
		t.Fatalf("got req=%s, want NEEDS_INFORMATION", req)
	}
	found := false
	for _, m := range missing {
		if m.Field == "vehicle_number" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing list %v does not flag vehicle_number", missing)
	}
}

func TestEvaluate_AboveThreshold_MissingHSN_NeedsInformation(t *testing.T) {
	rules := []Rule{mkRule(nil, "50000", "2018-04-01", nil)}
	bill := mkBill("100000", "27", "KA01AB1234", "50", "") // no HSN
	req, missing := Evaluate(rules, bill, bill.InvoiceDate)
	if req != NeedsInformation || len(missing) == 0 {
		t.Fatalf("got req=%s missing=%v, want NEEDS_INFORMATION with an HSN complaint", req, missing)
	}
}

func TestEvaluate_RuleVersioning_PicksRuleValidOnInvoiceDate(t *testing.T) {
	oldEnd := "2020-01-01"
	rules := []Rule{
		mkRule(nil, "5000", "2018-04-01", &oldEnd), // expired long before the invoice date
		mkRule(nil, "50000", "2020-01-02", nil),    // the one that should apply
	}
	// 10000 is above the old expired rule's 5000 threshold but below the
	// currently-applicable rule's 50000 — proves the CURRENT rule is used,
	// not an expired one that happens to be more restrictive.
	bill := mkBill("10000", "27", "KA01AB1234", "50", "998877")
	req, _ := Evaluate(rules, bill, bill.InvoiceDate)
	if req != NotRequired {
		t.Fatalf("got req=%s, want NOT_REQUIRED (the applicable rule's 50000 threshold, not the expired 5000 one)", req)
	}
}

func TestEvaluate_StateSpecificRule_OverridesNational(t *testing.T) {
	ka := "29"
	rules := []Rule{
		mkRule(nil, "50000", "2018-04-01", nil),  // national default
		mkRule(&ka, "100000", "2018-04-01", nil), // Karnataka-specific override
	}
	bill := mkBill("75000", "29", "KA01AB1234", "50", "998877") // above national, below KA's override
	req, _ := Evaluate(rules, bill, bill.InvoiceDate)
	if req != NotRequired {
		t.Fatalf("got req=%s, want NOT_REQUIRED (state-specific 100000 threshold should apply, not the national 50000)", req)
	}
}

func TestEvaluate_InvoiceOlderThan180Days_NotReady(t *testing.T) {
	rules := []Rule{mkRule(nil, "50000", "2018-04-01", nil)}
	bill := mkBill("100000", "27", "KA01AB1234", "50", "998877") // otherwise complete — would be Ready
	now := bill.InvoiceDate.Add(181 * 24 * time.Hour)
	req, missing := Evaluate(rules, bill, now)
	if req == Ready {
		t.Fatal("a 181-day-old, otherwise-complete invoice must not be reported Ready — the real government portal refuses e-Way Bill generation past 180 days")
	}
	found := false
	for _, m := range missing {
		if m.Field == "invoice_date" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing list %v does not explain the 180-day-age rejection", missing)
	}
}

func TestEvaluate_InvoiceWithin180Days_StillReady(t *testing.T) {
	rules := []Rule{mkRule(nil, "50000", "2018-04-01", nil)}
	bill := mkBill("100000", "27", "KA01AB1234", "50", "998877")
	now := bill.InvoiceDate.Add(179 * 24 * time.Hour)
	req, missing := Evaluate(rules, bill, now)
	if req != Ready || missing != nil {
		t.Fatalf("got req=%s missing=%v, want READY/nil at 179 days (must not false-positive before the real 180-day boundary)", req, missing)
	}
}

func TestEvaluate_NoApplicableRule_NeedsInformation(t *testing.T) {
	bill := mkBill("100000", "27", "KA01AB1234", "50", "998877")
	req, missing := Evaluate(nil, bill, bill.InvoiceDate)
	if req != NeedsInformation || len(missing) == 0 {
		t.Fatalf("got req=%s missing=%v, want NEEDS_INFORMATION (never silently skip a legally-required e-Way Bill for lack of configured rules)", req, missing)
	}
}
