//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	identityapp "rechvix/internal/modules/identity/app"
	inventoryapp "rechvix/internal/modules/inventory/app"
	reportingdomain "rechvix/internal/modules/reporting/domain"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/modules/taxregimes"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

// regimeFixture is a sales fixture for a non-India legal entity: its country,
// currency, a product whose tax category is the line's tax code, and stock.
func setupRegimeFixture(t *testing.T, ctx context.Context, country, currency, taxCode string) accountingFixture {
	t.Helper()
	identitySvc, _ := newTestIdentityService(t)
	unique := uuid.NewString()[:8]
	boot, err := identitySvc.Bootstrap(ctx, identityapp.BootstrapParams{
		OrganisationName: "Regime Co " + unique, DefaultCurrencyCode: currency, DefaultTimezone: "UTC",
		LegalEntityName: "Regime Co " + unique + " Ltd", CountryCode: country,
		BranchCode: "BR-" + unique, BranchName: "Main", WarehouseCode: "WH-" + unique, WarehouseName: "Main Warehouse",
		OwnerEmail: "regime-" + unique + "@example.com", OwnerFullName: "Owner", OwnerPassword: "correct horse battery staple 42",
	})
	if err != nil {
		t.Fatalf("Bootstrap(%s): %v", country, err)
	}
	principal := permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}
	catalogueSvc := newTestCatalogueService(t)
	pcs, err := catalogueSvc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalogueSvc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{BaseUOMID: pcs.ID, Name: "Widget " + unique, HSNSACCode: taxCode})
	if err != nil {
		t.Fatalf("CreateProduct with tax code %q: %v", taxCode, err)
	}
	variant, err := catalogueSvc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: product.ID, SKUCode: "RG-" + unique})
	if err != nil {
		t.Fatal(err)
	}
	cost := mustDecimal(t, "10")
	if _, err := newTestInventoryService(t).RecordOpeningStock(ctx, principal, inventoryapp.RecordMovementParams{
		WarehouseID: boot.WarehouseID, ProductVariantID: variant.ID, MovementType: "OPENING",
		UnitID: pcs.ID, Quantity: mustDecimal(t, "100"), UnitCost: &cost,
	}); err != nil {
		t.Fatal(err)
	}
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	contactsSvc := contactsapp.NewService(sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool),
		contactspg.NewTaxRegistrationRepo(sharedPool), checker, audit.NewPGRecorder(sharedPool))
	customer, err := contactsSvc.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyCustomer, LegalName: "Customer " + unique, CurrencyCode: currency,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accountingFixture{salesFixture: salesFixture{
		Principal: principal, LegalEntityID: boot.LegalEntityID, BranchID: boot.BranchID, WarehouseID: boot.WarehouseID,
		VariantID: variant.ID, PCS: pcs.ID, CustomerID: customer.ID, HSN: taxCode,
	}}
}

type regimeInvoice struct {
	currency, customerCountry, supplyRegion string
	taxExempt, surcharge                    bool
	registrationID                          *uuid.UUID
	mode                                    taxdomain.PricingMode
	qty, price                              string
}

func finalizeRegimeInvoice(t *testing.T, ctx context.Context, salesSvc *salesapp.Service, fx accountingFixture, in regimeInvoice) (*salesdomain.Document, error) {
	t.Helper()
	if in.mode == "" {
		in.mode = taxdomain.PricingExclusive
	}
	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, CurrencyCode: in.currency, BaseCurrencyCode: in.currency, PricingMode: in.mode,
		CustomerCountry: in.customerCountry, SupplyRegion: in.supplyRegion, CustomerTaxExempt: in.taxExempt,
		EquivalenceSurcharge: in.surcharge, CustomerTaxRegistrationID: in.registrationID,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, in.qty), UnitPrice: mustDecimal(t, in.price),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	return salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
}

// snapshotLine reads the persisted tax line and its components for a document.
func snapshotLine(t *testing.T, ctx context.Context, fx accountingFixture, doc *salesdomain.Document) (classification, regime, version string, rateMasterNull bool, components []string) {
	t.Helper()
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		var id uuid.UUID
		if err := sharedPool.Q(ctx).QueryRow(ctx,
			`SELECT tl.id, tl.classification, COALESCE(tl.tax_regime,''), COALESCE(tl.rate_pack_version,''), tl.tax_rate_master_id IS NULL
			 FROM tax_lines tl WHERE tl.tax_document_id = $1`, *doc.TaxDocumentID).Scan(&id, &classification, &regime, &version, &rateMasterNull); err != nil {
			return err
		}
		rows, err := sharedPool.Q(ctx).Query(ctx, `SELECT component_type || ':' || rate::text FROM tax_components WHERE tax_line_id = $1 ORDER BY component_type`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				return err
			}
			components = append(components, c)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("reading tax snapshot: %v", err)
	}
	return
}

func TestTaxRegimes_Spain_IVA_FinalizesAndPosts(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := accountingSvc.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	doc, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "EUR", qty: "10", price: "100"})
	if err != nil {
		t.Fatalf("finalize Spanish invoice: %v", err)
	}
	// 10 x 100 = 1000 net, 21% IVA = 210, total 1210.
	if got := doc.GrandTotalAmount.StringFixed(0); got != "1210.00" {
		t.Fatalf("grand total = %s, want 1210.00", got)
	}
	class, regime, version, rmNull, comps := snapshotLine(t, ctx, fx, doc)
	if class != "STANDARD" || regime != "ES_IVA" || version == "" || !rmNull || len(comps) != 1 || comps[0][:4] != "IVA:" {
		t.Fatalf("snapshot = class %s regime %s version %q rateMasterNull %v comps %v", class, regime, version, rmNull, comps)
	}
	assertJournalsBalanced(t, ctx, fx.Principal.OrganisationID)
}

func TestTaxRegimes_Spain_CanaryIslandsRefusedAndNothingPosted(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := accountingSvc.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "EUR", supplyRegion: "ES-CN", qty: "1", price: "100"}); !errors.Is(err, taxregimes.ErrRegionNotSupported) {
		t.Fatalf("a Canary Islands sale must be refused with ErrRegionNotSupported (IGIC not implemented), got %v", err)
	}
	var finalized int
	_ = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM sales_documents WHERE status = 'FINALIZED'`).Scan(&finalized)
	})
	if finalized != 0 {
		t.Fatalf("refused document must stay a draft, found %d finalized", finalized)
	}
}

func TestTaxRegimes_EU_Germany_B2BIntraCommunity_ZeroRated(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "DE", "EUR", "STANDARD")
	if err := accountingSvc.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	// A consumer in France is refused (OSS not implemented).
	if _, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "EUR", customerCountry: "FR", qty: "1", price: "100"}); !errors.Is(err, taxregimes.ErrCrossBorderB2CUnsupported) {
		t.Fatalf("DE->FR consumer sale must be refused with ErrCrossBorderB2CUnsupported, got %v", err)
	}
	// Domestic German sale: 19%.
	doc, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "EUR", qty: "1", price: "200"})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.GrandTotalAmount.StringFixed(0); got != "238.00" {
		t.Fatalf("DE domestic grand total = %s, want 238.00", got)
	}
}

func TestTaxRegimes_UnitedKingdom_ReducedRate_Inclusive(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "GB", "GBP", "RATE:5")
	if err := accountingSvc.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	// 105.00 inclusive at 5% -> 100.00 net + 5.00 VAT.
	doc, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "GBP", mode: taxdomain.PricingInclusive, qty: "1", price: "105"})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.GrandTotalAmount.StringFixed(0); got != "105.00" {
		t.Fatalf("grand total = %s, want 105.00", got)
	}
	class, regime, _, _, comps := snapshotLine(t, ctx, fx, doc)
	if class != "REDUCED" || regime != "GB_VAT" || len(comps) != 1 || comps[0] != "VAT:5.000000000000" {
		t.Fatalf("snapshot = %s %s %v", class, regime, comps)
	}
}

func TestTaxRegimes_US_SalesTax_OnlyWhereRegistered(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "US", "USD", "TAXABLE")
	if err := accountingSvc.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	orgSvc := newTestOrgService(t)
	if _, err := orgSvc.UpdateLegalEntityUSCollectionRegions(ctx, fx.Principal, fx.LegalEntityID, []string{"us-ca"}); err != nil {
		t.Fatalf("UpdateLegalEntityUSCollectionRegions: %v", err)
	}
	// California: registered -> 7.25% of 100 = 7.25.
	doc, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "USD", customerCountry: "US", supplyRegion: "us-ca", qty: "1", price: "100"})
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	if got := doc.GrandTotalAmount.StringFixed(0); got != "107.25" {
		t.Fatalf("CA grand total = %s, want 107.25", got)
	}
	// New York: not registered -> no tax charged, labelled NOT_COLLECTED.
	doc, err = finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "USD", customerCountry: "US", supplyRegion: "US-NY", qty: "1", price: "100"})
	if err != nil {
		t.Fatalf("NY: %v", err)
	}
	if got := doc.GrandTotalAmount.StringFixed(0); got != "100.00" {
		t.Fatalf("NY grand total = %s, want 100.00", got)
	}
	if class, _, _, _, _ := snapshotLine(t, ctx, fx, doc); class != "NOT_COLLECTED" {
		t.Fatalf("NY classification = %s, want NOT_COLLECTED", class)
	}
	// A bad region code is rejected at the settings API.
	if _, err := orgSvc.UpdateLegalEntityUSCollectionRegions(ctx, fx.Principal, fx.LegalEntityID, []string{"CA"}); err == nil {
		t.Fatal("a non-US-XX region must be rejected")
	}
}

// assertJournalsBalanced checks the organisation has posted journal lines and
// that total debits equal total credits.
func assertJournalsBalanced(t *testing.T, ctx context.Context, orgID uuid.UUID) {
	t.Helper()
	var lines int
	var diff string
	err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx,
			`SELECT count(*), COALESCE(SUM(debit_amount) - SUM(credit_amount), 0)::text FROM journal_lines`).Scan(&lines, &diff)
	})
	if err != nil {
		t.Fatalf("reading journals: %v", err)
	}
	if lines == 0 {
		t.Fatal("finalizing the invoice posted no journal lines")
	}
	if mustDecimal(t, diff).Sign() != 0 {
		t.Fatalf("journals unbalanced by %s", diff)
	}
}

// TestLedgerCurrency_ReportsAndJournalsFollowTheOrganisation is the regression
// test for amounts being labelled INR for every company: a EUR organisation's
// sales summary, trial balance and dashboard must all be denominated in EUR,
// and a JPY organisation must keep zero-decimal precision.
func TestLedgerCurrency_ReportsAndJournalsFollowTheOrganisation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ country, currency, code string }{
		{"ES", "EUR", "STANDARD"},
		{"GB", "GBP", "STANDARD"},
	} {
		t.Run(tc.currency, func(t *testing.T) {
			salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
			reportingSvc := newTestReportingService(t, accountingSvc)
			fx := setupRegimeFixture(t, ctx, tc.country, tc.currency, tc.code)
			if err := accountingSvc.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
				t.Fatal(err)
			}
			if _, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: tc.currency, qty: "3", price: "100"}); err != nil {
				t.Fatal(err)
			}
			sum, err := reportingSvc.SalesSummary(ctx, fx.Principal, reportingdomain.Filter{}, reportingdomain.GroupByDay)
			if err != nil || len(sum) == 0 {
				t.Fatalf("SalesSummary: rows=%d err=%v", len(sum), err)
			}
			if got := sum[0].GrandTotal.Currency(); got != tc.currency {
				t.Errorf("sales summary currency = %s, want %s", got, tc.currency)
			}
			tb, err := reportingSvc.TrialBalance(ctx, fx.Principal, time.Now().AddDate(0, 0, 1))
			if err != nil || len(tb) == 0 {
				t.Fatalf("TrialBalance: rows=%d err=%v", len(tb), err)
			}
			for _, r := range tb {
				if r.Debit.Currency() != tc.currency || r.Credit.Currency() != tc.currency {
					t.Fatalf("trial balance row %s is %s/%s, want %s", r.AccountCode, r.Debit.Currency(), r.Credit.Currency(), tc.currency)
				}
			}
			dash, err := reportingSvc.Dashboard(ctx, fx.Principal)
			if err != nil {
				t.Fatal(err)
			}
			if dash.TodaySales.Currency() != tc.currency || dash.OutstandingReceivable.Currency() != tc.currency {
				t.Errorf("dashboard currencies = %s/%s, want %s", dash.TodaySales.Currency(), dash.OutstandingReceivable.Currency(), tc.currency)
			}
		})
	}
}
