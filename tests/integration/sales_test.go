//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/modules/gstindia"
	gstindiaapp "rechvix/internal/modules/gstindia/app"
	gstindiadomain "rechvix/internal/modules/gstindia/domain"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	identityapp "rechvix/internal/modules/identity/app"
	inventoryapp "rechvix/internal/modules/inventory/app"
	orgapp "rechvix/internal/modules/organisation/app"
	orgdomain "rechvix/internal/modules/organisation/domain"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingpg "rechvix/internal/modules/pricing/pg"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	salespg "rechvix/internal/modules/sales/pg"
	"rechvix/internal/modules/sales/printing"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
	taxationpg "rechvix/internal/modules/taxation/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/numbering"
	"rechvix/internal/platform/permissions"
)

// salesFixture wires every module sales.Service depends on against
// sharedPool, exactly as apps/server/main.go composes them, and
// provisions one organisation with a GST-registered legal entity (Stage
// 5b's additive migrations/0017 field), a base unit + HSN-classified
// product + variant, a customer party, and one configured tax rate — the
// minimum a FinalizeDocument call actually needs to succeed.
type salesFixture struct {
	Principal     permissions.Principal
	LegalEntityID uuid.UUID
	BranchID      uuid.UUID
	WarehouseID   uuid.UUID
	VariantID     uuid.UUID
	PCS           uuid.UUID
	CustomerID    uuid.UUID
	HSN           string
}

func newTestSalesServices(t *testing.T) (*salesapp.Service, *inventoryapp.Service, *catalogueapp.Service, *gstindiaapp.Service) {
	t.Helper()
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)

	catalogueSvc := newTestCatalogueService(t)
	contactsSvc := contactsapp.NewService(
		sharedPool,
		contactspg.NewPartyRepo(sharedPool),
		contactspg.NewAddressRepo(sharedPool),
		contactspg.NewTaxRegistrationRepo(sharedPool),
		checker, recorder,
	)
	orgSvc := newTestOrgService(t)
	inventorySvc := newTestInventoryService(t)

	gstRateRepo := gstindiapg.NewTaxRateRepo(sharedPool)
	gstindiaSvc := gstindiaapp.NewService(sharedPool, gstRateRepo, gstindiapg.NewStateRepo(sharedPool), checker, recorder)
	gstEngine := gstindia.NewEngine(gstRateRepo, gstindiapg.NewStateRepo(sharedPool))
	taxationSvc := taxationapp.NewService(
		sharedPool, gstEngine,
		taxationpg.NewTaxDocumentRepo(sharedPool),
		taxationpg.NewTaxLineRepo(sharedPool),
		taxationpg.NewTaxComponentRepo(sharedPool),
	)
	numberingSvc := numbering.NewService(sharedPool, numbering.NewPGRepository(sharedPool))
	pricingSvc := pricingapp.NewService(
		sharedPool,
		pricingpg.NewPriceListRepo(sharedPool),
		pricingpg.NewPriceListItemRepo(sharedPool),
		checker, recorder,
	)

	// accountingSvc is nil here deliberately — this pre-Stage-6 helper is
	// shared by every existing sales test, none of which set up a chart of
	// accounts; FinalizeDocument treats a nil accounting as "skip posting"
	// (see sales/app.Service's field comment). Stage 6's own tests
	// (accounting_test.go) construct their own sales/purchases services
	// WITH a real accountingSvc wired in.
	salesSvc := salesapp.NewService(
		sharedPool,
		salespg.NewDocumentRepo(sharedPool),
		salespg.NewDocumentLineRepo(sharedPool),
		inventorySvc, taxationSvc, catalogueSvc, contactsSvc, orgSvc, pricingSvc, numberingSvc, nil, nil,
		checker, recorder,
	)
	return salesSvc, inventorySvc, catalogueSvc, gstindiaSvc
}

func setupSalesFixture(t *testing.T, ctx context.Context) salesFixture {
	t.Helper()
	identitySvc, _ := newTestIdentityService(t)
	unique := uuid.NewString()[:8]
	boot, err := identitySvc.Bootstrap(ctx, identityapp.BootstrapParams{
		OrganisationName: "Sales Test Co " + unique, DefaultCurrencyCode: "INR", DefaultTimezone: "Asia/Kolkata",
		LegalEntityName: "Sales Test Co " + unique + " Pvt Ltd", CountryCode: "IN",
		GSTIN: "27AAAAA0000A1Z5", GSTStateCode: "27", // Maharashtra, same code Stage 5a's golden fixtures use
		BranchCode: "BR-" + unique, BranchName: "Main Branch",
		WarehouseCode: "WH-" + unique, WarehouseName: "Main Warehouse",
		OwnerEmail: "sales-" + unique + "@example.com", OwnerFullName: "Test Owner", OwnerPassword: "correct horse battery staple 42",
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	principal := permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}

	catalogueSvc := newTestCatalogueService(t)
	pcs, err := catalogueSvc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}
	hsn := "998" + uuid.NewString()[:5]
	product, err := catalogueSvc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{
		BaseUOMID: pcs.ID, Name: "Sales Test Widget " + unique, HSNSACCode: hsn,
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	variant, err := catalogueSvc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: product.ID, SKUCode: "SAL-" + unique})
	if err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}

	inventorySvc := newTestInventoryService(t)
	openingCost := mustDecimal(t, "50")
	if _, err := inventorySvc.RecordOpeningStock(ctx, principal, inventoryapp.RecordMovementParams{
		WarehouseID: boot.WarehouseID, ProductVariantID: variant.ID, MovementType: "OPENING",
		UnitID: pcs.ID, Quantity: mustDecimal(t, "100"), UnitCost: &openingCost,
	}); err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}

	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)
	gstindiaSvc := gstindiaapp.NewService(sharedPool, gstindiapg.NewTaxRateRepo(sharedPool), gstindiapg.NewStateRepo(sharedPool), checker, recorder)
	if _, err := gstindiaSvc.CreateRate(ctx, principal, gstindiaapp.CreateRateParams{
		HSNSACCode: hsn, Classification: gstindiadomain.ClassificationTaxable,
		GSTRate: mustDecimal(t, "18"), CessRate: mustDecimal(t, "0"), ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateRate: %v", err)
	}

	contactsSvc := contactsapp.NewService(
		sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool),
		checker, recorder,
	)
	customer, err := contactsSvc.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyCustomer, LegalName: "Test Customer " + unique, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateParty(customer): %v", err)
	}

	return salesFixture{
		Principal: principal, LegalEntityID: boot.LegalEntityID, BranchID: boot.BranchID, WarehouseID: boot.WarehouseID,
		VariantID: variant.ID, PCS: pcs.ID, CustomerID: customer.ID, HSN: hsn,
	}
}

func TestSales_TaxInvoice_FinalizePostsTaxSnapshotAndStock(t *testing.T) {
	ctx := context.Background()
	salesSvc, inventorySvc, _, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
		PricingMode: taxdomain.PricingExclusive,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if doc.DocumentNumber == "" {
		t.Fatal("document number was not allocated")
	}

	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "10"), UnitPrice: mustDecimal(t, "100"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}

	finalized, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}
	if finalized.Status != salesdomain.StatusFinalized {
		t.Fatalf("status = %s, want FINALIZED", finalized.Status)
	}
	if finalized.TaxDocumentID == nil {
		t.Fatal("tax_document_id was not stamped on finalize")
	}
	// 10 * 100 = 1000 taxable, exclusive, 18% GST intra-state -> 90 CGST + 90 SGST -> grand total 1180.
	if got := finalized.GrandTotalAmount.StringFixed(0); got != "1180.00" {
		t.Fatalf("GrandTotalAmount = %s, want 1180.00", got)
	}

	bal, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "90")) {
		t.Fatalf("QuantityOnHand after sale = %s, want 90 (100 opening - 10 sold)", bal.QuantityOnHand)
	}
}

func TestSales_Finalize_InsufficientStockRejectsAtomically(t *testing.T) {
	ctx := context.Background()
	salesSvc, inventorySvc, _, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	// Only 100 PCS in stock (opening stock from setupSalesFixture); ask for more.
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "1000"), UnitPrice: mustDecimal(t, "100"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}

	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err == nil {
		t.Fatal("FinalizeDocument succeeded with insufficient stock, want an error")
	}

	// Atomicity: the document must still be DRAFT (not partially
	// finalized), and stock must be untouched — proving the tax
	// calculation + stock check + status update rolled back together.
	refetched, _, err := salesSvc.GetDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if refetched.Status != salesdomain.StatusDraft {
		t.Fatalf("status after failed finalize = %s, want DRAFT (atomicity broken)", refetched.Status)
	}
	bal, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "100")) {
		t.Fatalf("QuantityOnHand after failed finalize = %s, want 100 (unchanged)", bal.QuantityOnHand)
	}
}

// TestSales_Finalize_ZeroValueDocumentRejectedWithClearError verifies the
// fix for a confusing crash: a tax invoice whose lines all price out to
// ₹0.00 (e.g. a product added with no configured selling price — the
// billing UI silently defaults an unpriced line to "0") must be rejected
// with a clear, specific error before reaching accounting's double-entry
// post, which would otherwise fail deep inside with an opaque
// "must be either a debit or a credit, not both/neither" 500.
func TestSales_Finalize_ZeroValueDocumentRejectedWithClearError(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "1"), UnitPrice: mustDecimal(t, "0"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}

	_, err = salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if !errors.Is(err, salesdomain.ErrZeroValueDocument) {
		t.Fatalf("FinalizeDocument error = %v, want ErrZeroValueDocument", err)
	}

	refetched, _, err := salesSvc.GetDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if refetched.Status != salesdomain.StatusDraft {
		t.Fatalf("status after rejected finalize = %s, want DRAFT (atomicity broken)", refetched.Status)
	}
}

// TestSales_Numbering_ConcurrentCreateUniqueSequentialNumbers is
// Scenario I's building block for the sales module specifically: N
// concurrent CreateDocument calls for the same (org, branch, doc type,
// financial year) must all receive distinct, gap-free sequential
// numbers — proving internal/platform/numbering's INSERT ... ON CONFLICT
// DO UPDATE ... RETURNING allocation is genuinely race-free under real
// concurrent load, not just single-threaded-correct.
func TestSales_Numbering_ConcurrentCreateUniqueSequentialNumbers(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	const n = 12
	numbers := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
				LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
				DocumentType: salesdomain.DocQuotation, CurrencyCode: "INR", BaseCurrencyCode: "INR",
			})
			errs[i] = err
			if err == nil {
				numbers[i] = doc.DocumentNumber
			}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreateDocument[%d]: %v", i, err)
		}
		if seen[numbers[i]] {
			t.Fatalf("duplicate document number allocated: %s", numbers[i])
		}
		seen[numbers[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct numbers, want %d (no gaps, no duplicates)", len(seen), n)
	}
}

// TestSales_TaxSnapshot_ImmutableAfterLaterRateMasterUpdate proves brief
// §7's "never recalculate an old finalized invoice using today's GST
// master" specifically through the sales module's real finalize path
// (Stage 5a already proved this for the tax engine in isolation) — a
// rate change after finalize must not move the invoice's already-printed
// numbers.
func TestSales_TaxSnapshot_ImmutableAfterLaterRateMasterUpdate(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, gstindiaSvc := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "5"), UnitPrice: mustDecimal(t, "100"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	finalized, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}
	originalTotal := finalized.GrandTotalAmount.StringFixed(0)

	// Now change the GST rate for this HSN going forward.
	if _, err := gstindiaSvc.CreateRate(ctx, fx.Principal, gstindiaapp.CreateRateParams{
		HSNSACCode: fx.HSN, Classification: gstindiadomain.ClassificationTaxable,
		GSTRate: mustDecimal(t, "28"), CessRate: mustDecimal(t, "0"), ValidFrom: time.Now().AddDate(0, 0, 1),
	}); err != nil {
		t.Fatalf("CreateRate (new rate): %v", err)
	}

	refetched, _, err := salesSvc.GetDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if got := refetched.GrandTotalAmount.StringFixed(0); got != originalTotal {
		t.Fatalf("GrandTotalAmount after later rate change = %s, want unchanged %s", got, originalTotal)
	}
}

func TestSales_RLS_BlocksCrossOrganisationDocumentRead(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, _ := newTestSalesServices(t)
	fxA := setupSalesFixture(t, ctx)
	fxB := setupSalesFixture(t, ctx)

	docA, err := salesSvc.CreateDocument(ctx, fxA.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fxA.LegalEntityID, BranchID: fxA.BranchID, WarehouseID: fxA.WarehouseID, CustomerPartyID: fxA.CustomerID,
		DocumentType: salesdomain.DocQuotation, CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument (org A): %v", err)
	}

	if _, _, err := salesSvc.GetDocument(ctx, fxB.Principal, docA.ID); err == nil {
		t.Fatal("RLS FAILED: org B's principal could read org A's sales_document")
	}
}

// TestSales_BillingLookup_ScannedBarcodeResolvesExactVariant is the
// regression test for a real bug: the billing screen's own placeholder
// text has always said "scan a barcode", but BillingLookup only ever
// searched by product name (a trigram match, which a barcode's raw
// digits essentially never satisfy) — scanning a barcode silently found
// nothing. Attaches a barcode unrelated to the fixture's own product
// name (so a false positive via name-matching the barcode string can't
// make this pass for the wrong reason), then proves looking it up
// resolves exactly that variant, and that an unrelated query still
// falls through to the ordinary name search rather than erroring.
func TestSales_BillingLookup_ScannedBarcodeResolvesExactVariant(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, catalogueSvc, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	const barcode = "8901234567890"
	if _, err := catalogueSvc.AddBarcode(ctx, fx.Principal, catalogueapp.AddBarcodeParams{VariantID: fx.VariantID, UnitID: fx.PCS, Barcode: barcode}); err != nil {
		t.Fatalf("AddBarcode: %v", err)
	}

	results, err := salesSvc.BillingLookup(ctx, fx.Principal, barcode, &fx.WarehouseID, nil, 10)
	if err != nil {
		t.Fatalf("BillingLookup(barcode): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("BillingLookup(barcode) returned %d results, want exactly 1", len(results))
	}
	if results[0].ProductVariantID != fx.VariantID {
		t.Fatalf("BillingLookup(barcode) resolved variant %s, want %s", results[0].ProductVariantID, fx.VariantID)
	}
	if results[0].QuantityOnHand == "" {
		t.Fatal("BillingLookup(barcode) result carries no stock figure — the warehouse balance lookup didn't run for the barcode path")
	}

	// A query matching neither a barcode nor the product name: falls
	// through to the (empty) name search rather than failing.
	noMatch, err := salesSvc.BillingLookup(ctx, fx.Principal, "no-such-thing-zzz", &fx.WarehouseID, nil, 10)
	if err != nil {
		t.Fatalf("BillingLookup(no match): %v", err)
	}
	if len(noMatch) != 0 {
		t.Fatalf("BillingLookup(no match) returned %d results, want 0", len(noMatch))
	}
}

// TestSales_ConvertDocument_PartialQuantityReturn_RestocksAndReversesAR
// is the regression test for the exact gap the audit found: neither
// ConvertDocument nor its httpapi handler had ever been called from
// anywhere before this pass. A customer bought 10 units, returns only 3
// (SALES_RETURN, the "physical goods actually came back" type): the
// resulting document must carry exactly quantity 3, inventory must go
// back up by exactly 3 (not all 10), and the return quantity can never
// exceed what was actually on the source invoice.
func TestSales_ConvertDocument_PartialQuantityReturn_RestocksAndReversesAR(t *testing.T) {
	ctx := context.Background()
	salesSvc, inventorySvc, _, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	invoiceDoc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
		PricingMode: taxdomain.PricingExclusive,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: invoiceDoc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "10"), UnitPrice: mustDecimal(t, "100"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	invoice, err := salesSvc.FinalizeDocument(ctx, fx.Principal, invoiceDoc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument(invoice): %v", err)
	}
	_, invoiceLines, err := salesSvc.GetDocument(ctx, fx.Principal, invoice.ID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if len(invoiceLines) != 1 {
		t.Fatalf("len(invoiceLines) = %d, want 1", len(invoiceLines))
	}
	sourceLineID := invoiceLines[0].ID

	balAfterSale, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance after sale: %v", err)
	}

	// Asking to return more than was ever sold must be rejected before
	// any document is even created.
	_, err = salesSvc.ConvertDocument(ctx, fx.Principal, invoice.ID, salesdomain.DocSalesReturn,
		map[uuid.UUID]decimal.Decimal{sourceLineID: mustDecimal(t, "11")})
	if !errors.Is(err, salesdomain.ErrReturnQuantityExceedsSource) {
		t.Fatalf("ConvertDocument(qty=11 > sold=10) error = %v, want ErrReturnQuantityExceedsSource", err)
	}

	ret, err := salesSvc.ConvertDocument(ctx, fx.Principal, invoice.ID, salesdomain.DocSalesReturn,
		map[uuid.UUID]decimal.Decimal{sourceLineID: mustDecimal(t, "3")})
	if err != nil {
		t.Fatalf("ConvertDocument(qty=3): %v", err)
	}
	if ret.ReferenceDocumentID == nil || *ret.ReferenceDocumentID != invoice.ID {
		t.Fatalf("return's ReferenceDocumentID = %v, want %s", ret.ReferenceDocumentID, invoice.ID)
	}
	_, retLines, err := salesSvc.GetDocument(ctx, fx.Principal, ret.ID)
	if err != nil {
		t.Fatalf("GetDocument(return): %v", err)
	}
	if len(retLines) != 1 || !retLines[0].Quantity.Equal(mustDecimal(t, "3")) {
		t.Fatalf("return lines = %+v, want exactly one line at quantity 3", retLines)
	}

	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, ret.ID); err != nil {
		t.Fatalf("FinalizeDocument(return): %v", err)
	}

	balAfterReturn, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance after return: %v", err)
	}
	wantQty := balAfterSale.QuantityOnHand.Add(mustDecimal(t, "3"))
	if !balAfterReturn.QuantityOnHand.Equal(wantQty) {
		t.Fatalf("QuantityOnHand after returning 3 of 10 = %s, want %s (only the 3 returned units restocked)", balAfterReturn.QuantityOnHand, wantQty)
	}

	// A CREDIT_NOTE is purely a financial adjustment — StockAffecting is
	// false for it, so finalizing one must NOT move inventory at all,
	// unlike SALES_RETURN above.
	credit, err := salesSvc.ConvertDocument(ctx, fx.Principal, invoice.ID, salesdomain.DocCreditNote,
		map[uuid.UUID]decimal.Decimal{sourceLineID: mustDecimal(t, "2")})
	if err != nil {
		t.Fatalf("ConvertDocument(CREDIT_NOTE): %v", err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, credit.ID); err != nil {
		t.Fatalf("FinalizeDocument(credit note): %v", err)
	}
	balAfterCredit, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance after credit note: %v", err)
	}
	if !balAfterCredit.QuantityOnHand.Equal(balAfterReturn.QuantityOnHand) {
		t.Fatalf("QuantityOnHand changed after finalizing a CREDIT_NOTE (%s -> %s) — a credit note must not move stock", balAfterReturn.QuantityOnHand, balAfterCredit.QuantityOnHand)
	}

	// Omitting the override entirely must still behave exactly as before
	// this parameter existed: a full copy of every line at full quantity.
	fullCopy, err := salesSvc.ConvertDocument(ctx, fx.Principal, invoice.ID, salesdomain.DocSalesReturn, nil)
	if err != nil {
		t.Fatalf("ConvertDocument(nil override): %v", err)
	}
	_, fullCopyLines, err := salesSvc.GetDocument(ctx, fx.Principal, fullCopy.ID)
	if err != nil {
		t.Fatalf("GetDocument(fullCopy): %v", err)
	}
	if len(fullCopyLines) != 1 || !fullCopyLines[0].Quantity.Equal(mustDecimal(t, "10")) {
		t.Fatalf("nil-override copy lines = %+v, want one line at the full original quantity 10", fullCopyLines)
	}
}

func TestSales_Print_A4Invoice_RendersNonEmptyPDF(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx)

	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "2"), UnitPrice: mustDecimal(t, "250"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}

	data, err := salesSvc.BuildInvoiceData(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("BuildInvoiceData: %v", err)
	}
	for _, tpl := range []printing.Template{printing.TemplateA4GSTInvoice, printing.TemplateThermal80mm, printing.TemplateThermal58mm} {
		pdfBytes, err := printing.RenderPDF(tpl, printing.ThemeClassic, *data)
		if err != nil {
			t.Fatalf("RenderPDF(%s): %v", tpl, err)
		}
		if len(pdfBytes) < 100 {
			t.Fatalf("RenderPDF(%s) produced suspiciously small output: %d bytes", tpl, len(pdfBytes))
		}
		if !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
			t.Fatalf("RenderPDF(%s) output does not start with the PDF magic bytes", tpl)
		}
	}
}

// tinyPNG is a minimal valid 1x1 PNG, built via the standard image/png
// encoder rather than a hand-typed byte literal — real, decodable bytes
// (exercising decodeAndReencodeLogo's own re-encode path at the unit
// level would need httpapi's decoder; here it's exercised end-to-end via
// UpdateLegalEntityInvoiceBranding, so what matters is that these bytes
// ARE a valid PNG, not that they look like a real logo).
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding tinyPNG: %v", err)
	}
	return buf.Bytes()
}

// TestSales_Print_UsesLegalEntityInvoiceBranding is migrations/0034's own
// regression test: BuildInvoiceData used to hardcode
// printing.SellerInfo{LegalName, GSTIN} and nothing else, silently
// dropping every other field the print templates already knew how to
// render. This proves the full path — Settings' UpdateInvoiceBranding
// write, through to what an actual finalized invoice's InvoiceData
// carries — actually wires up, not just that the SQL compiles.
func TestSales_Print_UsesLegalEntityInvoiceBranding(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, _ := newTestSalesServices(t)
	orgSvc := newTestOrgService(t)
	fx := setupSalesFixture(t, ctx)

	logo := tinyPNG(t)
	branding := orgdomain.InvoiceBrandingUpdate{
		Phone: "+91 98765 43210", Email: "billing@example.com", Website: "https://example.com",
		Address:  "Shop 4, Market Road\nPune, MH 411001",
		BankName: "Example Bank", BankAccountNumber: "000123456789", BankIFSC: "EXAM0001234",
		UPIID:                     "shop@examplebank",
		AuthorizedSignatoryName:   "Priya Sharma",
		DefaultTermsAndConditions: "Goods once sold will not be taken back.",
		LogoPNG:                   logo,
	}
	if _, err := orgSvc.UpdateLegalEntityInvoiceBranding(ctx, fx.Principal, fx.LegalEntityID, branding); err != nil {
		t.Fatalf("UpdateLegalEntityInvoiceBranding: %v", err)
	}

	// A second, unrelated update (no LogoPNG, RemoveLogo=false) must NOT
	// wipe the logo just set above — this is the specific "leave
	// unchanged" branch of pg.go's three-way CASE that a naive
	// NULLIF($n,'')-style update would get wrong.
	if _, err := orgSvc.UpdateLegalEntityInvoiceBranding(ctx, fx.Principal, fx.LegalEntityID, orgdomain.InvoiceBrandingUpdate{
		Phone: branding.Phone, Email: branding.Email, Website: branding.Website, Address: branding.Address,
		BankName: branding.BankName, BankAccountNumber: branding.BankAccountNumber, BankIFSC: branding.BankIFSC,
		UPIID: branding.UPIID, AuthorizedSignatoryName: "Priya Sharma (updated)",
		DefaultTermsAndConditions: branding.DefaultTermsAndConditions,
	}); err != nil {
		t.Fatalf("UpdateLegalEntityInvoiceBranding (no-op logo update): %v", err)
	}

	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "1"), UnitPrice: mustDecimal(t, "100"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	// Deliberately not setting the document's own TermsAndConditions —
	// CreateDocumentParams has no such field (see setupSalesFixture's
	// sibling test), so this document's own terms are always "", which is
	// exactly the case that should fall back to the legal entity's
	// DefaultTermsAndConditions.
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}

	data, err := salesSvc.BuildInvoiceData(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("BuildInvoiceData: %v", err)
	}
	if data.Seller.Phone != branding.Phone {
		t.Errorf("Seller.Phone = %q, want %q", data.Seller.Phone, branding.Phone)
	}
	if data.Seller.Email != branding.Email {
		t.Errorf("Seller.Email = %q, want %q", data.Seller.Email, branding.Email)
	}
	if data.Seller.Website != branding.Website {
		t.Errorf("Seller.Website = %q, want %q", data.Seller.Website, branding.Website)
	}
	wantAddrLines := []string{"Shop 4, Market Road", "Pune, MH 411001"}
	if len(data.Seller.AddressLines) != len(wantAddrLines) || data.Seller.AddressLines[0] != wantAddrLines[0] || data.Seller.AddressLines[1] != wantAddrLines[1] {
		t.Errorf("Seller.AddressLines = %v, want %v", data.Seller.AddressLines, wantAddrLines)
	}
	if data.Seller.BankName != branding.BankName || data.Seller.BankAccount != branding.BankAccountNumber || data.Seller.BankIFSC != branding.BankIFSC {
		t.Errorf("Seller bank fields = %+v, want name=%q account=%q ifsc=%q", data.Seller, branding.BankName, branding.BankAccountNumber, branding.BankIFSC)
	}
	if data.Seller.UPIID != branding.UPIID {
		t.Errorf("Seller.UPIID = %q, want %q", data.Seller.UPIID, branding.UPIID)
	}
	if !bytes.Equal(data.Seller.LogoPNG, logo) {
		t.Errorf("Seller.LogoPNG (%d bytes) does not match the logo set via UpdateLegalEntityInvoiceBranding (%d bytes) — the 'leave unchanged' update path may have wiped or altered it", len(data.Seller.LogoPNG), len(logo))
	}
	if data.TermsAndConditions != branding.DefaultTermsAndConditions {
		t.Errorf("TermsAndConditions = %q, want the legal entity's default %q (document set none of its own)", data.TermsAndConditions, branding.DefaultTermsAndConditions)
	}
	if data.AuthorizedSignatoryName != "Priya Sharma (updated)" {
		t.Errorf("AuthorizedSignatoryName = %q, want %q", data.AuthorizedSignatoryName, "Priya Sharma (updated)")
	}

	pdfBytes, err := printing.RenderPDF(printing.TemplateA4GSTInvoice, printing.ThemeClassic, *data)
	if err != nil {
		t.Fatalf("RenderPDF: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatalf("RenderPDF output does not start with the PDF magic bytes")
	}

	// RemoveLogo=true must actually clear it — the third leg of the
	// three-way CASE, otherwise untestable by the two updates above.
	updated, err := orgSvc.UpdateLegalEntityInvoiceBranding(ctx, fx.Principal, fx.LegalEntityID, orgdomain.InvoiceBrandingUpdate{RemoveLogo: true})
	if err != nil {
		t.Fatalf("UpdateLegalEntityInvoiceBranding (remove logo): %v", err)
	}
	if updated.LogoPNG != nil {
		t.Errorf("LogoPNG after RemoveLogo=true = %d bytes, want nil", len(updated.LogoPNG))
	}
}

// TestSales_BuildInvoiceDataForShareLink_ImpersonatesCreatorScopedToOrg
// proves the share-link document-rendering path (wired as
// notificationshttp.DocumentRenderer in apps/server/main.go, called from
// the unauthenticated GET /share/{token}/pdf route) does what its own
// doc comment claims: it renders successfully when given the real
// creator's identity and the correct organisation (the only combination
// notifications.RedeemShareLink's real callers ever produce), and it
// fails closed — not open — when given a mismatched organisation, the
// one thing standing between "share links work" and "any anonymous
// visitor can read any document by id."
func TestSales_BuildInvoiceDataForShareLink_ImpersonatesCreatorScopedToOrg(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, _ := newTestSalesServices(t)
	fxA := setupSalesFixture(t, ctx)
	fxB := setupSalesFixture(t, ctx)

	doc, err := salesSvc.CreateDocument(ctx, fxA.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fxA.LegalEntityID, BranchID: fxA.BranchID, WarehouseID: fxA.WarehouseID, CustomerPartyID: fxA.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fxA.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fxA.VariantID, UnitID: fxA.PCS,
		Quantity: mustDecimal(t, "1"), UnitPrice: mustDecimal(t, "500"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fxA.Principal, doc.ID); err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}

	// The real path: orgID and createdBy both come from the SAME
	// redeemed share_links row, exactly like httpapi.redeemPDF calls it.
	data, err := salesSvc.BuildInvoiceDataForShareLink(ctx, fxA.Principal.OrganisationID, fxA.Principal.UserID, doc.ID)
	if err != nil {
		t.Fatalf("BuildInvoiceDataForShareLink (correct org): %v", err)
	}
	pdfBytes, err := printing.RenderPDF(printing.TemplateA4GSTInvoice, printing.ThemeClassic, *data)
	if err != nil {
		t.Fatalf("RenderPDF: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatalf("RenderPDF output does not start with the PDF magic bytes")
	}

	// A mismatched organisation (org B's creator/org paired with org A's
	// document id) must fail, not silently render org A's invoice to
	// someone whose share link was for a different business entirely.
	if _, err := salesSvc.BuildInvoiceDataForShareLink(ctx, fxB.Principal.OrganisationID, fxB.Principal.UserID, doc.ID); err == nil {
		t.Fatal("BuildInvoiceDataForShareLink succeeded across organisations — should have failed closed")
	}
}

// TestSales_CompanyScopedAccess covers the multi-company access-control
// gap this session closed: a SECOND legal entity (company) added to the
// SAME organisation as setupSalesFixture's first one, and a team member
// restricted to only the first company (identityapp.CreateTeamMemberParams
// .LegalEntityIDs). Proves both enforcement points from
// sales/app.Service's own doc comments: ListDocuments excludes the other
// company's documents for a restricted user, and CreateDocument rejects
// creating one under a company the user isn't granted.
func TestSales_CompanyScopedAccess(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, _, _ := newTestSalesServices(t)
	fx := setupSalesFixture(t, ctx) // company A, via bootstrap
	orgSvc := newTestOrgService(t)

	unique := uuid.NewString()[:8]
	companyB, err := orgSvc.CreateLegalEntity(ctx, fx.Principal, orgapp.CreateLegalEntityParams{
		LegalName: "Company B " + unique, CountryCode: "IN", BaseCurrencyCode: "INR",
		GSTIN: "27BBBBB0000B1Z5", GSTStateCode: "27",
	})
	if err != nil {
		t.Fatalf("CreateLegalEntity (company B): %v", err)
	}
	branchB, err := orgSvc.CreateBranch(ctx, fx.Principal, orgapp.CreateBranchParams{
		LegalEntityID: companyB.ID, Code: "BR-B-" + unique, Name: "Company B Branch",
	})
	if err != nil {
		t.Fatalf("CreateBranch (company B): %v", err)
	}
	warehouseB, err := orgSvc.CreateWarehouse(ctx, fx.Principal, orgapp.CreateWarehouseParams{
		BranchID: branchB.ID, Code: "WH-B-" + unique, Name: "Company B Warehouse",
	})
	if err != nil {
		t.Fatalf("CreateWarehouse (company B): %v", err)
	}

	// One document under each company, both created by the (unrestricted)
	// owner — so any filtering difference below comes purely from the
	// restricted member's own grants, not from who created what.
	docA, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocQuotation, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument (company A): %v", err)
	}
	// DocSalesOrder, not DocQuotation like docA — numbering.Service.Next is
	// keyed by (org, branch, documentType, financial year), but
	// sales_documents' own UNIQUE constraint is
	// (organisation_id, document_type, document_number) with no branch in
	// it (migrations/0019_sales.up.sql) — two DIFFERENT branches' first
	// document of the SAME type in the SAME organisation both generate
	// e.g. "QTN/2026-27/000001" and collide. Real, pre-existing bug this
	// test tripped over (reported separately, out of scope for THIS
	// session's plan) — worked around here with a different document
	// type so this test exercises company-scoped access control, not
	// numbering.
	docB, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: companyB.ID, BranchID: branchB.ID, WarehouseID: warehouseB.ID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocSalesOrder, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument (company B): %v", err)
	}

	identitySvc, _ := newTestIdentityService(t)
	memberEmail := "restricted-" + unique + "@example.com"
	memberPassword := "another very long password 99"
	memberID, err := identitySvc.CreateTeamMember(ctx, fx.Principal, identityapp.CreateTeamMemberParams{
		FullName: "Restricted Member", Email: memberEmail, Password: memberPassword,
		LegalEntityIDs: []uuid.UUID{fx.LegalEntityID}, // company A only
	})
	if err != nil {
		t.Fatalf("CreateTeamMember: %v", err)
	}
	restricted := permissions.Principal{UserID: memberID, OrganisationID: fx.Principal.OrganisationID}

	// Unfiltered list (no legal_entity_id query param, the same as the
	// frontend's "show everything I can see" case) must only surface
	// company A's document.
	list, err := salesSvc.ListDocuments(ctx, restricted, nil, nil)
	if err != nil {
		t.Fatalf("ListDocuments (restricted, unfiltered): %v", err)
	}
	if len(list) != 1 || list[0].ID != docA.ID {
		t.Fatalf("ListDocuments (restricted, unfiltered) = %d doc(s), want exactly [docA]", len(list))
	}

	// Explicitly asking for company B's documents must return empty, not
	// an error and not company A's documents either — a restricted user
	// requesting a company they don't hold sees nothing there.
	listB, err := salesSvc.ListDocuments(ctx, restricted, nil, &companyB.ID)
	if err != nil {
		t.Fatalf("ListDocuments (restricted, company B filter): %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("ListDocuments (restricted, company B filter) = %d doc(s), want 0", len(listB))
	}

	// The unrestricted owner must still see both.
	listOwner, err := salesSvc.ListDocuments(ctx, fx.Principal, nil, nil)
	if err != nil {
		t.Fatalf("ListDocuments (owner): %v", err)
	}
	seenOwner := map[uuid.UUID]bool{}
	for _, d := range listOwner {
		seenOwner[d.ID] = true
	}
	if len(listOwner) != 2 || !seenOwner[docA.ID] || !seenOwner[docB.ID] {
		t.Fatalf("ListDocuments (owner) = %d doc(s) %v, want exactly [docA, docB]", len(listOwner), seenOwner)
	}

	// Creating a document under company B, as the member restricted to
	// company A, must be rejected — the highest-risk gap this feature
	// exists to close.
	if _, err := salesSvc.CreateDocument(ctx, restricted, salesapp.CreateDocumentParams{
		LegalEntityID: companyB.ID, BranchID: branchB.ID, WarehouseID: warehouseB.ID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocQuotation, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
	}); err == nil {
		t.Fatal("CreateDocument under company B succeeded for a member restricted to company A — should have failed closed")
	}
	// The same call under company A (their own, granted company) must
	// still succeed — this isn't a blanket "sales.create is broken" bug,
	// only company B specifically is forbidden.
	docA2, err := salesSvc.CreateDocument(ctx, restricted, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocQuotation, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
		PricingMode: taxdomain.PricingExclusive,
	})
	if err != nil {
		t.Fatalf("CreateDocument under company A (granted): %v", err)
	}

	// The write-action gap this session closed: AddLine/FinalizeDocument
	// on the restricted member's OWN company must now succeed (they
	// previously could create and view but never finalize/edit, even
	// their own company's documents — see sales/app.Service.editDraft/
	// finalizePerm's own doc comments for the fix).
	if _, err := salesSvc.AddLine(ctx, restricted, salesapp.AddLineParams{
		DocumentID: docA2.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "1"), UnitPrice: mustDecimal(t, "100"),
	}); err != nil {
		t.Fatalf("AddLine on own company's document (restricted member): %v", err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, restricted, docA2.ID); err != nil {
		t.Fatalf("FinalizeDocument on own company's document (restricted member): %v", err)
	}

	// The same actions against company B's document (docB, not theirs)
	// must still be rejected — the fix above closes the gap for a
	// restricted member's OWN company, it does not accidentally grant
	// them company B too.
	if _, err := salesSvc.AddLine(ctx, restricted, salesapp.AddLineParams{
		DocumentID: docB.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "1"), UnitPrice: mustDecimal(t, "100"),
	}); err == nil {
		t.Fatal("AddLine on company B's document succeeded for a member restricted to company A — should have failed closed")
	}
	if _, err := salesSvc.FinalizeDocument(ctx, restricted, docB.ID); err == nil {
		t.Fatal("FinalizeDocument on company B's document succeeded for a member restricted to company A — should have failed closed")
	}
}
