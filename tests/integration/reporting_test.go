//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	inventoryapp "rechvix/internal/modules/inventory/app"
	orgapp "rechvix/internal/modules/organisation/app"
	purchasesapp "rechvix/internal/modules/purchases/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
	reportingapp "rechvix/internal/modules/reporting/app"
	reportingdomain "rechvix/internal/modules/reporting/domain"
	reportingpg "rechvix/internal/modules/reporting/pg"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func newTestReportingService(t *testing.T, accountingSvc *accountingapp.Service) *reportingapp.Service {
	t.Helper()
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	contactsSvc := contactsapp.NewService(
		sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool),
		checker, audit.NewPGRecorder(sharedPool),
	)
	return reportingapp.NewService(sharedPool, reportingpg.NewRepo(sharedPool), accountingSvc, contactsSvc, checker)
}

func finalizePurchase(t *testing.T, ctx context.Context, purchasesSvc *purchasesapp.Service, fx accountingFixture, qty, price string) *purchasesdomain.Document {
	t.Helper()
	doc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierID,
		DocumentType: purchasesdomain.DocGoodsReceipt, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("purchases CreateDocument: %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, qty), UnitPrice: mustDecimal(t, price),
	}); err != nil {
		t.Fatalf("purchases AddLine: %v", err)
	}
	finalized, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("purchases FinalizeDocument: %v", err)
	}
	return finalized
}

// TestReporting_SalesSummary_GroupedByCustomer_MatchesHandComputed seeds
// two finalized tax invoices for the same customer (10 PCS @ 100 and 5 PCS
// @ 100, both exclusive, 18% intra-state — same fixture math as
// sales_test.go's own finalize test) and checks the summary report's
// totals against hand-computed expectations, not just "the query ran."
func TestReporting_SalesSummary_GroupedByCustomer_MatchesHandComputed(t *testing.T) {
	ctx := context.Background()
	salesSvc, purchasesSvc, accountingSvc, _ := newTestAccountingServices(t)
	_ = purchasesSvc
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100") // taxable 1000, grand 1180
	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "5", "100")  // taxable 500, grand 590

	rows, err := reportingSvc.SalesSummary(ctx, fx.Principal, reportingdomain.Filter{}, reportingdomain.GroupByCustomer)
	if err != nil {
		t.Fatalf("SalesSummary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d groups, want 1 (one customer)", len(rows))
	}
	r := rows[0]
	if r.DocumentCount != 2 {
		t.Fatalf("DocumentCount = %d, want 2", r.DocumentCount)
	}
	if got := r.GrandTotal.StringFixed(0); got != "1770.00" {
		t.Fatalf("GrandTotal = %s, want 1770.00 (1180+590)", got)
	}
	if got := r.TaxableAmount.StringFixed(0); got != "1500.00" {
		t.Fatalf("TaxableAmount = %s, want 1500.00 (1000+500)", got)
	}
}

// TestReporting_SalesSummary_FilteredByCompany covers the multi-company
// report filtering this session added (domain.Filter.LegalEntityID,
// resolved through app.Service.resolvedFilter and applied via
// pg.whereBuilder.addOptionalUUIDs("sd.legal_entity_id", ...)) — the
// mechanism GSTR1/GSTR3B/PurchaseSummary/StockValuation/etc. all share,
// so proving it here via SalesSummary (which already has an established
// hand-computed-totals test pattern) covers the shared code path without
// duplicating the same assertion once per report function.
func TestReporting_SalesSummary_FilteredByCompany(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fxA := setupAccountingFixture(t, ctx, accountingSvc)
	orgSvc := newTestOrgService(t)

	unique := uuid.NewString()[:8]
	companyB, err := orgSvc.CreateLegalEntity(ctx, fxA.Principal, orgapp.CreateLegalEntityParams{
		LegalName: "Report Company B " + unique, CountryCode: "IN", BaseCurrencyCode: "INR",
		GSTIN: "27CCCCC0000C1Z5", GSTStateCode: "27",
	})
	if err != nil {
		t.Fatalf("CreateLegalEntity (company B): %v", err)
	}
	branchB, err := orgSvc.CreateBranch(ctx, fxA.Principal, orgapp.CreateBranchParams{
		LegalEntityID: companyB.ID, Code: "RB-" + unique, Name: "Company B Branch",
	})
	if err != nil {
		t.Fatalf("CreateBranch (company B): %v", err)
	}
	warehouseB, err := orgSvc.CreateWarehouse(ctx, fxA.Principal, orgapp.CreateWarehouseParams{
		BranchID: branchB.ID, Code: "RW-" + unique, Name: "Company B Warehouse",
	})
	if err != nil {
		t.Fatalf("CreateWarehouse (company B): %v", err)
	}
	// Products/customers are org-wide (no legal_entity_id of their own —
	// see catalogue/contacts' app.Service.view doc comments), so company
	// B's invoice reuses fxA's product/customer, differing only in which
	// company/branch/warehouse the DOCUMENT itself belongs to. Stock is
	// NOT shared across warehouses though — company B's own warehouse
	// starts with zero, so it needs its own opening stock the same way
	// setupSalesFixture already gave fxA's warehouse.
	fxB := accountingFixture{
		salesFixture: salesFixture{
			Principal: fxA.Principal, LegalEntityID: companyB.ID, BranchID: branchB.ID, WarehouseID: warehouseB.ID,
			VariantID: fxA.VariantID, PCS: fxA.PCS, CustomerID: fxA.CustomerID,
		},
		SupplierID: fxA.SupplierID,
	}
	inventorySvc := newTestInventoryService(t)
	openingCost := mustDecimal(t, "50")
	if _, err := inventorySvc.RecordOpeningStock(ctx, fxA.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: warehouseB.ID, ProductVariantID: fxA.VariantID, MovementType: "OPENING",
		UnitID: fxA.PCS, Quantity: mustDecimal(t, "100"), UnitCost: &openingCost,
	}); err != nil {
		t.Fatalf("RecordOpeningStock (company B warehouse): %v", err)
	}

	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fxA, "10", "100") // taxable 1000
	// DocPOSInvoice, not DocTaxInvoice like fxA — same pre-existing
	// numbering/branch collision noted in sales_test.go's
	// TestSales_CompanyScopedAccess (numbering.Service.Next is keyed by
	// branch, but sales_documents' UNIQUE constraint is
	// (organisation_id, document_type, document_number) with no branch —
	// two different branches' first invoice of the SAME type collide).
	finalizeTaxInvoiceAs(t, ctx, salesSvc, fxB, salesdomain.DocPOSInvoice, "3", "100") // taxable 300, different company

	rowsA, err := reportingSvc.SalesSummary(ctx, fxA.Principal, reportingdomain.Filter{LegalEntityID: &fxA.LegalEntityID}, reportingdomain.GroupByCustomer)
	if err != nil {
		t.Fatalf("SalesSummary (company A filter): %v", err)
	}
	if len(rowsA) != 1 || rowsA[0].DocumentCount != 1 || rowsA[0].TaxableAmount.StringFixed(0) != "1000.00" {
		t.Fatalf("SalesSummary (company A filter) = %+v, want 1 doc, taxable 1000.00", rowsA)
	}

	rowsB, err := reportingSvc.SalesSummary(ctx, fxA.Principal, reportingdomain.Filter{LegalEntityID: &companyB.ID}, reportingdomain.GroupByCustomer)
	if err != nil {
		t.Fatalf("SalesSummary (company B filter): %v", err)
	}
	if len(rowsB) != 1 || rowsB[0].DocumentCount != 1 || rowsB[0].TaxableAmount.StringFixed(0) != "300.00" {
		t.Fatalf("SalesSummary (company B filter) = %+v, want 1 doc, taxable 300.00", rowsB)
	}

	// No company filter at all -> both companies' invoices combined.
	rowsAll, err := reportingSvc.SalesSummary(ctx, fxA.Principal, reportingdomain.Filter{}, reportingdomain.GroupByCustomer)
	if err != nil {
		t.Fatalf("SalesSummary (unfiltered): %v", err)
	}
	if len(rowsAll) != 1 || rowsAll[0].DocumentCount != 2 || rowsAll[0].TaxableAmount.StringFixed(0) != "1300.00" {
		t.Fatalf("SalesSummary (unfiltered) = %+v, want 2 docs combined, taxable 1300.00", rowsAll)
	}
}

func TestReporting_SalesSummary_GroupedByDay_SeparatesDates(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "1", "100")

	rows, err := reportingSvc.SalesSummary(ctx, fx.Principal, reportingdomain.Filter{}, reportingdomain.GroupByDay)
	if err != nil {
		t.Fatalf("SalesSummary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d day-buckets, want 1", len(rows))
	}
	today := time.Now().Format("2006-01-02")
	if rows[0].Key != today {
		t.Fatalf("day key = %q, want today (%q)", rows[0].Key, today)
	}
}

func TestReporting_SalesSummary_InvalidGroupDimension_Rejected(t *testing.T) {
	ctx := context.Background()
	_, _, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	_, err := reportingSvc.SalesSummary(ctx, fx.Principal, reportingdomain.Filter{}, reportingdomain.GroupDimension("'; DROP TABLE sales_documents;--"))
	if err == nil {
		t.Fatal("expected an invalid group dimension to be rejected before it ever reaches SQL, got no error")
	}
}

func TestReporting_PurchaseSummary_MatchesHandComputed(t *testing.T) {
	ctx := context.Background()
	salesSvc, purchasesSvc, accountingSvc, _ := newTestAccountingServices(t)
	_ = salesSvc
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	finalizePurchase(t, ctx, purchasesSvc, fx, "50", "20") // 1000
	finalizePurchase(t, ctx, purchasesSvc, fx, "25", "20") // 500

	rows, err := reportingSvc.PurchaseSummary(ctx, fx.Principal, reportingdomain.Filter{}, reportingdomain.GroupBySupplier)
	if err != nil {
		t.Fatalf("PurchaseSummary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d groups, want 1", len(rows))
	}
	if got := rows[0].GrandTotal.StringFixed(0); got != "1500.00" {
		t.Fatalf("GrandTotal = %s, want 1500.00", got)
	}
	if rows[0].DocumentCount != 2 {
		t.Fatalf("DocumentCount = %d, want 2", rows[0].DocumentCount)
	}
}

func TestReporting_StockValuation_ReflectsOpeningMinusSale(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	// setupSalesFixture opens 100 PCS @ cost 50; sell 10.
	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100")

	rows, err := reportingSvc.StockValuation(ctx, fx.Principal, reportingdomain.Filter{WarehouseID: &fx.WarehouseID})
	if err != nil {
		t.Fatalf("StockValuation: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.ProductVariantID == fx.VariantID {
			found = true
			if r.QuantityOnHand != "90" {
				t.Fatalf("QuantityOnHand = %s, want 90 (100 opening - 10 sold)", r.QuantityOnHand)
			}
			if got := r.TotalValue.StringFixed(0); got != "4500.00" {
				t.Fatalf("TotalValue = %s, want 4500.00 (90 * 50 cost)", got)
			}
		}
	}
	if !found {
		t.Fatal("stock valuation report did not include the fixture's product variant")
	}
}

func TestReporting_TrialBalance_DebitsEqualCreditsAfterSale(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100")

	rows, err := reportingSvc.TrialBalance(ctx, fx.Principal, time.Now().AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("TrialBalance: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("trial balance is empty after a finalized sale")
	}
	totalDebit, totalCredit := 0.0, 0.0
	for _, r := range rows {
		var d, c float64
		fmt.Sscanf(r.Debit.StringFixed(0), "%f", &d)
		fmt.Sscanf(r.Credit.StringFixed(0), "%f", &c)
		totalDebit += d
		totalCredit += c
	}
	if diff := totalDebit - totalCredit; diff > 0.001 || diff < -0.001 {
		t.Fatalf("trial balance does not balance: total debit=%.2f total credit=%.2f", totalDebit, totalCredit)
	}
}

func TestReporting_ReceivablesSummary_ShowsOutstandingAfterPartialReceipt(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	// 100 PCS @ 100, exclusive, 18% -> taxable 10000, grand 11800.
	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "100", "100")
	if _, err := accountingSvc.RecordReceipt(ctx, fx.Principal, accountingapp.RecordReceiptParams{
		PartyID: fx.CustomerID, Amount: mustDecimal(t, "1800"), Method: accountingdomain.MethodCash, ReceivedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordReceipt: %v", err)
	}

	rows, err := reportingSvc.ReceivablesSummary(ctx, fx.Principal, time.Now().AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ReceivablesSummary: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.PartyID == fx.CustomerID {
			found = true
			if got := r.Total.StringFixed(0); got != "10000.00" {
				t.Fatalf("outstanding total = %s, want 10000.00 (11800 - 1800)", got)
			}
		}
	}
	if !found {
		t.Fatal("receivables summary did not include the customer with an outstanding balance")
	}
}

func TestReporting_HSNSummary_AggregatesTaxComponents(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100") // taxable 1000, CGST 90 + SGST 90 (intra-state 18%)

	rows, err := reportingSvc.HSNSummary(ctx, fx.Principal, reportingdomain.Filter{})
	if err != nil {
		t.Fatalf("HSNSummary: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d HSN buckets, want 1", len(rows))
	}
	r := rows[0]
	if r.HSNSACCode != fx.HSN {
		t.Fatalf("HSNSACCode = %q, want %q", r.HSNSACCode, fx.HSN)
	}
	if got := r.CGST.StringFixed(0); got != "90.00" {
		t.Fatalf("CGST = %s, want 90.00", got)
	}
	if got := r.SGST.StringFixed(0); got != "90.00" {
		t.Fatalf("SGST = %s, want 90.00", got)
	}
	if !r.IGST.IsZero() {
		t.Fatalf("IGST = %s, want 0 (intra-state fixture)", r.IGST.StringFixed(0))
	}
}

// TestReporting_GSTR3B_OutwardAndITC_MatchHandComputed exercises both
// halves of the report in one pass: a finalized TAX_INVOICE feeds
// 3.1(a) (same fixture math as TestReporting_HSNSummary_
// AggregatesTaxComponents above), and a finalized PURCHASE_INVOICE from
// a GST-registered supplier feeds 4(A)(5) — proving migrations/0038's
// purchase-side tax tracking actually reaches this report, not just
// purchases' own accounting split (already covered by
// TestPurchases_TaxCalculation_IntraState_SplitsInputTaxCreditFromPurchases
// in accounting_test.go).
func TestReporting_GSTR3B_OutwardAndITC_MatchHandComputed(t *testing.T) {
	ctx := context.Background()
	salesSvc, purchasesSvc, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100") // taxable 1000, CGST 90 + SGST 90

	contactsSvc := contactsapp.NewService(
		sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool),
	)
	if _, err := contactsSvc.AddTaxRegistration(ctx, fx.Principal, contactsapp.AddTaxRegistrationParams{
		PartyID: fx.SupplierID, CountryCode: "IN", RegistrationNumber: "27BBBBB0000B1Z1", StateCode: "27", IsPrimary: true,
	}); err != nil {
		t.Fatalf("AddTaxRegistration(supplier): %v", err)
	}
	purchaseDoc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierID,
		DocumentType: purchasesdomain.DocPurchaseInvoice, CurrencyCode: "INR", DocumentDate: time.Now(),
	})
	if err != nil {
		t.Fatalf("purchases CreateDocument: %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: purchaseDoc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "5"), UnitPrice: mustDecimal(t, "100"), // taxable 500, CGST 45 + SGST 45
	}); err != nil {
		t.Fatalf("purchases AddLine: %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, purchaseDoc.ID); err != nil {
		t.Fatalf("purchases FinalizeDocument: %v", err)
	}

	rows, err := reportingSvc.GSTR3B(ctx, fx.Principal, reportingdomain.Filter{})
	if err != nil {
		t.Fatalf("GSTR3B: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d GSTR-3B rows, want 3 (3.1(a), 3.1(b), 4(A)(5))", len(rows))
	}

	outward := rows[0]
	if outward.Label != "3.1(a) Outward taxable supplies" {
		t.Fatalf("rows[0].Label = %q, want 3.1(a)", outward.Label)
	}
	if got := outward.TaxableAmount.StringFixed(0); got != "1000.00" {
		t.Fatalf("outward taxable = %s, want 1000.00", got)
	}
	if got := outward.CGST.StringFixed(0); got != "90.00" {
		t.Fatalf("outward CGST = %s, want 90.00", got)
	}
	if got := outward.SGST.StringFixed(0); got != "90.00" {
		t.Fatalf("outward SGST = %s, want 90.00", got)
	}

	zeroRated := rows[1]
	if zeroRated.Label != "3.1(b) Outward taxable supplies (zero rated)" {
		t.Fatalf("rows[1].Label = %q, want 3.1(b)", zeroRated.Label)
	}
	if !zeroRated.TaxableAmount.Decimal().IsZero() {
		t.Fatalf("zero-rated taxable = %s, want 0 (no export/SEZ documents in this fixture)", zeroRated.TaxableAmount.StringFixed(0))
	}

	itc := rows[2]
	if itc.Label != "4(A)(5) All other ITC" {
		t.Fatalf("rows[2].Label = %q, want 4(A)(5)", itc.Label)
	}
	if got := itc.TaxableAmount.StringFixed(0); got != "500.00" {
		t.Fatalf("ITC taxable = %s, want 500.00", got)
	}
	if got := itc.CGST.StringFixed(0); got != "45.00" {
		t.Fatalf("ITC CGST = %s, want 45.00", got)
	}
	if got := itc.SGST.StringFixed(0); got != "45.00" {
		t.Fatalf("ITC SGST = %s, want 45.00", got)
	}
	if !itc.IGST.Decimal().IsZero() {
		t.Fatalf("ITC IGST = %s, want 0 (intra-state fixture)", itc.IGST.StringFixed(0))
	}
}

func TestReporting_Dashboard_ReflectsTodayActivity(t *testing.T) {
	ctx := context.Background()
	salesSvc, purchasesSvc, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100") // grand total 1180
	finalizePurchase(t, ctx, purchasesSvc, fx, "50", "20")      // 1000

	d, err := reportingSvc.Dashboard(ctx, fx.Principal)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got := d.TodaySales.StringFixed(0); got != "1180.00" {
		t.Fatalf("TodaySales = %s, want 1180.00", got)
	}
	if got := d.TodayPurchases.StringFixed(0); got != "1000.00" {
		t.Fatalf("TodayPurchases = %s, want 1000.00", got)
	}
	if got := d.OutstandingReceivable.StringFixed(0); got != "1180.00" {
		t.Fatalf("OutstandingReceivable = %s, want 1180.00 (nothing received yet)", got)
	}
	// Stock value is conserved through a weighted-average receipt: 100
	// opening @ cost 50 (value 5000), sell 10 (90 @ 50 = value 4500), then
	// receive 50 more @ cost 20 (value 1000) — weighted-average total value
	// after a receipt is exactly old_value + received_value regardless of
	// the blended per-unit rate (4500 + 1000 = 5500), so this is the
	// correct expected figure, not the pre-purchase 4500.
	if got := d.CurrentStockValue.StringFixed(0); got != "5500.00" {
		t.Fatalf("CurrentStockValue = %s, want 5500.00 (4500 post-sale + 1000 from the purchase receipt)", got)
	}
}

// TestReporting_RLS_ReportsNeverLeakCrossOrganisationData is the
// report-specific version of Scenario G (brief §79) — priority #1 per the
// task brief: a report queried under one organisation's principal, even
// with the broadest possible (empty) filter, must never surface another
// organisation's rows. Checked across several report types, not just one,
// since a missing tenant filter is a per-query mistake, not a
// module-wide one.
func TestReporting_RLS_ReportsNeverLeakCrossOrganisationData(t *testing.T) {
	ctx := context.Background()
	salesSvc, purchasesSvc, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)

	fxA := setupAccountingFixture(t, ctx, accountingSvc)
	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fxA, "10", "100")
	finalizePurchase(t, ctx, purchasesSvc, fxA, "50", "20")

	fxB := setupAccountingFixture(t, ctx, accountingSvc) // a second, unrelated organisation

	if rows, err := reportingSvc.SalesInvoiceDetail(ctx, fxB.Principal, reportingdomain.Filter{}); err != nil {
		t.Fatalf("SalesInvoiceDetail as org B: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("RLS FAILED: org B's sales invoice report returned %d of org A's rows", len(rows))
	}

	if rows, err := reportingSvc.PurchaseDetail(ctx, fxB.Principal, reportingdomain.Filter{}); err != nil {
		t.Fatalf("PurchaseDetail as org B: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("RLS FAILED: org B's purchase report returned %d of org A's rows", len(rows))
	}

	if rows, err := reportingSvc.StockValuation(ctx, fxB.Principal, reportingdomain.Filter{}); err != nil {
		t.Fatalf("StockValuation as org B: %v", err)
	} else {
		for _, r := range rows {
			if r.ProductVariantID == fxA.VariantID {
				t.Fatal("RLS FAILED: org B's stock valuation report included org A's product variant")
			}
		}
	}

	if rows, err := reportingSvc.TrialBalance(ctx, fxB.Principal, time.Now().AddDate(0, 0, 1)); err != nil {
		t.Fatalf("TrialBalance as org B: %v", err)
	} else {
		for _, r := range rows {
			if !r.Debit.IsZero() || !r.Credit.IsZero() {
				t.Fatalf("RLS FAILED: org B's (freshly bootstrapped, no transactions) trial balance shows non-zero activity on %s — likely org A's postings leaking through", r.AccountCode)
			}
		}
	}

	dashB, err := reportingSvc.Dashboard(ctx, fxB.Principal)
	if err != nil {
		t.Fatalf("Dashboard as org B: %v", err)
	}
	if !dashB.TodaySales.IsZero() {
		t.Fatalf("RLS FAILED: org B's dashboard shows non-zero TodaySales (%s) — org A's sale leaked through", dashB.TodaySales.StringFixed(0))
	}
	if !dashB.TodayPurchases.IsZero() {
		t.Fatalf("RLS FAILED: org B's dashboard shows non-zero TodayPurchases (%s) — org A's purchase leaked through", dashB.TodayPurchases.StringFixed(0))
	}

	// And the mirror check: org A must still see its OWN data (proves the
	// isolation is real filtering, not every query just returning empty).
	if rows, err := reportingSvc.SalesInvoiceDetail(ctx, fxA.Principal, reportingdomain.Filter{}); err != nil {
		t.Fatalf("SalesInvoiceDetail as org A: %v", err)
	} else if len(rows) != 1 {
		t.Fatalf("org A's own sales invoice report returned %d rows, want 1", len(rows))
	}
}

// TestReporting_Dashboard_PerformanceSanityCheck seeds a moderate dataset
// (not brief §70's full 100k/1M scale — that's Stage 11) and confirms the
// dashboard summary completes quickly, per docs/adr/0004-dashboard-query-design.md.
func TestReporting_Dashboard_PerformanceSanityCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance sanity check in -short mode")
	}
	ctx := context.Background()
	salesSvc, purchasesSvc, accountingSvc, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, accountingSvc)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	const salesCount, purchaseCount = 500, 200
	// The fixture only opens 100 PCS of stock — selling 1 unit per
	// invoice, 500 times, would run out and fail FinalizeDocument on
	// insufficient stock well before the loop finishes. Top up with one
	// large warm-up receipt first, separate from (and not counted in)
	// the purchaseCount documents seeded below.
	finalizePurchase(t, ctx, purchasesSvc, fx, "10000", "10")
	for i := 0; i < salesCount; i++ {
		finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "1", "10")
	}
	for i := 0; i < purchaseCount; i++ {
		finalizePurchase(t, ctx, purchasesSvc, fx, "1", "5")
	}

	start := time.Now()
	if _, err := reportingSvc.Dashboard(ctx, fx.Principal); err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("dashboard summary over %d sales + %d purchase documents: %s", salesCount, purchaseCount, elapsed)
	if elapsed > 2*time.Second {
		t.Fatalf("dashboard summary took %s for %d+%d documents — too slow for a live-query design (docs/adr/0004)", elapsed, salesCount, purchaseCount)
	}
}
