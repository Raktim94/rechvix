//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	accountingpg "rechvix/internal/modules/accounting/pg"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/modules/gstindia"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	inventoryapp "rechvix/internal/modules/inventory/app"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingpg "rechvix/internal/modules/pricing/pg"
	purchasesapp "rechvix/internal/modules/purchases/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
	purchasespg "rechvix/internal/modules/purchases/pg"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	salespg "rechvix/internal/modules/sales/pg"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
	taxationpg "rechvix/internal/modules/taxation/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/numbering"
	"rechvix/internal/platform/permissions"
)

// newTestAccountingServices wires sales, purchases, and accounting together
// exactly as apps/server/main.go composes them (unlike sales_test.go's/
// purchases_test.go's own helpers, which deliberately pass nil accounting
// to avoid disturbing their pre-Stage-6 fixtures — see the comments there).
func newTestAccountingServices(t *testing.T) (*salesapp.Service, *purchasesapp.Service, *accountingapp.Service, *inventoryapp.Service) {
	t.Helper()
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)

	accountingSvc := accountingapp.NewService(
		sharedPool,
		accountingpg.NewAccountRepo(sharedPool),
		accountingpg.NewJournalRepo(sharedPool),
		accountingpg.NewJournalLineRepo(sharedPool),
		accountingpg.NewFiscalPeriodRepo(sharedPool),
		accountingpg.NewBankAccountRepo(sharedPool),
		accountingpg.NewReceiptRepo(sharedPool),
		accountingpg.NewPaymentRepo(sharedPool),
		accountingpg.NewReconciliationRepo(sharedPool),
		checker, recorder,
	)

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
		sharedPool, gstEngine,
		taxationpg.NewTaxDocumentRepo(sharedPool), taxationpg.NewTaxLineRepo(sharedPool), taxationpg.NewTaxComponentRepo(sharedPool),
	)
	numberingSvc := numbering.NewService(sharedPool, numbering.NewPGRepository(sharedPool))
	pricingSvc := pricingapp.NewService(sharedPool, pricingpg.NewPriceListRepo(sharedPool), pricingpg.NewPriceListItemRepo(sharedPool), checker, recorder)

	salesSvc := salesapp.NewService(
		sharedPool, salespg.NewDocumentRepo(sharedPool), salespg.NewDocumentLineRepo(sharedPool),
		inventorySvc, taxationSvc, catalogueSvc, contactsSvc, orgSvc, pricingSvc, numberingSvc, accountingSvc, nil,
		checker, recorder,
	)
	purchasesSvc := purchasesapp.NewService(
		sharedPool, purchasespg.NewDocumentRepo(sharedPool), purchasespg.NewDocumentLineRepo(sharedPool),
		inventorySvc, accountingSvc, checker, recorder,
	)
	return salesSvc, purchasesSvc, accountingSvc, inventorySvc
}

type accountingFixture struct {
	salesFixture
	SupplierID uuid.UUID
}

func setupAccountingFixture(t *testing.T, ctx context.Context, accountingSvc *accountingapp.Service) accountingFixture {
	t.Helper()
	fx := setupSalesFixture(t, ctx)
	if err := accountingSvc.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatalf("EnsureDefaultChartOfAccounts: %v", err)
	}
	contactsSvc := contactsapp.NewService(
		sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool),
	)
	supplier, err := contactsSvc.CreateParty(ctx, fx.Principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartySupplier, LegalName: "Test Supplier " + uuid.NewString()[:8], CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateParty(supplier): %v", err)
	}
	return accountingFixture{salesFixture: fx, SupplierID: supplier.ID}
}

func accountIDByCode(t *testing.T, ctx context.Context, orgID uuid.UUID, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT id FROM accounts WHERE organisation_id = $1 AND code = $2`, orgID, code).Scan(&id)
	})
	if err != nil {
		t.Fatalf("looking up account %s: %v", code, err)
	}
	return id
}

// --- Layer 2: deferred constraint trigger (fires at COMMIT) ---

func TestAccounting_DeferredTrigger_RejectsUnbalancedRawSQL(t *testing.T) {
	ctx := context.Background()
	_, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	cash := accountIDByCode(t, ctx, fx.Principal.OrganisationID, accountingdomain.CodeCash)
	ar := accountIDByCode(t, ctx, fx.Principal.OrganisationID, accountingdomain.CodeAccountsReceivable)
	journalID := uuid.Must(uuid.NewV7())

	// Entirely raw SQL — bypasses accounting.PostTx and its Layer-1
	// application check completely, to prove Layer 2 (the DB trigger)
	// holds on its own even if the Go application layer is bypassed by a
	// future bug or a manual fix script (docs/architecture.md §7).
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		if _, err := sharedPool.Q(ctx).Exec(ctx,
			`INSERT INTO journals (id, organisation_id, source_type, journal_date, created_by) VALUES ($1,$2,'manual',now(),$3)`,
			journalID, fx.Principal.OrganisationID, fx.Principal.UserID); err != nil {
			return err
		}
		if _, err := sharedPool.Q(ctx).Exec(ctx,
			`INSERT INTO journal_lines (id, organisation_id, journal_id, account_id, debit_amount, credit_amount) VALUES ($1,$2,$3,$4,100,0)`,
			uuid.Must(uuid.NewV7()), fx.Principal.OrganisationID, journalID, cash); err != nil {
			return err
		}
		// Deliberately unbalanced: 100 debit vs 90 credit.
		if _, err := sharedPool.Q(ctx).Exec(ctx,
			`INSERT INTO journal_lines (id, organisation_id, journal_id, account_id, debit_amount, credit_amount) VALUES ($1,$2,$3,$4,0,90)`,
			uuid.Must(uuid.NewV7()), fx.Principal.OrganisationID, journalID, ar); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected the deferred constraint trigger to reject an unbalanced journal at COMMIT, got no error")
	}
	if !strings.Contains(err.Error(), "unbalanced") {
		t.Fatalf("expected the trigger's 'unbalanced' message, got: %v", err)
	}
}

func TestAccounting_DeferredTrigger_AcceptsBalancedRawSQL(t *testing.T) {
	ctx := context.Background()
	_, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	cash := accountIDByCode(t, ctx, fx.Principal.OrganisationID, accountingdomain.CodeCash)
	ar := accountIDByCode(t, ctx, fx.Principal.OrganisationID, accountingdomain.CodeAccountsReceivable)
	journalID := uuid.Must(uuid.NewV7())

	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		if _, err := sharedPool.Q(ctx).Exec(ctx,
			`INSERT INTO journals (id, organisation_id, source_type, journal_date, created_by) VALUES ($1,$2,'manual',now(),$3)`,
			journalID, fx.Principal.OrganisationID, fx.Principal.UserID); err != nil {
			return err
		}
		if _, err := sharedPool.Q(ctx).Exec(ctx,
			`INSERT INTO journal_lines (id, organisation_id, journal_id, account_id, debit_amount, credit_amount) VALUES ($1,$2,$3,$4,100,0)`,
			uuid.Must(uuid.NewV7()), fx.Principal.OrganisationID, journalID, cash); err != nil {
			return err
		}
		if _, err := sharedPool.Q(ctx).Exec(ctx,
			`INSERT INTO journal_lines (id, organisation_id, journal_id, account_id, debit_amount, credit_amount) VALUES ($1,$2,$3,$4,0,100)`,
			uuid.Must(uuid.NewV7()), fx.Principal.OrganisationID, journalID, ar); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected a genuinely balanced journal to commit cleanly, got: %v", err)
	}
}

// --- Layer 3: journal_lines are immutable from the instant they're written ---

func TestAccounting_JournalLines_ImmutableRejectsUpdate(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	doc := finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100")
	lineID := firstJournalLineID(t, ctx, fx.Principal.OrganisationID, doc.ID)

	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE journal_lines SET debit_amount = debit_amount + 1 WHERE id = $1`, lineID)
		return err
	})
	if err == nil {
		t.Fatal("expected an UPDATE on a posted journal_lines row to be rejected, got no error")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected the trigger's 'immutable' message, got: %v", err)
	}
}

func TestAccounting_JournalLines_ImmutableRejectsDelete(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	doc := finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100")
	lineID := firstJournalLineID(t, ctx, fx.Principal.OrganisationID, doc.ID)

	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `DELETE FROM journal_lines WHERE id = $1`, lineID)
		return err
	})
	if err == nil {
		t.Fatal("expected a DELETE on a posted journal_lines row to be rejected, got no error")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected the trigger's 'immutable' message, got: %v", err)
	}
}

// --- Auto-posting on finalize ---

func TestAccounting_AutoPostOnSalesFinalize(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	// 10 * 100 = 1000 taxable, exclusive, 18% GST intra-state -> 90 CGST +
	// 90 SGST -> grand total 1180 (same fixture as
	// TestSales_TaxInvoice_FinalizePostsTaxSnapshotAndStock).
	doc := finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100")

	entries, err := accountingSvc.GetPartyLedger(ctx, fx.Principal, fx.CustomerID, time.Now())
	if err != nil {
		t.Fatalf("GetPartyLedger: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("customer ledger has %d entries, want exactly 1 (the sale's AR debit)", len(entries))
	}
	if got := entries[0].Debit.StringFixed(money.RoundHalfUp); got != "1180.00" {
		t.Fatalf("AR debit = %s, want 1180.00", got)
	}
	if entries[0].SourceType != "sales_document" || entries[0].SourceID == nil || *entries[0].SourceID != doc.ID {
		t.Fatalf("ledger entry source = %s/%v, want sales_document/%s", entries[0].SourceType, entries[0].SourceID, doc.ID)
	}
}

func TestAccounting_AutoPostOnPurchaseFinalize(t *testing.T) {
	ctx := context.Background()
	_, purchasesSvc, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	doc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierID,
		DocumentType: purchasesdomain.DocPurchaseInvoice, CurrencyCode: "INR", DocumentDate: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "5"), UnitPrice: mustDecimal(t, "200"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}

	entries, err := accountingSvc.GetPartyLedger(ctx, fx.Principal, fx.SupplierID, time.Now())
	if err != nil {
		t.Fatalf("GetPartyLedger: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("supplier ledger has %d entries, want exactly 1", len(entries))
	}
	// AP is credited (increases what we owe) — RunningBalance is
	// cumulative(debit)-cumulative(credit), so a pure-credit AP ledger
	// shows a NEGATIVE running balance; -RunningBalance is the payable.
	if got := entries[0].Credit.StringFixed(money.RoundHalfUp); got != "1000.00" {
		t.Fatalf("AP credit = %s, want 1000.00 (5 * 200)", got)
	}
	if !entries[0].RunningBalance.IsNegative() {
		t.Fatalf("running balance = %s, want negative (a payable, from the customer-ledger sign convention)", entries[0].RunningBalance)
	}
}

// finalizeSimpleTaxInvoice creates and finalizes one TAX_INVOICE line
// (qty * price, exclusive pricing, the fixture's 18% intra-state HSN) —
// shared by several tests above that only need "some finalized sale
// exists" rather than caring about the exact totals themselves.
func finalizeSimpleTaxInvoice(t *testing.T, ctx context.Context, salesSvc *salesapp.Service, fx accountingFixture, qty, price string) *salesdomain.Document {
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
		Quantity: mustDecimal(t, qty), UnitPrice: mustDecimal(t, price),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	finalized, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}
	return finalized
}

func firstJournalLineID(t *testing.T, ctx context.Context, orgID, salesDocumentID uuid.UUID) uuid.UUID {
	t.Helper()
	var journalID, lineID uuid.UUID
	err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		if err := sharedPool.Q(ctx).QueryRow(ctx,
			`SELECT id FROM journals WHERE organisation_id = $1 AND source_type = 'sales_document' AND source_id = $2`,
			orgID, salesDocumentID).Scan(&journalID); err != nil {
			return err
		}
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT id FROM journal_lines WHERE journal_id = $1 LIMIT 1`, journalID).Scan(&lineID)
	})
	if err != nil {
		t.Fatalf("looking up posted journal line: %v", err)
	}
	return lineID
}

// TestAccounting_ManualExpense_ListedBySourceType is the Expenses page's
// backing query, proven directly against Service.Post (the same
// standalone entry point postJournal's HTTP handler wraps) rather than
// through the HTTP layer: two manual_expense journals at different
// amounts/accounts both come back from ListExpenseEntries, newest first,
// each row carrying the debit-side account's own name and amount — and
// a real auto-posted sales journal (source_type "sales_document",
// already proven elsewhere) does NOT leak into the same list, since
// ListDebitLinesBySourceType filters by source_type.
func TestAccounting_ManualExpense_ListedBySourceType(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	post := func(accountCode string, amount string, description string) {
		t.Helper()
		if _, err := accountingSvc.Post(ctx, fx.Principal, accountingapp.JournalRequest{
			OrganisationID: fx.Principal.OrganisationID, SourceType: "manual_expense", JournalDate: time.Now(),
			Description: description, CreatedBy: fx.Principal.UserID,
			Lines: []accountingapp.JournalLineRequest{
				{AccountCode: accountCode, Debit: mustDecimal(t, amount), Description: description},
				{AccountCode: accountingdomain.CodeCash, Credit: mustDecimal(t, amount), Description: description},
			},
		}); err != nil {
			t.Fatalf("Post(manual_expense %s %s): %v", accountCode, amount, err)
		}
	}
	post(accountingdomain.CodeGeneralExpenses, "500", "Electricity bill")
	post(accountingdomain.CodeFreight, "150", "Auto-rickshaw delivery charge")

	// A real, differently-sourced journal (source_type "sales_document",
	// auto-posted on finalize — same fixture as
	// TestAccounting_AutoPostOnSalesFinalize) must not leak into the
	// expense list just because ListDebitLinesBySourceType filters by
	// source_type, not by account type.
	finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "10", "100")

	entries, err := accountingSvc.ListExpenseEntries(ctx, fx.Principal, 0)
	if err != nil {
		t.Fatalf("ListExpenseEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	// Newest first: Freight (posted second) before General Expenses.
	if entries[0].AccountCode != accountingdomain.CodeFreight || !entries[0].Amount.Decimal().Equal(mustDecimal(t, "150")) {
		t.Fatalf("entries[0] = %+v, want Freight/150", entries[0])
	}
	if entries[1].AccountCode != accountingdomain.CodeGeneralExpenses || !entries[1].Amount.Decimal().Equal(mustDecimal(t, "500")) {
		t.Fatalf("entries[1] = %+v, want General Expenses/500", entries[1])
	}
	if entries[0].AccountName == "" || entries[1].Description == "" {
		t.Fatalf("expected account name and description to be populated, got %+v / %+v", entries[0], entries[1])
	}
}

// --- Scenario E (brief §79): credit-sale invoice, partial receipt, ledger outstanding ---

func TestAccounting_ScenarioE_CreditSaleThenPartialReceipt(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	// Tax-INCLUSIVE, qty 1 @ ₹10,000 — grand total stays exactly ₹10,000
	// (docs/architecture.md §5's whole point of the inclusive-pricing
	// design), so the fixture's own numbers ARE the brief's ₹10,000 figure
	// directly, no separate GST arithmetic to account for here.
	doc, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, PlaceOfSupplyStateCode: "27", CurrencyCode: "INR", BaseCurrencyCode: "INR",
		PricingMode: taxdomain.PricingInclusive,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "1"), UnitPrice: mustDecimal(t, "10000"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	finalized, err := salesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}
	if got := finalized.GrandTotalAmount.StringFixed(money.RoundHalfUp); got != "10000.00" {
		t.Fatalf("invoice grand total = %s, want exactly 10000.00 (tax-inclusive pricing must reconcile to the entered gross)", got)
	}

	if _, err := accountingSvc.RecordReceipt(ctx, fx.Principal, accountingapp.RecordReceiptParams{
		PartyID: fx.CustomerID, SalesDocumentID: &doc.ID, Amount: mustDecimal(t, "4000"),
		Method: accountingdomain.MethodCash, ReceivedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordReceipt: %v", err)
	}

	entries, err := accountingSvc.GetPartyLedger(ctx, fx.Principal, fx.CustomerID, time.Now())
	if err != nil {
		t.Fatalf("GetPartyLedger: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("customer ledger has %d entries, want 2 (the sale, then the receipt)", len(entries))
	}
	last := entries[len(entries)-1]
	if got := last.RunningBalance.StringFixed(money.RoundHalfUp); got != "6000.00" {
		t.Fatalf("outstanding balance after ₹10,000 sale + ₹4,000 receipt = %s, want exactly 6000.00 (Scenario E)", got)
	}
}

// --- RLS ---

func TestAccounting_RLS_BlocksCrossOrganisationJournalRead(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fxA := setupAccountingFixture(t, ctx, accountingSvc)
	fxB := setupAccountingFixture(t, ctx, accountingSvc)

	doc := finalizeSimpleTaxInvoice(t, ctx, salesSvc, fxA, "1", "100")

	var journalID uuid.UUID
	err := sharedPool.RunScoped(ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx,
			`SELECT id FROM journals WHERE source_type = 'sales_document' AND source_id = $1`, doc.ID).Scan(&journalID)
	})
	if err != nil {
		t.Fatalf("looking up org A's journal: %v", err)
	}

	// Deliberately raw SQL with NO organisation_id in the WHERE clause —
	// scoped only via RunScoped to org B — so this exercises RLS itself,
	// not an application-layer WHERE filter (same pattern as
	// tests/integration/rls_test.go's TestRLS_BlocksCrossOrganisationReads).
	var found bool
	err = sharedPool.RunScoped(ctx, fxB.Principal.OrganisationID, func(ctx context.Context) error {
		var id uuid.UUID
		getErr := sharedPool.Q(ctx).QueryRow(ctx, `SELECT id FROM journals WHERE id = $1`, journalID).Scan(&id)
		found = getErr == nil
		return nil
	})
	if err != nil {
		t.Fatalf("querying journals as org B: %v", err)
	}
	if found {
		t.Fatal("RLS FAILED: org B's transaction could read org A's journal by ID")
	}

	found = false
	err = sharedPool.RunScoped(ctx, fxB.Principal.OrganisationID, func(ctx context.Context) error {
		rows, qerr := sharedPool.Q(ctx).Query(ctx, `SELECT id FROM journal_lines WHERE journal_id = $1`, journalID)
		if qerr != nil {
			return qerr
		}
		defer rows.Close()
		for rows.Next() {
			found = true
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("querying journal_lines as org B: %v", err)
	}
	if found {
		t.Fatal("RLS FAILED: org B's transaction could read org A's journal_lines")
	}
}

// --- Fiscal period locking ---

func TestAccounting_FiscalPeriod_LockLifecycleAndOverride(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)

	now := time.Now()
	period, err := accountingSvc.CreateFiscalPeriod(ctx, fx.Principal, accountingapp.CreateFiscalPeriodParams{
		StartDate: now.AddDate(0, 0, -5), EndDate: now.AddDate(0, 0, 5), Label: "Test Period",
	})
	if err != nil {
		t.Fatalf("CreateFiscalPeriod: %v", err)
	}
	if err := accountingSvc.SetPeriodLock(ctx, fx.Principal, period.ID, true); err != nil {
		t.Fatalf("SetPeriodLock(true): %v", err)
	}

	// The bootstrap Owner holds every permission (identity.Bootstrap's
	// GrantAllPermissions), including accounting.override_locked_period —
	// so posting into the now-locked period must still succeed for them.
	// This proves the override path works; it does NOT prove a
	// non-override user is blocked (that needs a second, deliberately
	// under-permissioned role/user, which this fixture doesn't build —
	// flagged as a follow-up in the final report, not silently skipped).
	doc := finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "1", "50")
	if doc.Status != salesdomain.StatusFinalized {
		t.Fatalf("expected the Owner (who holds accounting.override_locked_period) to finalize into a locked period, got status %s", doc.Status)
	}

	if err := accountingSvc.SetPeriodLock(ctx, fx.Principal, period.ID, false); err != nil {
		t.Fatalf("SetPeriodLock(false): %v", err)
	}
	periods, err := accountingSvc.ListFiscalPeriods(ctx, fx.Principal)
	if err != nil {
		t.Fatalf("ListFiscalPeriods: %v", err)
	}
	var reloaded *accountingdomain.FiscalPeriod
	for _, p := range periods {
		if p.ID == period.ID {
			reloaded = p
		}
	}
	if reloaded == nil {
		t.Fatal("created fiscal period not found in ListFiscalPeriods")
	}
	if reloaded.IsLocked {
		t.Fatal("expected the period to be unlocked after SetPeriodLock(false)")
	}
}
