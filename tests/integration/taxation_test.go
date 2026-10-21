//go:build integration

package integration

import (
	"context"
	"rechvix/internal/modules/taxregimes"
	"testing"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/gstindia"
	gstindiaapp "rechvix/internal/modules/gstindia/app"
	gstindiadomain "rechvix/internal/modules/gstindia/domain"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxationdomain "rechvix/internal/modules/taxation/domain"
	taxationpg "rechvix/internal/modules/taxation/pg"
	"rechvix/internal/platform/money"
)

func newTestTaxationService(t *testing.T) *taxationapp.Service {
	t.Helper()
	rateRepo := gstindiapg.NewTaxRateRepo(sharedPool)
	stateRepo := gstindiapg.NewStateRepo(sharedPool)
	engine := gstindia.NewEngine(rateRepo, stateRepo)
	return taxationapp.NewService(
		sharedPool, taxregimes.NewDispatcher(engine),
		taxationpg.NewTaxDocumentRepo(sharedPool),
		taxationpg.NewTaxLineRepo(sharedPool),
		taxationpg.NewTaxComponentRepo(sharedPool),
	)
}

func TestTaxation_CalculateAndSnapshot_PersistsAndIsRetrievable(t *testing.T) {
	ctx := context.Background()
	gstSvc, _ := newTestGSTIndiaService(t)
	taxSvc := newTestTaxationService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	_, err := gstSvc.CreateRate(ctx, principal, gstindiaapp.CreateRateParams{
		HSNSACCode: "7001", Classification: gstindiadomain.ClassificationTaxable,
		GSTRate: mustDecimal(t, "18"), CessRate: mustDecimal(t, "0"),
		ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateRate: %v", err)
	}

	refID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	amount, err := money.Parse("90", "INR")
	if err != nil {
		t.Fatal(err)
	}
	doc, lines, err := func() (*taxationdomain.TaxDocument, []*taxationdomain.TaxLine, error) {
		doc, lines, _, err := taxSvc.CalculateAndSnapshot(ctx, taxationapp.SnapshotRequest{
			ReferenceType: "STANDALONE_TEST",
			ReferenceID:   &refID,
			Input: taxationdomain.TaxCalculationInput{
				OrganisationID:    principal.OrganisationID,
				SupplierStateCode: "27",
				SupplyPlace:       taxationdomain.PlaceOfSupply{StateCode: "27"},
				DocumentDate:      time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
				CurrencyCode:      "INR",
				SupplyType:        taxationdomain.SupplyB2C,
				Lines: []taxationdomain.TaxableLine{
					{LineRef: "line-1", HSNSACCode: "7001", Amount: amount, PricingMode: taxationdomain.PricingInclusive},
				},
			},
		})
		return doc, lines, err
	}()
	if err != nil {
		t.Fatalf("CalculateAndSnapshot: %v", err)
	}
	if doc.GrandTotal.StringFixed(money.RoundHalfUp) != "90.00" {
		t.Errorf("persisted GrandTotal = %s, want 90.00", doc.GrandTotal.StringFixed(money.RoundHalfUp))
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 persisted tax_line, got %d", len(lines))
	}

	// Re-fetch by reference — proves the snapshot round-trips through
	// Postgres correctly, not just that the in-memory result looked right.
	refetched, refetchedLines, _, err := taxSvc.GetByReference(ctx, principal.OrganisationID, "STANDALONE_TEST", refID)
	if err != nil {
		t.Fatalf("GetByReference: %v", err)
	}
	if refetched.ID != doc.ID {
		t.Errorf("refetched document ID = %s, want %s", refetched.ID, doc.ID)
	}
	if refetched.GrandTotal.StringFixed(money.RoundHalfUp) != "90.00" {
		t.Errorf("refetched GrandTotal = %s, want 90.00", refetched.GrandTotal.StringFixed(money.RoundHalfUp))
	}
	if len(refetchedLines) != 1 || refetchedLines[0].TaxRateMasterID == uuid.Nil {
		t.Errorf("refetched line missing its tax_rate_master snapshot reference: %+v", refetchedLines)
	}
}

// TestTaxation_FinalizedSnapshot_UnaffectedByLaterRateMasterUpdate is the
// integration-level proof of brief §7: "never recalculate an old
// finalized invoice using today's GST master." A rate calculated and
// persisted against tax_rate_master v1 must keep resolving to v1 even
// after a v2 row is added for a later effective date — both for a fresh
// recalculation pinned to the ORIGINAL document date, and, trivially, for
// the already-persisted snapshot itself (which nothing here ever mutates).
func TestTaxation_FinalizedSnapshot_UnaffectedByLaterRateMasterUpdate(t *testing.T) {
	ctx := context.Background()
	gstSvc, _ := newTestGSTIndiaService(t)
	taxSvc := newTestTaxationService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	const hsn = "7002"
	const originalDocumentDate = "2025-06-01"
	origDate, err := time.Parse("2006-01-02", originalDocumentDate)
	if err != nil {
		t.Fatal(err)
	}

	// v1: 12% GST, open-ended from 2025-01-01.
	_, err = gstSvc.CreateRate(ctx, principal, gstindiaapp.CreateRateParams{
		HSNSACCode: hsn, Classification: gstindiadomain.ClassificationTaxable,
		GSTRate: mustDecimal(t, "12"), CessRate: mustDecimal(t, "0"),
		ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateRate v1: %v", err)
	}

	amount, err := money.Parse("1000", "INR")
	if err != nil {
		t.Fatal(err)
	}
	refID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	calcInput := func(docDate time.Time) taxationapp.SnapshotRequest {
		return taxationapp.SnapshotRequest{
			ReferenceType: "STANDALONE_TEST",
			ReferenceID:   &refID,
			Input: taxationdomain.TaxCalculationInput{
				OrganisationID: principal.OrganisationID, SupplierStateCode: "27",
				SupplyPlace: taxationdomain.PlaceOfSupply{StateCode: "27"}, DocumentDate: docDate, CurrencyCode: "INR",
				Lines: []taxationdomain.TaxableLine{{LineRef: "1", HSNSACCode: hsn, Amount: amount, PricingMode: taxationdomain.PricingExclusive}},
			},
		}
	}

	originalDoc, _, _, err := taxSvc.CalculateAndSnapshot(ctx, calcInput(origDate))
	if err != nil {
		t.Fatalf("CalculateAndSnapshot (v1): %v", err)
	}
	// 1000 * 12% = 120.
	if got := originalDoc.TotalTaxAmount.StringFixed(money.RoundHalfUp); got != "120.00" {
		t.Fatalf("original snapshot tax = %s, want 120.00 (12%% under v1)", got)
	}

	// The GST master "changes": v2 at 18%, effective a month AFTER the
	// original document's date. v1's window is left open-ended
	// (deliberately, to prove Resolve's own "most recent valid_from <=
	// asOf" logic — not just a valid_to cutoff — is what protects history).
	_, err = gstSvc.CreateRate(ctx, principal, gstindiaapp.CreateRateParams{
		HSNSACCode: hsn, Classification: gstindiadomain.ClassificationTaxable,
		GSTRate: mustDecimal(t, "18"), CessRate: mustDecimal(t, "0"),
		ValidFrom: time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateRate v2: %v", err)
	}

	// A fresh calculation for a NEW document dated AFTER 2025-07-01 must
	// pick up v2 (18%) — confirms the master update took effect at all.
	newDate := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	newRefID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	freshReq := calcInput(newDate)
	freshReq.ReferenceID = &newRefID
	freshDoc, _, _, err := taxSvc.CalculateAndSnapshot(ctx, freshReq)
	if err != nil {
		t.Fatalf("CalculateAndSnapshot (post-v2, new document): %v", err)
	}
	if got := freshDoc.TotalTaxAmount.StringFixed(money.RoundHalfUp); got != "180.00" {
		t.Fatalf("fresh document (dated after v2's effective date) tax = %s, want 180.00 (18%% under v2)", got)
	}

	// The critical assertion: recalculating for the ORIGINAL document
	// date (still before v2's valid_from) must still resolve to v1 (12%),
	// not v2 — even though v2 now exists in tax_rate_master.
	recalcReq := calcInput(origDate)
	recalcRefID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	recalcReq.ReferenceID = &recalcRefID
	recalcDoc, _, _, err := taxSvc.CalculateAndSnapshot(ctx, recalcReq)
	if err != nil {
		t.Fatalf("CalculateAndSnapshot (re-run for original date, post-v2): %v", err)
	}
	if got := recalcDoc.TotalTaxAmount.StringFixed(money.RoundHalfUp); got != "120.00" {
		t.Fatalf("re-run for the ORIGINAL document date after v2 exists: tax = %s, want 120.00 (must still resolve to v1, brief §7)", got)
	}

	// And the very first persisted snapshot, fetched back, is of course
	// still exactly what it always was — nothing mutates it.
	refetched, _, _, err := taxSvc.GetByReference(ctx, principal.OrganisationID, "STANDALONE_TEST", refID)
	if err != nil {
		t.Fatalf("GetByReference (original): %v", err)
	}
	if got := refetched.TotalTaxAmount.StringFixed(money.RoundHalfUp); got != "120.00" {
		t.Fatalf("refetched original snapshot tax = %s, want unchanged 120.00", got)
	}
}
