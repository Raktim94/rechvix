//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	reportingapp "rechvix/internal/modules/reporting/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/platform/permissions"
	"testing"
	"time"
)

func TestAccountantPackage_SpainReconcilesCorrectionsAndPurchases(t *testing.T) {
	ctx := context.Background()
	sales, _, acct, _ := newTestAccountingServices(t)
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	invoice, err := finalizeRegimeInvoice(t, ctx, sales, fx, regimeInvoice{currency: "EUR", qty: "10", price: "100", surcharge: true})
	if err != nil {
		t.Fatal(err)
	}
	_, lines, _ := sales.GetDocument(ctx, fx.Principal, invoice.ID)
	credit, err := sales.ConvertDocument(ctx, fx.Principal, invoice.ID, salesdomain.DocCreditNote, map[uuid.UUID]decimal.Decimal{lines[0].ID: mustDecimal(t, "2")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sales.FinalizeDocument(ctx, fx.Principal, credit.ID); err != nil {
		t.Fatal(err)
	}
	purchases, _ := newPurchasesFor(t)
	if _, err := purchaseInvoice(t, ctx, purchases, fx, fx.CustomerID, "EUR", "5", "100"); err != nil {
		t.Fatal(err)
	}
	report := newTestReportingService(t, acct)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	pack, err := report.AccountantPackage(ctx, fx.Principal, fx.LegalEntityID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if pack.DocumentCount != 3 || len(pack.Data.Lines) != 3 {
		t.Fatalf("wrong snapshot counts: %d / %d", pack.DocumentCount, len(pack.Data.Lines))
	}
	amounts := map[string]string{}
	for _, b := range pack.Boxes {
		amounts[b.Code] = b.Amount
	}
	for code, want := range map[string]string{"07": "1000.00", "09": "210.00", "14": "-200.00", "15": "-42.00", "22": "1000.00", "24": "52.00", "25": "-200.00", "26": "-10.40", "INPUT_TAX_CANDIDATE": "105.00", "NET_TAX_RECORDED": "104.60"} {
		if amounts[code] != want {
			t.Errorf("box %s = %s want %s", code, amounts[code], want)
		}
	}
	for _, check := range pack.Checks {
		if check.Code == "LEDGER_TAX_MISMATCH" || check.Code == "SNAPSHOT_MISMATCH" || check.Code == "JOURNAL_RECONCILIATION" {
			t.Errorf("unexpected reconciliation issue %+v", check)
		}
	}
	if amounts["29"] != "" {
		t.Fatal("input eligibility must not be assumed")
	}
	archive, err := pack.ZIP()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, f := range z.File {
		files[f.Name] = true
	}
	for _, name := range []string{"accountant.xlsx", "Documents.csv", "Tax lines.csv", "Tax components.csv", "Checks.csv", "manifest.json", "raw-records.json"} {
		if !files[name] {
			t.Errorf("missing %s", name)
		}
	}
	// A different tenant cannot retrieve company data even with its own full rights.
	other := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if _, err := report.AccountantPackage(ctx, other.Principal, fx.LegalEntityID, from, to); err == nil {
		t.Fatal("cross-tenant export succeeded")
	}
	// No permissions: neither previews nor file bytes may be generated.
	denied := permissions.Principal{OrganisationID: fx.Principal.OrganisationID, UserID: uuid.New()}
	var forbidden *permissions.ErrForbidden
	if _, err := report.AccountantPackage(ctx, denied, fx.LegalEntityID, from, to); !errors.As(err, &forbidden) {
		t.Fatalf("want permission denial, got %v", err)
	}
	if _, err := report.AccountantPackage(ctx, fx.Principal, fx.LegalEntityID, to, from); !errors.Is(err, reportingapp.ErrInvalidAccountantRequest) {
		t.Fatalf("reversed period: %v", err)
	}
}

func TestAccountantPackage_CountryIsolationAndExactDecimals(t *testing.T) {
	ctx := context.Background()
	sales, _, acct, _ := newTestAccountingServices(t)
	report := newTestReportingService(t, acct)
	for _, tc := range []struct{ country, currency, region string }{{"GB", "GBP", ""}, {"US", "USD", "US-CA"}} {
		code := "STANDARD"
		if tc.country == "US" {
			code = "TAXABLE"
		}
		fx := setupRegimeFixture(t, ctx, tc.country, tc.currency, code)
		if tc.country == "US" {
			if _, err := newTestOrgService(t).UpdateLegalEntityUSCollectionRegions(ctx, fx.Principal, fx.LegalEntityID, []string{"US-CA"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
			t.Fatal(err)
		}
		if _, err := finalizeRegimeInvoice(t, ctx, sales, fx, regimeInvoice{currency: tc.currency, qty: "1", price: "19.99", supplyRegion: tc.region}); err != nil {
			t.Fatal(err)
		}
		pack, err := report.AccountantPackage(ctx, fx.Principal, fx.LegalEntityID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if pack.Company.Country != tc.country || pack.Company.Currency != tc.currency || pack.DocumentCount != 1 {
			t.Fatalf("cross-company or currency mix: %+v", pack.Company)
		}
		if tc.country == "GB" {
			for _, b := range pack.Boxes {
				if b.Code == "1" && b.Amount != "4.00" {
					t.Errorf("UK VAT %s", b.Amount)
				}
				if b.Code == "2" && b.Amount != "" {
					t.Fatal("NI must stay unresolved")
				}
			}
		}
		if tc.country == "US" {
			found := false
			for _, table := range pack.Tables {
				if table.Name == "US US-CA" {
					found = true
				}
			}
			if !found {
				t.Fatal("missing separate CA state schedule")
			}
		}
	}
}
