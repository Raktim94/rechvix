//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	mockprovider "rechvix/internal/modules/einvoice/v1/mock"
	ewaybillapp "rechvix/internal/modules/ewaybill/app"
	"rechvix/internal/modules/ewaybill/canonical"
	"rechvix/internal/modules/ewaybill/domain"
	"rechvix/internal/modules/ewaybill/eligibility"
	"rechvix/internal/modules/ewaybill/portal"
	portalv1 "rechvix/internal/modules/ewaybill/portal/v1"
	"rechvix/internal/modules/gstindia"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	logisticsapp "rechvix/internal/modules/logistics/app"
	logisticspg "rechvix/internal/modules/logistics/pg"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
	taxationpg "rechvix/internal/modules/taxation/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

// newTestFreePortalEwaybillService wires ewaybill's FREE_PORTAL mode
// (Stage 8c, docs/architecture.md §9b) on top of newTestEinvoiceServices'
// existing composition — same sharedPool, same salesSvc, same mock
// provider for the AUTOMATIC_API side. Never touches the real einvoice/v1/
// sandbox adapter.
func newTestFreePortalEwaybillService(t *testing.T) (*salesapp.Service, *contactsapp.Service, *mockprovider.Provider, *ewaybillapp.Service) {
	t.Helper()
	salesSvc, _, ewaybillSvc, provider, _, _ := newTestEinvoiceServices(t)

	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)
	orgSvc := newTestOrgService(t)
	contactsSvc := contactsapp.NewService(
		sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool),
		checker, recorder,
	)
	taxationSvc := taxationapp.NewService(
		sharedPool, gstindia.NewEngine(gstindiapg.NewTaxRateRepo(sharedPool), gstindiapg.NewStateRepo(sharedPool)),
		taxationpg.NewTaxDocumentRepo(sharedPool), taxationpg.NewTaxLineRepo(sharedPool), taxationpg.NewTaxComponentRepo(sharedPool),
	)

	freePortalSvc := ewaybillSvc.WithFreePortal(orgSvc, contactsSvc, taxationSvc, eligibility.NewPGRepository(sharedPool), portalv1.New(), recorder)
	return salesSvc, contactsSvc, provider, freePortalSvc
}

// createFinalizedInvoiceWithTransport is createAndFinalizeTaxInvoice's
// sibling with a configurable amount and shipping address — the
// free-portal eligibility/canonical tests need control over the
// consignment value (to cross/stay under the ₹50,000 seeded threshold)
// and a real shipping address (to prove snapshot immutability against a
// later live edit).
func createFinalizedInvoiceWithTransport(t *testing.T, ctx context.Context, salesSvc *salesapp.Service,
	fx salesFixture, qty, unitPrice string, shippingAddressID *uuid.UUID) *salesdomain.Document {
	t.Helper()
	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
		PricingMode: taxdomain.PricingExclusive, ShippingAddressID: shippingAddressID,
		Transporter: "Test Transporter", VehicleNumber: "MH12AB1234",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, qty), UnitPrice: mustDecimal(t, unitPrice),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	finalized, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}
	return finalized
}

// ewaybillRecordRepoGetScoped wraps ewaybillRecordRepoGet in RunScoped —
// the bare helper (defined in einvoice_test.go) needs an RLS-scoped
// transaction to see anything at all; calling it with a raw, unscoped ctx
// always returns ErrNotFound regardless of whether a row exists.
func ewaybillRecordRepoGetScoped(t *testing.T, ctx context.Context, orgID, salesDocumentID uuid.UUID) *domain.Record {
	t.Helper()
	var rec *domain.Record
	err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		var getErr error
		rec, getErr = ewaybillRecordRepoGet(ctx, orgID, salesDocumentID)
		return getErr
	})
	if err != nil {
		t.Fatalf("ewaybillRecordRepoGetScoped: %v", err)
	}
	return rec
}

func addShippingAddress(t *testing.T, ctx context.Context, contactsSvc *contactsapp.Service, principal permissions.Principal, customerID uuid.UUID, city string) uuid.UUID {
	t.Helper()
	addr, err := contactsSvc.AddAddress(ctx, principal, contactsapp.AddAddressParams{
		PartyID: customerID, AddressType: contactsdomain.AddressShipping,
		Line1: "123 Test Street", City: city, State: "Karnataka", PostalCode: "560001", CountryCode: "IN",
	})
	if err != nil {
		t.Fatalf("AddAddress: %v", err)
	}
	return addr.ID
}

// evaluateAndSupplyDistance runs EvaluateEligibility (capturing the
// canonical snapshot on first call) then patches in a distance via
// UpdateTransportInfo — sales.Document has no distance field at all
// (see UpdateTransportInfo's doc comment), so this is the only path any
// test (or real caller) has to reach READY.
func evaluateAndSupplyDistance(t *testing.T, ctx context.Context, ewSvc *ewaybillapp.Service, orgID, docID uuid.UUID) eligibility.Requirement {
	t.Helper()
	var req eligibility.Requirement
	err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		if _, evalErr := ewSvc.EvaluateEligibility(ctx, orgID, docID); evalErr != nil {
			return evalErr
		}
		dist := mustDecimal(t, "120")
		r, updErr := ewSvc.UpdateTransportInfo(ctx, orgID, docID, ewaybillapp.TransportInfoParams{DistanceKM: &dist})
		if updErr != nil {
			return updErr
		}
		req = r.Requirement
		return nil
	})
	if err != nil {
		t.Fatalf("evaluateAndSupplyDistance: %v", err)
	}
	return req
}

func TestEwaybillFreePortal_SnapshotImmutable_SurvivesLiveAddressEdit(t *testing.T) {
	ctx := context.Background()
	salesSvc, contactsSvc, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	addrID := addShippingAddress(t, ctx, contactsSvc, fx.Principal, fx.CustomerID, "Bangalore")

	// 10 * 6000 = 60,000 taxable, above the seeded ₹50,000 threshold (within the fixture's 100-unit opening stock).
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", &addrID)

	if req := evaluateAndSupplyDistance(t, ctx, ewSvc, fx.Principal.OrganisationID, doc.ID); req != eligibility.Ready {
		t.Fatalf("Requirement after supplying distance = %s, want READY", req)
	}
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, _, prepErr := ewSvc.PrepareFreePortalUpload(ctx, fx.Principal.OrganisationID, doc.ID)
		return prepErr
	})
	if err != nil {
		t.Fatalf("PrepareFreePortalUpload (first): %v", err)
	}

	rec := ewaybillRecordRepoGetScoped(t, ctx, fx.Principal.OrganisationID, doc.ID)
	var firstBill canonical.CanonicalEWayBill
	if err := json.Unmarshal(rec.CanonicalSnapshot, &firstBill); err != nil {
		t.Fatalf("unmarshaling first snapshot: %v", err)
	}
	if firstBill.ShipTo.City != "Bangalore" {
		t.Fatalf("captured snapshot city = %q, want Bangalore", firstBill.ShipTo.City)
	}

	// Simulate the customer's address changing AFTER the snapshot was
	// captured — raw SQL, bypassing the app layer entirely (there is no
	// UpdateAddress feature yet; this directly mutates the underlying row
	// the same way any future edit path eventually would).
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, execErr := sharedPool.Q(ctx).Exec(ctx, `UPDATE party_addresses SET city = 'Mumbai' WHERE id = $1`, addrID)
		return execErr
	})
	if err != nil {
		t.Fatalf("simulating live address edit: %v", err)
	}

	// Re-fetch the record's stored snapshot again (simulating "file lost,
	// regenerate") — must still show the ORIGINAL city, not the edited one.
	rec2 := ewaybillRecordRepoGetScoped(t, ctx, fx.Principal.OrganisationID, doc.ID)
	var secondBill canonical.CanonicalEWayBill
	if err := json.Unmarshal(rec2.CanonicalSnapshot, &secondBill); err != nil {
		t.Fatalf("unmarshaling second snapshot: %v", err)
	}
	if secondBill.ShipTo.City != "Bangalore" {
		t.Fatalf("SNAPSHOT IMMUTABILITY FAILED: stored snapshot's city = %q after a live address edit, want it to still read Bangalore (the value as of finalization)", secondBill.ShipTo.City)
	}
}

func TestEwaybillFreePortal_Eligibility_BelowThreshold_NotRequired(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	// 5 * 100 = 500, far below the ₹50,000 seeded threshold.
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "5", "100", nil)

	var req eligibility.Requirement
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		r, evalErr := ewSvc.EvaluateEligibility(ctx, fx.Principal.OrganisationID, doc.ID)
		if evalErr != nil {
			return evalErr
		}
		req = r.Requirement
		return nil
	})
	if err != nil {
		t.Fatalf("EvaluateEligibility: %v", err)
	}
	if req != eligibility.NotRequired {
		t.Fatalf("Requirement = %s, want NOT_REQUIRED", req)
	}
}

func TestEwaybillFreePortal_Eligibility_AboveThreshold_ReadyWhenComplete(t *testing.T) {
	ctx := context.Background()
	salesSvc, contactsSvc, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	// 10 * 6000 = 60,000, above threshold; vehicle/transporter already
	// set by createFinalizedInvoiceWithTransport. A shipping address is
	// required too (ShipTo.StateCode is one of Evaluate's completeness
	// checks) — without one, no CustomerTaxRegistration exists either in
	// this fixture, so ShipTo.StateCode would stay empty.
	addrID := addShippingAddress(t, ctx, contactsSvc, fx.Principal, fx.CustomerID, "Bangalore")
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", &addrID)

	req := evaluateAndSupplyDistance(t, ctx, ewSvc, fx.Principal.OrganisationID, doc.ID)
	if req != eligibility.Ready {
		t.Fatalf("Requirement = %s, want READY", req)
	}
}

func TestEwaybillFreePortal_PrepareUpload_ProducesFileAndAwaitsCompletion(t *testing.T) {
	ctx := context.Background()
	salesSvc, contactsSvc, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	addrID := addShippingAddress(t, ctx, contactsSvc, fx.Principal, fx.CustomerID, "Bangalore")
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", &addrID)
	if req := evaluateAndSupplyDistance(t, ctx, ewSvc, fx.Principal.OrganisationID, doc.ID); req != eligibility.Ready {
		t.Fatalf("Requirement after supplying distance = %s, want READY", req)
	}

	var fileName string
	var fileLen int
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		f, _, prepErr := ewSvc.PrepareFreePortalUpload(ctx, fx.Principal.OrganisationID, doc.ID)
		if prepErr != nil {
			return prepErr
		}
		fileName, fileLen = f.FileName, len(f.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("PrepareFreePortalUpload: %v", err)
	}
	if fileLen == 0 {
		t.Fatal("prepared file has no content")
	}
	if fileName == "" {
		t.Fatal("prepared file has no filename")
	}
	rec := ewaybillRecordRepoGetScoped(t, ctx, fx.Principal.OrganisationID, doc.ID)
	if rec.Status != domain.StatusAwaitingPortalCompletion {
		t.Fatalf("status = %s, want AWAITING_PORTAL_COMPLETION", rec.Status)
	}
	if rec.PreparedFileName == nil || *rec.PreparedFileName != fileName {
		t.Fatalf("PreparedFileName = %v, want %q", rec.PreparedFileName, fileName)
	}
	if rec.Mode != domain.ModeFreePortal {
		t.Fatalf("Mode = %s, want FREE_PORTAL", rec.Mode)
	}
}

func TestEwaybillFreePortal_ManualResult_LinksAndSetsGenerated(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", nil)

	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, evalErr := ewSvc.EvaluateEligibility(ctx, fx.Principal.OrganisationID, doc.ID)
		return evalErr
	})
	if err != nil {
		t.Fatalf("EvaluateEligibility: %v", err)
	}

	validFrom := time.Now().UTC()
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, recErr := ewSvc.RecordManualResult(ctx, fx.Principal.OrganisationID, doc.ID, ewaybillapp.ManualResultParams{
			EWBNumber: "881735003315", ValidFrom: validFrom, ValidUntil: validFrom.Add(24 * time.Hour),
		})
		return recErr
	})
	if err != nil {
		t.Fatalf("RecordManualResult: %v", err)
	}
	rec := ewaybillRecordRepoGetScoped(t, ctx, fx.Principal.OrganisationID, doc.ID)
	if rec.Status != domain.StatusGenerated {
		t.Fatalf("status = %s, want GENERATED", rec.Status)
	}
	if rec.EWBNumber == nil || *rec.EWBNumber != "881735003315" {
		t.Fatalf("EWBNumber = %v, want 881735003315", rec.EWBNumber)
	}
	if rec.Source == nil || *rec.Source != domain.SourceManualPortal {
		t.Fatalf("Source = %v, want MANUAL_PORTAL", rec.Source)
	}
}

func TestEwaybillFreePortal_ImportResult_MismatchRejected(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", nil)

	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, evalErr := ewSvc.EvaluateEligibility(ctx, fx.Principal.OrganisationID, doc.ID)
		return evalErr
	})
	if err != nil {
		t.Fatalf("EvaluateEligibility: %v", err)
	}

	now := time.Now().UTC()
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, importErr := ewSvc.ImportAndVerifyResult(ctx, fx.Principal.OrganisationID, doc.ID, ewaybillapp.ImportedResultParams{
			ManualResultParams: ewaybillapp.ManualResultParams{
				EWBNumber: "999", ValidFrom: now, ValidUntil: now.Add(24 * time.Hour),
			},
			ClaimedInvoiceNumber: "THIS-IS-NOT-" + doc.DocumentNumber, ClaimedInvoiceDate: doc.IssueDate,
			ClaimedSupplierGSTIN: "27AAAAA0000A1Z5", ClaimedDocumentType: "INV",
		})
		return importErr
	})
	if err == nil {
		t.Fatal("expected ImportAndVerifyResult to reject a mismatched invoice number, got no error")
	}
	rec := ewaybillRecordRepoGetScoped(t, ctx, fx.Principal.OrganisationID, doc.ID)
	if rec.Status == domain.StatusGenerated {
		t.Fatal("a mismatched result must NOT be linked — status is GENERATED, expected it to remain un-linked")
	}
}

func TestEwaybillFreePortal_ImportResult_MatchingLinksSuccessfully(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", nil)

	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, evalErr := ewSvc.EvaluateEligibility(ctx, fx.Principal.OrganisationID, doc.ID)
		return evalErr
	})
	if err != nil {
		t.Fatalf("EvaluateEligibility: %v", err)
	}
	rec0 := ewaybillRecordRepoGetScoped(t, ctx, fx.Principal.OrganisationID, doc.ID)
	var bill canonical.CanonicalEWayBill
	if err := json.Unmarshal(rec0.CanonicalSnapshot, &bill); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	now := time.Now().UTC()
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, importErr := ewSvc.ImportAndVerifyResult(ctx, fx.Principal.OrganisationID, doc.ID, ewaybillapp.ImportedResultParams{
			ManualResultParams: ewaybillapp.ManualResultParams{
				EWBNumber: "881735003315", ValidFrom: now, ValidUntil: now.Add(24 * time.Hour),
			},
			ClaimedInvoiceNumber: bill.InvoiceNumber, ClaimedInvoiceDate: bill.InvoiceDate,
			ClaimedSupplierGSTIN: bill.Supplier.GSTIN, ClaimedDocumentType: bill.DocumentType,
		})
		return importErr
	})
	if err != nil {
		t.Fatalf("ImportAndVerifyResult (matching): %v", err)
	}
	rec := ewaybillRecordRepoGetScoped(t, ctx, fx.Principal.OrganisationID, doc.ID)
	if rec.Status != domain.StatusGenerated {
		t.Fatalf("status = %s, want GENERATED", rec.Status)
	}
	if rec.Source == nil || *rec.Source != domain.SourceImportedFile {
		t.Fatalf("Source = %v, want IMPORTED_FILE", rec.Source)
	}
}

// TestEwaybillFreePortal_APIFailureFallback_SucceedsWithoutReentry is
// docs/architecture.md §9b's fallback requirement: an AUTOMATIC_API
// failure must not force re-entering the invoice — the same underlying
// document can immediately go through FREE_PORTAL instead.
func TestEwaybillFreePortal_APIFailureFallback_SucceedsWithoutReentry(t *testing.T) {
	ctx := context.Background()
	salesSvc, contactsSvc, provider, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	addrID := addShippingAddress(t, ctx, contactsSvc, fx.Principal, fx.CustomerID, "Bangalore")
	doc := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", &addrID)

	// AUTOMATIC_API attempt fails (simulated outage) — GenerateForDocument
	// needs an IRN; pass a synthetic one since no real e-Invoice flow ran
	// for this test, only the EWayBillProvider call itself is under test.
	provider.FailNextGenerateEWayBill(errTransientOutage)
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, genErr := ewSvc.GenerateForDocument(ctx, fx.Principal.OrganisationID, ewaybillapp.GenerateParams{
			SalesDocumentID: doc.ID, IRN: "synthetic-irn-for-fallback-test", VehicleNumber: "MH12AB1234",
			DistanceKM: mustDecimal(t, "50"),
		})
		return genErr
	})
	if err == nil {
		t.Fatal("expected the AUTOMATIC_API attempt to fail (simulated outage)")
	}

	// FREE_PORTAL fallback: same document, no re-entry, must succeed —
	// this creates its own new FREE_PORTAL-mode record (the failed
	// AUTOMATIC_API row is left alone, per getOrCreateFreePortalRecord's
	// doc comment).
	evaluateAndSupplyDistance(t, ctx, ewSvc, fx.Principal.OrganisationID, doc.ID)
	var fileLen int
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		f, _, prepErr := ewSvc.PrepareFreePortalUpload(ctx, fx.Principal.OrganisationID, doc.ID)
		fileLen = len(f.Content)
		return prepErr
	})
	if err != nil {
		t.Fatalf("PrepareFreePortalUpload after API fallback: %v", err)
	}
	if fileLen == 0 {
		t.Fatal("fallback prepared file has no content")
	}
}

func TestEwaybillFreePortal_RLS_VehiclesBlockCrossOrgRead(t *testing.T) {
	ctx := context.Background()
	fxA := setupSalesFixture(t, ctx)
	fxB := setupSalesFixture(t, ctx)

	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)
	logisticsSvc := logisticsapp.NewService(sharedPool, logisticspg.NewVehicleRepo(sharedPool), logisticspg.NewTransporterRepo(sharedPool),
		logisticspg.NewPreferenceRepo(sharedPool), checker, recorder)

	v, err := logisticsSvc.CreateVehicle(ctx, fxA.Principal, logisticsapp.CreateVehicleParams{RegistrationNumber: "KA01AB9999"})
	if err != nil {
		t.Fatalf("CreateVehicle: %v", err)
	}

	listB, err := logisticsSvc.ListVehicles(ctx, fxB.Principal, false)
	if err != nil {
		t.Fatalf("ListVehicles as org B: %v", err)
	}
	for _, veh := range listB {
		if veh.ID == v.ID {
			t.Fatal("RLS FAILED: org B's vehicle list included org A's vehicle")
		}
	}

	listA, err := logisticsSvc.ListVehicles(ctx, fxA.Principal, false)
	if err != nil {
		t.Fatalf("ListVehicles as org A: %v", err)
	}
	found := false
	for _, veh := range listA {
		if veh.ID == v.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("org A's own vehicle list did not include the vehicle it just created")
	}
}

// TestEwaybillFreePortal_PrepareBatch_SkipsIneligibleAndGroupsTheRest is
// the bulk-selection queue's own test (docs/architecture.md §9b — Stage
// 8c's own pass left this explicitly unbuilt: "the SplitBatch primitive
// exists and is tested, but no endpoint to select multiple invoices").
// Proves PrepareFreePortalUploadBatch does the two things a bulk action
// over a mixed selection actually needs: an ineligible document doesn't
// abort the whole batch (it's reported, not silently dropped), and the
// eligible documents' prepared content really does end up inside the
// returned batch file(s).
func TestEwaybillFreePortal_PrepareBatch_SkipsIneligibleAndGroupsTheRest(t *testing.T) {
	ctx := context.Background()
	salesSvc, contactsSvc, _, ewSvc := newTestFreePortalEwaybillService(t)
	fx := setupSalesFixture(t, ctx)
	addrID := addShippingAddress(t, ctx, contactsSvc, fx.Principal, fx.CustomerID, "Bangalore")

	// Two docs above the seeded ₹50,000 threshold, brought to READY the
	// same way evaluateAndSupplyDistance already does for the single-
	// document tests above.
	docA := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "6000", &addrID)
	if req := evaluateAndSupplyDistance(t, ctx, ewSvc, fx.Principal.OrganisationID, docA.ID); req != eligibility.Ready {
		t.Fatalf("docA requirement = %s, want READY", req)
	}
	docB := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "10", "7000", &addrID)
	if req := evaluateAndSupplyDistance(t, ctx, ewSvc, fx.Principal.OrganisationID, docB.ID); req != eligibility.Ready {
		t.Fatalf("docB requirement = %s, want READY", req)
	}

	// One doc well under the threshold — never becomes eligible at all,
	// so PrepareFreePortalUpload hits domain.ErrNotEligible for it.
	docC := createFinalizedInvoiceWithTransport(t, ctx, salesSvc, fx, "1", "100", &addrID)

	var batches []portal.PreparedFile
	var skipped []ewaybillapp.BatchSkip
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		var err error
		batches, skipped, err = ewSvc.PrepareFreePortalUploadBatch(ctx, fx.Principal.OrganisationID, []uuid.UUID{docA.ID, docB.ID, docC.ID})
		return err
	})
	if err != nil {
		t.Fatalf("PrepareFreePortalUploadBatch: %v", err)
	}

	if len(skipped) != 1 || skipped[0].SalesDocumentID != docC.ID {
		t.Fatalf("skipped = %+v, want exactly docC (%s) skipped", skipped, docC.ID)
	}
	if len(batches) == 0 {
		t.Fatal("batches is empty — the two eligible documents produced no output")
	}

	var totalItems int
	for _, b := range batches {
		var items []json.RawMessage
		if err := json.Unmarshal(b.Content, &items); err != nil {
			t.Fatalf("unmarshaling batch file %q: %v", b.FileName, err)
		}
		totalItems += len(items)
	}
	if totalItems != 2 {
		t.Fatalf("total items across all batch files = %d, want 2 (docA and docB)", totalItems)
	}
}
