//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/modules/legacyimport"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

func rowsOf(fields ...map[string]string) []importer.Row {
	out := make([]importer.Row, len(fields))
	for i, f := range fields {
		out[i] = importer.Row{Number: i + 1, Fields: f}
	}
	return out
}

func TestLegacyImport_OpeningBalancesAndHistoryRegister(t *testing.T) {
	ctx := context.Background()
	_, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	contacts := contactsapp.NewService(sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool), checker, audit.NewPGRecorder(sharedPool))
	svc := legacyimport.NewService(sharedPool, accountingSvc, checker, audit.NewPGRecorder(sharedPool))
	reportingSvc := newTestReportingService(t, accountingSvc)

	mk := func(pt contactsdomain.PartyType, name string) uuid.UUID {
		p, err := contacts.CreateParty(ctx, fx.Principal, contactsapp.CreatePartyParams{PartyType: pt, LegalName: name, CurrencyCode: "INR"})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	sfx := uuid.NewString()[:6]
	cust, supp, both := "Asha Stores "+sfx, "Bulk Traders "+sfx, "Dual Co "+sfx
	custID := mk(contactsdomain.PartyCustomer, cust)
	suppID := mk(contactsdomain.PartySupplier, supp)
	mk(contactsdomain.PartyBoth, both)
	mk(contactsdomain.PartyCustomer, "Twin "+sfx)
	mk(contactsdomain.PartyCustomer, "Twin "+sfx)

	asOf := time.Now().AddDate(0, 0, -30)
	file := rowsOf(
		map[string]string{"party_name": cust, "balance": "12500.50"},                        // customer: receivable by default
		map[string]string{"party_name": supp, "balance": "8000"},                            // supplier: payable by default
		map[string]string{"party_name": both, "balance": "100"},                             // both: must say which
		map[string]string{"party_name": both, "balance": "100", "balance_type": "payable"},  // ok once said
		map[string]string{"party_name": "Nobody " + sfx, "balance": "5"},                    // unknown
		map[string]string{"party_name": "Twin " + sfx, "balance": "5"},                      // ambiguous
		map[string]string{"party_name": cust, "balance": "1"},                               // duplicate within the file
		map[string]string{"party_name": supp, "balance": "9", "balance_type": "RECEIVABLE"}, // wrong side for a supplier
		map[string]string{"party_name": cust + " ", "balance": "-5"},                        // not above zero
	)

	// A dry run writes nothing.
	dry, err := svc.ImportOpeningBalances(ctx, fx.Principal, file, asOf, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Valid != 3 || dry.Committed != 0 {
		t.Fatalf("dry run: %+v", dry)
	}
	if e, _ := accountingSvc.GetPartyLedger(ctx, fx.Principal, custID, time.Now()); len(e) != 0 {
		t.Fatalf("a dry run must not post: %+v", e)
	}

	rep, err := svc.ImportOpeningBalances(ctx, fx.Principal, file, asOf, false)
	if err != nil {
		t.Fatal(err)
	}
	outcome := map[int]importer.RowOutcome{}
	for _, r := range rep.Results {
		outcome[r.RowNumber] = r.Outcome
	}
	want := map[int]importer.RowOutcome{1: "COMMITTED", 2: "COMMITTED", 3: "ERROR", 4: "COMMITTED", 5: "ERROR", 6: "ERROR", 7: "DUPLICATE", 8: "ERROR", 9: "ERROR"}
	for n, w := range want {
		if outcome[n] != w {
			t.Fatalf("row %d = %s, want %s (%+v)", n, outcome[n], w, rep.Results)
		}
	}

	// The customer owes 12,500.50 and the supplier is owed 8,000.00, in the right ledgers.
	ce, err := accountingSvc.GetPartyLedger(ctx, fx.Principal, custID, time.Now())
	if err != nil || len(ce) != 1 || ce[len(ce)-1].RunningBalance.StringFixed(2) != "12500.50" {
		t.Fatalf("customer ledger: %+v %v", ce, err)
	}
	se, err := accountingSvc.GetPartyLedger(ctx, fx.Principal, suppID, time.Now())
	if err != nil || len(se) != 1 {
		t.Fatalf("supplier ledger: %+v %v", se, err)
	}
	// Re-running the same file posts nothing more.
	again, _ := svc.ImportOpeningBalances(ctx, fx.Principal, file, asOf, false)
	if again.Committed != 0 || again.Duplicates < 3 {
		t.Fatalf("a re-run must be idempotent: %+v", again)
	}
	// The books still balance.
	tb, err := reportingSvc.TrialBalance(ctx, fx.Principal, time.Now().AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	sumD, sumC := mustDecimal(t, "0"), mustDecimal(t, "0")
	for _, r := range tb {
		sumD, sumC = sumD.Add(r.Debit.Decimal()), sumC.Add(r.Credit.Decimal())
	}
	if !sumD.Equal(sumC) {
		t.Fatalf("trial balance does not balance: debit %s credit %s", sumD, sumC)
	}
	if _, err := svc.ImportOpeningBalances(ctx, fx.Principal, file, time.Now().AddDate(1, 0, 0), true); err == nil {
		t.Fatal("a future opening balance date must be refused")
	}

	// History register: reference only. Ledgers and stock stay untouched.
	before, _ := accountingSvc.GetPartyLedger(ctx, fx.Principal, custID, time.Now())
	inv := rowsOf(
		map[string]string{"party_name": cust, "document_number": "INV-" + sfx + "-1", "document_date": "25/12/2025", "total": "1180", "paid": "180"},
		map[string]string{"party_name": cust, "document_number": "INV-" + sfx + "-1", "document_date": "26/12/2025", "total": "10"},                 // duplicate number
		map[string]string{"party_name": cust, "document_number": "INV-" + sfx + "-2", "document_date": "not a date", "total": "10"},                 // bad date
		map[string]string{"party_name": cust, "document_number": "INV-" + sfx + "-3", "document_date": "2025-12-27", "total": "100", "paid": "200"}, // paid > total
		map[string]string{"party_name": supp, "document_number": "INV-" + sfx + "-4", "document_date": "2025-12-27", "total": "100"},                // a supplier is not a sale party
		map[string]string{"party_name": "Nobody " + sfx, "document_number": "INV-" + sfx + "-5", "document_date": "2025-12-27", "total": "100"},     // unknown
	)
	irep, err := svc.ImportHistoricalInvoices(ctx, fx.Principal, legacyimport.KindSale, inv, false)
	if err != nil {
		t.Fatal(err)
	}
	if irep.Committed != 1 || irep.Duplicates != 1 || irep.Errors != 4 {
		t.Fatalf("history import: %+v", irep.Results)
	}
	list, err := svc.ListHistoricalInvoices(ctx, fx.Principal, "SALE", sfx, 50)
	if err != nil || len(list) != 1 || list[0].Total != "1180.00" || list[0].Due != "1000.00" || list[0].Date != "2025-12-25" || !strings.HasPrefix(list[0].Number, "INV-") {
		t.Fatalf("register: %+v %v", list, err)
	}
	after, _ := accountingSvc.GetPartyLedger(ctx, fx.Principal, custID, time.Now())
	if len(after) != len(before) {
		t.Fatal("the history register must not post to the ledger")
	}
	// The same number is a different invoice for purchases.
	prep, err := svc.ImportHistoricalInvoices(ctx, fx.Principal, legacyimport.KindPurchase, rowsOf(
		map[string]string{"party_name": supp, "document_number": "INV-" + sfx + "-1", "document_date": "01-Jan-2026", "total": "500"}), false)
	if err != nil || prep.Committed != 1 {
		t.Fatalf("purchase history: %+v %v", prep, err)
	}
	if _, err := svc.ImportHistoricalInvoices(ctx, fx.Principal, "BOGUS", inv, true); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
}
