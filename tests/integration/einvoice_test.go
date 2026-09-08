//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	einvoiceapp "rechvix/internal/modules/einvoice/app"
	einvoicedomain "rechvix/internal/modules/einvoice/domain"
	einvoicepg "rechvix/internal/modules/einvoice/pg"
	mockprovider "rechvix/internal/modules/einvoice/v1/mock"
	ewaybillapp "rechvix/internal/modules/ewaybill/app"
	"rechvix/internal/modules/ewaybill/domain"
	ewaybillpg "rechvix/internal/modules/ewaybill/pg"
	"rechvix/internal/modules/gstindia"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingpg "rechvix/internal/modules/pricing/pg"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	salespg "rechvix/internal/modules/sales/pg"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
	taxationpg "rechvix/internal/modules/taxation/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/numbering"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

var errTransientOutage = errors.New("simulated e-Invoice sandbox outage")

// newTestEinvoiceServices mirrors apps/worker/main.go's composition
// exactly, but against sharedPool and with a mock EInvoiceProvider the
// test can control (fail-on-demand) — never the real sandbox adapter.
func newTestEinvoiceServices(t *testing.T) (*salesapp.Service, *einvoiceapp.Service, *ewaybillapp.Service, *mockprovider.Provider, map[string]outbox.Handler, *outbox.PGStore) {
	t.Helper()
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)

	catalogueSvc := newTestCatalogueService(t)
	contactsSvc := contactsapp.NewService(
		sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool),
		checker, recorder,
	)
	orgSvc := newTestOrgService(t)
	inventorySvc := newTestInventoryService(t)

	gstRateRepo := gstindiapg.NewTaxRateRepo(sharedPool)
	gstEngine := gstindia.NewEngine(gstRateRepo, gstindiapg.NewStateRepo(sharedPool))
	taxationSvc := taxationapp.NewService(
		sharedPool, gstEngine, taxationpg.NewTaxDocumentRepo(sharedPool), taxationpg.NewTaxLineRepo(sharedPool), taxationpg.NewTaxComponentRepo(sharedPool),
	)
	numberingSvc := numbering.NewService(sharedPool, numbering.NewPGRepository(sharedPool))
	pricingSvc := pricingapp.NewService(sharedPool, pricingpg.NewPriceListRepo(sharedPool), pricingpg.NewPriceListItemRepo(sharedPool), checker, recorder)

	outboxStore := outbox.NewPGStore(sharedPool)

	salesSvc := salesapp.NewService(
		sharedPool, salespg.NewDocumentRepo(sharedPool), salespg.NewDocumentLineRepo(sharedPool),
		inventorySvc, taxationSvc, catalogueSvc, contactsSvc, orgSvc, pricingSvc, numberingSvc, nil, outboxStore,
		checker, recorder,
	)

	provider := mockprovider.New()
	einvoiceSvc := einvoiceapp.NewService(einvoicepg.NewRecordRepo(sharedPool), provider, "mock", salesSvc, taxationSvc, orgSvc, contactsSvc, outboxStore)
	ewaybillSvc := ewaybillapp.NewService(ewaybillpg.NewRecordRepo(sharedPool), provider, salesSvc)

	// Handlers are processed via processNextForOrg/drainForOrg
	// (tests/integration/outbox_helpers_test.go), NOT outbox.Poller —
	// see that file's doc comment for why: outbox.Poller.ProcessOnce
	// claims across ALL organisations with no test isolation, which a
	// real worker needs but a test claiming its own known, freshly-created
	// organisation's events does not. A no-op handler for
	// "invoice.finalized" (Stage 9's addition to sales.FinalizeDocument,
	// alongside this file's pre-existing "einvoice.generate" enqueue) is
	// correct here — this file's tests only care about EventTypeGenerate's
	// outcome, not webhook fan-out, but still need SOME handler registered
	// so draining this test's own organisation doesn't error out on it.
	handlers := map[string]outbox.Handler{
		einvoiceapp.EventTypeGenerate: einvoiceSvc.Handler(),
		"invoice.finalized":           func(context.Context, outbox.Event) error { return nil },
	}

	return salesSvc, einvoiceSvc, ewaybillSvc, provider, handlers, outboxStore
}

// Event draining/processing for this file's tests goes through
// processNextForOrg/drainForOrg (tests/integration/outbox_helpers_test.go)
// — organisation-scoped via RLS, not outbox.Poller's cross-organisation
// claim — see that file's doc comment for why.

func TestEinvoice_FullFlow_FinalizeEnqueuesAndPollerGenerates(t *testing.T) {
	ctx := context.Background()
	salesSvc, einvoiceSvc, _, provider, handlers, _ := newTestEinvoiceServices(t)
	fx := setupSalesFixture(t, ctx)

	doc := createAndFinalizeTaxInvoice(t, ctx, salesSvc, fx)

	drainForOrg(t, ctx, fx.Principal.OrganisationID, handlers, 2)

	rec, err := recordFor(t, ctx, einvoiceSvc, fx.Principal.OrganisationID, doc.ID)
	if err != nil {
		t.Fatalf("fetching einvoice record: %v", err)
	}
	if rec.Status != "GENERATED" {
		t.Fatalf("record status = %s, want GENERATED", rec.Status)
	}
	if rec.IRN == nil || *rec.IRN == "" {
		t.Fatal("expected a non-empty IRN")
	}
	if got := provider.GenerateIRNCallCount(); got != 1 {
		t.Fatalf("provider.GenerateIRN called %d times, want exactly 1", got)
	}
}

func TestEinvoice_FailedRetryable_ThenRetrySucceeds(t *testing.T) {
	ctx := context.Background()
	salesSvc, einvoiceSvc, _, provider, handlers, _ := newTestEinvoiceServices(t)
	fx := setupSalesFixture(t, ctx)
	doc := createAndFinalizeTaxInvoice(t, ctx, salesSvc, fx)

	provider.FailNextGenerateIRN(errTransientOutage)

	// First attempt: go through the REAL production path (poller.ProcessOnce
	// claiming the outbox event and running the handler inside its own
	// RunScoped transaction) rather than calling GenerateForDocument
	// directly — calling it directly wrapped in a naive RunScoped that
	// returns its error verbatim would roll back the very
	// "mark FAILED_RETRYABLE" write GenerateForDocument just made, which is
	// a test-harness bug, not how apps/worker actually behaves (see
	// poller.go: ProcessOnce captures the handler's error and calls
	// MarkFailed, returning THAT result from the RunScoped closure, so a
	// handler failure still commits the failure record).
	drainForOrg(t, ctx, fx.Principal.OrganisationID, handlers, 2)
	rec, err := recordFor(t, ctx, einvoiceSvc, fx.Principal.OrganisationID, doc.ID)
	if err != nil {
		t.Fatalf("fetching record after failed attempt: %v", err)
	}
	if rec.Status != "FAILED_RETRYABLE" {
		t.Fatalf("record status after failure = %s, want FAILED_RETRYABLE", rec.Status)
	}

	// Make the retry due immediately instead of sleeping out a real
	// exponential-backoff window — this org is fixture-isolated (unique
	// per test), so "every FAILED row" is unambiguous.
	forceOutboxRetryNow(t, ctx, fx.Principal.OrganisationID)

	// Second attempt (the "retry"): the mock's one-shot failure has been
	// consumed, so this succeeds. Only the einvoice.generate retry is
	// due now — the sibling invoice.finalized event was already drained
	// (marked DONE) in the first drainOutboxBatch call above, so n=1 is
	// correct here, not 2.
	drainForOrg(t, ctx, fx.Principal.OrganisationID, handlers, 1)
	rec, err = recordFor(t, ctx, einvoiceSvc, fx.Principal.OrganisationID, doc.ID)
	if err != nil {
		t.Fatalf("fetching record after retry: %v", err)
	}
	if rec.Status != "GENERATED" {
		t.Fatalf("record status after retry = %s, want GENERATED", rec.Status)
	}
	if got := provider.GenerateIRNCallCount(); got != 2 {
		t.Fatalf("provider.GenerateIRN called %d times across both attempts, want exactly 2", got)
	}
}

// TestEinvoice_Idempotency_DoubleProcessingDoesNotDoubleSubmit is Scenario
// H's building block for e-Invoice specifically (brief §33): reprocessing
// the same outbox event (worker crash/restart after a prior attempt
// already succeeded) must never call GenerateIRN a second time.
func TestEinvoice_Idempotency_DoubleProcessingDoesNotDoubleSubmit(t *testing.T) {
	ctx := context.Background()
	salesSvc, einvoiceSvc, _, provider, handlers, _ := newTestEinvoiceServices(t)
	fx := setupSalesFixture(t, ctx)
	doc := createAndFinalizeTaxInvoice(t, ctx, salesSvc, fx)

	// First processing: the real production path (poller claims and runs
	// the handler), reaching GENERATED.
	drainForOrg(t, ctx, fx.Principal.OrganisationID, handlers, 2)
	firstRec, err := recordFor(t, ctx, einvoiceSvc, fx.Principal.OrganisationID, doc.ID)
	if err != nil {
		t.Fatalf("fetching record after first call: %v", err)
	}
	if firstRec.Status != "GENERATED" {
		t.Fatalf("record status after first processing = %s, want GENERATED", firstRec.Status)
	}

	// Simulate the outbox event being reprocessed (e.g. worker crashed
	// after GenerateIRN succeeded but before the outbox row was marked
	// DONE) — GenerateForDocument is called again directly for the SAME,
	// now-already-GENERATED document. Safe to wrap in RunScoped directly
	// here (unlike the FAILED_RETRYABLE case above): the idempotency
	// check returns nil with NO writes at all, so there is nothing for a
	// nil-returning RunScoped to roll back.
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return einvoiceSvc.GenerateForDocument(ctx, fx.Principal.OrganisationID, doc.ID)
	})
	if err != nil {
		t.Fatalf("second (duplicate) GenerateForDocument: %v", err)
	}
	secondRec, err := recordFor(t, ctx, einvoiceSvc, fx.Principal.OrganisationID, doc.ID)
	if err != nil {
		t.Fatalf("fetching record after second call: %v", err)
	}

	if got := provider.GenerateIRNCallCount(); got != 1 {
		t.Fatalf("provider.GenerateIRN called %d times across two GenerateForDocument calls, want exactly 1 (idempotent no-op on the second)", got)
	}
	if *firstRec.IRN != *secondRec.IRN {
		t.Fatalf("IRN changed between the two calls: %s vs %s — a second record or a second IRN was generated", *firstRec.IRN, *secondRec.IRN)
	}
}

// TestEinvoice_OutageDoesNotCorruptSale is Scenario L (brief §79): a
// government API outage must never block or corrupt the sale itself —
// only the e-Invoice record reflects the failure.
func TestEinvoice_OutageDoesNotCorruptSale(t *testing.T) {
	ctx := context.Background()
	salesSvc, einvoiceSvc, _, provider, handlers, _ := newTestEinvoiceServices(t)
	fx := setupSalesFixture(t, ctx)

	doc := createAndFinalizeTaxInvoice(t, ctx, salesSvc, fx)
	if doc.Status != salesdomain.StatusFinalized {
		t.Fatalf("sanity check: document status = %s, want FINALIZED before the outage even happens", doc.Status)
	}

	provider.FailNextGenerateIRN(errTransientOutage)
	drainForOrg(t, ctx, fx.Principal.OrganisationID, handlers, 2)

	refetched, _, err := salesSvc.GetDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("GetDocument after the outage: %v", err)
	}
	if refetched.Status != salesdomain.StatusFinalized {
		t.Fatalf("sale's status after the einvoice outage = %s, want still FINALIZED (Scenario L: a government API failure must not corrupt the sale)", refetched.Status)
	}
	if refetched.GrandTotalAmount == nil {
		t.Fatal("sale's grand total was cleared by the einvoice outage — Scenario L violated")
	}

	rec, err := recordFor(t, ctx, einvoiceSvc, fx.Principal.OrganisationID, doc.ID)
	if err != nil {
		t.Fatalf("fetching record: %v", err)
	}
	if rec.Status != "FAILED_RETRYABLE" {
		t.Fatalf("record status = %s, want FAILED_RETRYABLE (only the einvoice record should reflect the outage)", rec.Status)
	}
}

func TestEinvoice_RLS_BlocksCrossOrganisationRecordRead(t *testing.T) {
	ctx := context.Background()
	salesSvc, einvoiceSvc, _, _, handlers, _ := newTestEinvoiceServices(t)
	fxA := setupSalesFixture(t, ctx)
	docA := createAndFinalizeTaxInvoice(t, ctx, salesSvc, fxA)
	drainForOrg(t, ctx, fxA.Principal.OrganisationID, handlers, 2)

	fxB := setupSalesFixture(t, ctx)
	_, err := recordFor(t, ctx, einvoiceSvc, fxB.Principal.OrganisationID, docA.ID)
	if err == nil {
		t.Fatal("RLS FAILED: org B could read org A's einvoice record")
	}
}

func TestEwaybill_ShipToGSTINAndVoluntaryClosure_RoundTrip(t *testing.T) {
	ctx := context.Background()
	salesSvc, einvoiceSvc, ewaybillSvc, _, handlers, _ := newTestEinvoiceServices(t)
	fx := setupSalesFixture(t, ctx)
	doc := createAndFinalizeTaxInvoice(t, ctx, salesSvc, fx)
	drainForOrg(t, ctx, fx.Principal.OrganisationID, handlers, 2)
	einvoiceRec, err := recordFor(t, ctx, einvoiceSvc, fx.Principal.OrganisationID, doc.ID)
	if err != nil {
		t.Fatalf("fetching einvoice record: %v", err)
	}

	var ewbRec *domain.Record
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		var genErr error
		ewbRec, genErr = ewaybillSvc.GenerateForDocument(ctx, fx.Principal.OrganisationID, ewaybillapp.GenerateParams{
			SalesDocumentID: doc.ID, IRN: *einvoiceRec.IRN, VehicleNumber: "MH12AB1234",
			DistanceKM: mustDecimal(t, "120"),
			// URP: the 2026-08-01 GSTN requirement (docs/research.md) —
			// an explicit "unregistered person" value, not an empty field.
			ShipToGSTIN: "URP",
		})
		return genErr
	})
	if err != nil {
		t.Fatalf("GenerateForDocument (ewaybill): %v", err)
	}
	if ewbRec.Status != domain.StatusGenerated {
		t.Fatalf("ewaybill status = %s, want GENERATED", ewbRec.Status)
	}
	if ewbRec.ShipToGSTIN == nil || *ewbRec.ShipToGSTIN != "URP" {
		t.Fatalf("ShipToGSTIN = %v, want \"URP\"", ewbRec.ShipToGSTIN)
	}
	if ewbRec.EWBNumber == nil || *ewbRec.EWBNumber == "" {
		t.Fatal("expected a non-empty EWB number")
	}

	// Voluntary closure (distinct from cancellation) — the shipment
	// happened, it's just no longer in transit.
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return ewaybillSvc.Close(ctx, fx.Principal.OrganisationID, ewbRec.ID, domain.ClosedByTransporter)
	})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	var closed *domain.Record
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		var getErr error
		closed, getErr = ewaybillRecordRepoGet(ctx, fx.Principal.OrganisationID, doc.ID)
		return getErr
	})
	if err != nil {
		t.Fatalf("re-fetching closed record: %v", err)
	}
	if closed.Status != domain.StatusClosed {
		t.Fatalf("status after Close = %s, want CLOSED (distinct from CANCELLED)", closed.Status)
	}
	if closed.ClosedByRole == nil || *closed.ClosedByRole != domain.ClosedByTransporter {
		t.Fatalf("ClosedByRole = %v, want TRANSPORTER", closed.ClosedByRole)
	}
	if closed.ClosedAt == nil {
		t.Fatal("ClosedAt was not set")
	}
}

// --- shared helpers ---

// forceOutboxRetryNow bypasses the real exponential backoff (outbox.go's
// backoff()) so a test can exercise a retry deterministically and fast
// instead of sleeping out a real 1-minute-plus window. Every FAILED row
// for orgID, not just one by id, because each test fixture is its own
// freshly-bootstrapped organisation — "every FAILED row for this org" is
// unambiguous within a single test.
func forceOutboxRetryNow(t *testing.T, ctx context.Context, orgID uuid.UUID) {
	t.Helper()
	err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE outbox_events SET next_attempt_at = now() WHERE status = 'FAILED'`)
		return err
	})
	if err != nil {
		t.Fatalf("forcing outbox retry: %v", err)
	}
}

func createAndFinalizeTaxInvoice(t *testing.T, ctx context.Context, salesSvc *salesapp.Service, fx salesFixture) *salesdomain.Document {
	t.Helper()
	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
		PricingMode: taxdomain.PricingExclusive,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "5"), UnitPrice: mustDecimal(t, "200"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	finalized, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}
	return finalized
}

// recordFor goes through einvoiceSvc.GetRecordForDocument (the same
// method einvoice/httpapi's status endpoint calls) rather than querying
// the repo directly — this is what actually exercises that method
// end-to-end for every test in this file, including the RLS test below.
func recordFor(t *testing.T, ctx context.Context, svc *einvoiceapp.Service, orgID, salesDocumentID uuid.UUID) (*einvoicedomain.Record, error) {
	t.Helper()
	var rec *einvoicedomain.Record
	err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		var getErr error
		rec, getErr = svc.GetRecordForDocument(ctx, orgID, salesDocumentID)
		return getErr
	})
	if err == nil && rec == nil {
		return nil, einvoicedomain.ErrNotFound
	}
	return rec, err
}

func ewaybillRecordRepoGet(ctx context.Context, orgID, salesDocumentID uuid.UUID) (*domain.Record, error) {
	repo := ewaybillpg.NewRecordRepo(sharedPool)
	return repo.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
}
