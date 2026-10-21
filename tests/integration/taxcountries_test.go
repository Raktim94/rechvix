//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	purchasesapp "rechvix/internal/modules/purchases/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
	reportingdomain "rechvix/internal/modules/reporting/domain"
	salesdomain "rechvix/internal/modules/sales/domain"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

// ledger returns, for one source document, the debit and credit totals per
// account code, as exact decimal strings.
func ledger(t *testing.T, ctx context.Context, fx accountingFixture, sourceType string, sourceID uuid.UUID) map[string][2]decimal.Decimal {
	t.Helper()
	out := map[string][2]decimal.Decimal{}
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		rows, err := sharedPool.Q(ctx).Query(ctx, `
			SELECT a.code, COALESCE(SUM(jl.debit_amount),0)::text, COALESCE(SUM(jl.credit_amount),0)::text
			FROM journals j JOIN journal_lines jl ON jl.journal_id = j.id JOIN accounts a ON a.id = jl.account_id
			WHERE j.source_type = $1 AND j.source_id = $2 GROUP BY a.code`, sourceType, sourceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var code, d, c string
			if err := rows.Scan(&code, &d, &c); err != nil {
				return err
			}
			out[code] = [2]decimal.Decimal{decimal.RequireFromString(d), decimal.RequireFromString(c)}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("reading ledger: %v", err)
	}
	return out
}

func wantLedger(t *testing.T, got map[string][2]decimal.Decimal, code, debit, credit string) {
	t.Helper()
	g, ok := got[code]
	if debit == "0" && credit == "0" {
		if ok && (!g[0].IsZero() || !g[1].IsZero()) {
			t.Errorf("account %s should be untouched, got debit %s credit %s", code, g[0], g[1])
		}
		return
	}
	if !ok {
		t.Errorf("account %s missing from the journal (have %v)", code, got)
		return
	}
	if !g[0].Equal(decimal.RequireFromString(debit)) || !g[1].Equal(decimal.RequireFromString(credit)) {
		t.Errorf("account %s: debit %s credit %s, want debit %s credit %s", code, g[0], g[1], debit, credit)
	}
}

func newPurchasesFor(t *testing.T) (*purchasesapp.Service, *contactsapp.Service) {
	t.Helper()
	_, purchasesSvc, _, _ := newTestAccountingServices(t)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	contactsSvc := contactsapp.NewService(sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool),
		contactspg.NewTaxRegistrationRepo(sharedPool), checker, audit.NewPGRecorder(sharedPool))
	return purchasesSvc, contactsSvc
}

func purchaseInvoice(t *testing.T, ctx context.Context, svc *purchasesapp.Service, fx accountingFixture, supplierID uuid.UUID, currency, qty, price string) (*purchasesdomain.Document, error) {
	t.Helper()
	doc, err := svc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: supplierID,
		DocumentType: purchasesdomain.DocPurchaseInvoice, CurrencyCode: currency,
	})
	if err != nil {
		t.Fatalf("purchases CreateDocument: %v", err)
	}
	if _, err := svc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, qty), UnitPrice: mustDecimal(t, price),
	}); err != nil {
		t.Fatalf("purchases AddLine: %v", err)
	}
	return svc.FinalizeDocument(ctx, fx.Principal, doc.ID)
}

func TestTaxCountries_Spain_PurchaseRecordsInputIVA(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	purchasesSvc, _ := newPurchasesFor(t)
	doc, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "10", "100")
	if err != nil {
		t.Fatalf("finalize Spanish purchase: %v", err)
	}
	if doc.TaxDocumentID == nil {
		t.Fatal("a domestic purchase in Spain must record a tax snapshot (input IVA); it was silently skipped")
	}
	// 10 x 100 = 1000 net, IVA 21% = 210 reclaimable, 1210 owed to the supplier.
	j := ledger(t, ctx, fx, "purchase_document", doc.ID)
	wantLedger(t, j, "5000", "1000", "0")
	wantLedger(t, j, "1400", "210", "0")
	wantLedger(t, j, "2000", "0", "1210")
}

func TestTaxCountries_Spain_PurchaseFromForeignSupplierIsRefusedClearly(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	purchasesSvc, contactsSvc := newPurchasesFor(t)
	if _, err := contactsSvc.AddTaxRegistration(ctx, fx.Principal, contactsapp.AddTaxRegistrationParams{
		PartyID: fx.CustomerID, CountryCode: "FR", RegistrationNumber: "FR12345678901", IsPrimary: true,
	}); err != nil {
		t.Fatalf("AddTaxRegistration: %v", err)
	}
	_, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "1", "100")
	if !errors.Is(err, taxdomain.ErrUnsupportedTaxCase) {
		t.Fatalf("want ErrUnsupportedTaxCase for a French supplier, got %v", err)
	}
	if msg := taxdomain.UserMessage(err); !strings.Contains(msg, "another country") {
		t.Errorf("user message %q should explain it is a cross-country purchase", msg)
	}
}

func TestTaxCountries_Spain_CreditNoteReversesIVAProportionally(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	invoice, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "EUR", qty: "10", price: "100"})
	if err != nil {
		t.Fatal(err)
	}
	_, lines, err := salesSvc.GetDocument(ctx, fx.Principal, invoice.ID)
	if err != nil || len(lines) != 1 {
		t.Fatalf("GetDocument: lines=%d err=%v", len(lines), err)
	}
	credit, err := salesSvc.ConvertDocument(ctx, fx.Principal, invoice.ID, salesdomain.DocCreditNote,
		map[uuid.UUID]decimal.Decimal{lines[0].ID: mustDecimal(t, "2")})
	if err != nil {
		t.Fatalf("ConvertDocument(credit note): %v", err)
	}
	finalized, err := salesSvc.FinalizeDocument(ctx, fx.Principal, credit.ID)
	if err != nil {
		t.Fatalf("finalize credit note: %v", err)
	}
	// 2 of 10 units: net 200, IVA 42, total 242, all reversed against the invoice's accounts.
	if got := finalized.GrandTotalAmount.StringFixed(0); got != "242.00" {
		t.Errorf("credit note total = %s, want 242.00", got)
	}
	j := ledger(t, ctx, fx, "sales_document", credit.ID)
	wantLedger(t, j, "1100", "0", "242")
	wantLedger(t, j, "2100", "42", "0")
	data, err := salesSvc.BuildInvoiceData(ctx, fx.Principal, credit.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data.DocumentTypeLabel != "CREDIT NOTE" || len(data.TaxTotals) != 1 || data.TaxTotals[0].Amount != "42.00" {
		t.Errorf("credit note print data: label=%s totals=%+v", data.DocumentTypeLabel, data.TaxTotals)
	}
}

func TestTaxCountries_UK_SaleAndPurchase(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "GB", "GBP", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	sale, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "GBP", qty: "5", price: "40"})
	if err != nil {
		t.Fatal(err)
	}
	// 5 x 40 = 200 net, VAT 20% = 40.
	sj := ledger(t, ctx, fx, "sales_document", sale.ID)
	wantLedger(t, sj, "2100", "0", "40")
	wantLedger(t, sj, "1100", "240", "0")
	purchasesSvc, _ := newPurchasesFor(t)
	purchase, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "GBP", "5", "30")
	if err != nil {
		t.Fatalf("UK purchase: %v", err)
	}
	// 5 x 30 = 150 net, input VAT 30.
	pj := ledger(t, ctx, fx, "purchase_document", purchase.ID)
	wantLedger(t, pj, "1400", "30", "0")
	wantLedger(t, pj, "2000", "0", "180")
}

func TestTaxCountries_US_PurchaseSalesTaxIsPartOfCost(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "US", "USD", "TAXABLE")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	if _, err := newTestOrgService(t).UpdateLegalEntityUSCollectionRegions(ctx, fx.Principal, fx.LegalEntityID, []string{"US-TX"}); err != nil {
		t.Fatal(err)
	}
	sale, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "USD", customerCountry: "US", supplyRegion: "US-TX", qty: "4", price: "250"})
	if err != nil {
		t.Fatal(err)
	}
	// 4 x 250 = 1000, Texas 6.25% = 62.50 collected as a liability.
	sj := ledger(t, ctx, fx, "sales_document", sale.ID)
	wantLedger(t, sj, "2100", "0", "62.5")
	wantLedger(t, sj, "1100", "1062.5", "0")
	purchasesSvc, _ := newPurchasesFor(t)
	purchase, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "USD", "2", "100")
	if err != nil {
		t.Fatalf("US purchase: %v", err)
	}
	if purchase.TaxDocumentID != nil {
		t.Error("a US purchase books sales tax as part of cost; no tax snapshot is expected")
	}
	pj := ledger(t, ctx, fx, "purchase_document", purchase.ID)
	wantLedger(t, pj, "5000", "200", "0")
	wantLedger(t, pj, "1400", "0", "0")
	wantLedger(t, pj, "2000", "0", "200")
}

func accountName(t *testing.T, ctx context.Context, fx accountingFixture, code string) string {
	t.Helper()
	var name string
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT name FROM accounts WHERE organisation_id = $1 AND code = $2`, fx.Principal.OrganisationID, code).Scan(&name)
	})
	if err != nil {
		t.Fatalf("reading account %s: %v", code, err)
	}
	return name
}

func TestTaxCountries_TaxAccountNamesFollowTheCountry(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	for _, tc := range []struct{ country, currency, input, output string }{
		{"ES", "EUR", "IVA soportado (Input VAT)", "IVA repercutido (Output VAT)"},
		{"GB", "GBP", "Input VAT", "Output VAT"},
		{"DE", "EUR", "Input VAT", "Output VAT"},
		{"US", "USD", "Sales Tax Paid", "Sales Tax Payable"},
		{"JP", "JPY", "Input Tax", "Output Tax"},
	} {
		fx := setupRegimeFixture(t, ctx, tc.country, tc.currency, "STANDARD")
		if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
			t.Fatalf("%s: %v", tc.country, err)
		}
		if got := accountName(t, ctx, fx, "1400"); got != tc.input {
			t.Errorf("%s input-tax account = %q, want %q", tc.country, got, tc.input)
		}
		if got := accountName(t, ctx, fx, "2100"); got != tc.output {
			t.Errorf("%s output-tax account = %q, want %q", tc.country, got, tc.output)
		}
	}
	// India keeps the GST wording.
	in := setupAccountingFixture(t, ctx, acct)
	if got := accountName(t, ctx, in, "2100"); got != "GST Output Tax Payable" {
		t.Errorf("India output-tax account = %q, want the GST name", got)
	}
}

func TestTaxCountries_ExistingChartIsRenamedOnlyWhileItHasTheStockName(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	// Simulate a chart created before this change, then an accountant's own rename of the other account.
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		if _, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE accounts SET name = 'GST Input Tax Credit' WHERE code = '1400'`); err != nil {
			return err
		}
		_, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE accounts SET name = 'Mi cuenta de IVA' WHERE code = '2100'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	if got := accountName(t, ctx, fx, "1400"); got != "IVA soportado (Input VAT)" {
		t.Errorf("stock-named account should be renamed, got %q", got)
	}
	if got := accountName(t, ctx, fx, "2100"); got != "Mi cuenta de IVA" {
		t.Errorf("a customised account name must never be overwritten, got %q", got)
	}
}

func findTaxRow(rows []reportingdomain.TaxSummaryRow, dir, name, rate, region string) *reportingdomain.TaxSummaryRow {
	for i := range rows {
		r := &rows[i]
		if r.Direction == dir && r.TaxName == name && r.Rate == rate && r.Region == region {
			return r
		}
	}
	return nil
}

func TestTaxSummary_Spain_OutputInputCreditNoteAndExport(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, acct)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	// Sale 10 x 100 -> base 1000, IVA 210.
	sale, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "EUR", qty: "10", price: "100"})
	if err != nil {
		t.Fatal(err)
	}
	// Credit note for 2 units -> base -200, IVA -42.
	_, lines, _ := salesSvc.GetDocument(ctx, fx.Principal, sale.ID)
	cn, err := salesSvc.ConvertDocument(ctx, fx.Principal, sale.ID, salesdomain.DocCreditNote, map[uuid.UUID]decimal.Decimal{lines[0].ID: mustDecimal(t, "2")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, cn.ID); err != nil {
		t.Fatal(err)
	}
	// Export to the US -> base 300, no VAT.
	if _, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "EUR", customerCountry: "US", qty: "3", price: "100"}); err != nil {
		t.Fatal(err)
	}
	// Purchase 5 x 100 -> base 500, input IVA 105.
	purchasesSvc, _ := newPurchasesFor(t)
	if _, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "5", "100"); err != nil {
		t.Fatal(err)
	}

	rows, err := reportingSvc.TaxSummary(ctx, fx.Principal, reportingdomain.Filter{})
	if err != nil {
		t.Fatalf("TaxSummary: %v", err)
	}
	out := findTaxRow(rows, "OUTPUT", "IVA", "21", "")
	if out == nil || out.TaxableAmount.StringFixed(0) != "800.00" || out.TaxAmount.StringFixed(0) != "168.00" || out.DocumentCount != 2 {
		t.Fatalf("output IVA 21%% = %+v, want base 800.00 tax 168.00 over 2 documents (sale minus credit note)", out)
	}
	in := findTaxRow(rows, "INPUT", "IVA", "21", "")
	if in == nil || in.TaxableAmount.StringFixed(0) != "500.00" || in.TaxAmount.StringFixed(0) != "105.00" {
		t.Fatalf("input IVA 21%% = %+v, want base 500.00 tax 105.00", in)
	}
	exp := findTaxRow(rows, "OUTPUT", "EXPORT", "0", "")
	if exp == nil || exp.TaxableAmount.StringFixed(0) != "300.00" || !exp.TaxAmount.Decimal().IsZero() {
		t.Fatalf("export row = %+v, want base 300.00 and no tax", exp)
	}
	for _, r := range rows {
		if r.TaxAmount.Currency() != "EUR" {
			t.Errorf("row %s/%s is %s, want EUR", r.Direction, r.TaxName, r.TaxAmount.Currency())
		}
	}
}

func TestTaxSummary_US_SalesTaxByState(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	reportingSvc := newTestReportingService(t, acct)
	fx := setupRegimeFixture(t, ctx, "US", "USD", "TAXABLE")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	if _, err := newTestOrgService(t).UpdateLegalEntityUSCollectionRegions(ctx, fx.Principal, fx.LegalEntityID, []string{"US-CA", "US-TX"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct{ region, qty string }{{"US-TX", "4"}, {"US-TX", "1"}, {"US-CA", "2"}, {"US-NY", "1"}} {
		if _, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, regimeInvoice{currency: "USD", customerCountry: "US", supplyRegion: s.region, qty: s.qty, price: "100"}); err != nil {
			t.Fatalf("%s: %v", s.region, err)
		}
	}
	rows, err := reportingSvc.TaxSummary(ctx, fx.Principal, reportingdomain.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// Texas 6.25% on 500 = 31.25; California 7.25% on 200 = 14.50; New York not registered: base 100, no tax.
	tx := findTaxRow(rows, "OUTPUT", "STATE_SALES_TAX", "6.25", "US-TX")
	ca := findTaxRow(rows, "OUTPUT", "STATE_SALES_TAX", "7.25", "US-CA")
	ny := findTaxRow(rows, "OUTPUT", "NOT_COLLECTED", "0", "US-NY")
	if tx == nil || tx.TaxableAmount.StringFixed(0) != "500.00" || tx.TaxAmount.StringFixed(0) != "31.25" || tx.DocumentCount != 2 {
		t.Errorf("Texas row = %+v, want base 500.00 tax 31.25 over 2 documents", tx)
	}
	if ca == nil || ca.TaxableAmount.StringFixed(0) != "200.00" || ca.TaxAmount.StringFixed(0) != "14.50" {
		t.Errorf("California row = %+v, want base 200.00 tax 14.50", ca)
	}
	if ny == nil || ny.TaxableAmount.StringFixed(0) != "100.00" || !ny.TaxAmount.Decimal().IsZero() {
		t.Errorf("New York row = %+v, want base 100.00 and no tax collected", ny)
	}
}

func TestPrint_CustomerAddressLanguageAndDateFollowTheSellersCountry(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		country, currency, code, lang, layout string
		line1, city, state, postal            string
		wantCity                              string
	}{
		{"ES", "EUR", "STANDARD", "es", "02/01/2006", "Calle Mayor 1", "Madrid", "Madrid", "28013", "28013 Madrid (Madrid)"},
		{"DE", "EUR", "STANDARD", "de", "02.01.2006", "Hauptstraße 5", "Berlin", "", "10115", "10115 Berlin"},
		{"GB", "GBP", "STANDARD", "en", "02-Jan-2006", "10 High Street", "Leeds", "", "LS1 4AB", "Leeds LS1 4AB"},
		{"US", "USD", "TAXABLE", "en", "01/02/2006", "500 Main St", "Austin", "TX", "78701", "Austin, TX 78701"},
	} {
		t.Run(tc.country, func(t *testing.T) {
			salesSvc, _, acct, _ := newTestAccountingServices(t)
			fx := setupRegimeFixture(t, ctx, tc.country, tc.currency, tc.code)
			if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
				t.Fatal(err)
			}
			_, contactsSvc := newPurchasesFor(t)
			if _, err := contactsSvc.AddAddress(ctx, fx.Principal, contactsapp.AddAddressParams{
				PartyID: fx.CustomerID, AddressType: "BILLING", Line1: tc.line1, City: tc.city, State: tc.state, PostalCode: tc.postal, CountryCode: tc.country, IsDefault: true,
			}); err != nil {
				t.Fatal(err)
			}
			if tc.country == "US" {
				if _, err := newTestOrgService(t).UpdateLegalEntityUSCollectionRegions(ctx, fx.Principal, fx.LegalEntityID, []string{"US-TX"}); err != nil {
					t.Fatal(err)
				}
			}
			in := regimeInvoice{currency: tc.currency, qty: "1", price: "100"}
			if tc.country == "US" {
				in.customerCountry, in.supplyRegion = "US", "US-TX"
			}
			doc, err := finalizeRegimeInvoice(t, ctx, salesSvc, fx, in)
			if err != nil {
				t.Fatal(err)
			}
			if doc.BillingAddressID == nil {
				t.Fatal("the customer's default billing address must be attached to a new document")
			}
			data, err := salesSvc.BuildInvoiceData(ctx, fx.Principal, doc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if data.Language != tc.lang || data.DateLayout != tc.layout {
				t.Errorf("language %q layout %q, want %q %q", data.Language, data.DateLayout, tc.lang, tc.layout)
			}
			lines := data.BillTo.AddressLines
			if len(lines) < 3 || lines[0] != tc.line1 || lines[2] != tc.wantCity {
				t.Errorf("address lines = %q, want [%q _ %q]", lines, tc.line1, tc.wantCity)
			}
			if len(lines) > 3 {
				t.Errorf("a domestic address must not print a country line, got %q", lines)
			}
		})
	}
}
