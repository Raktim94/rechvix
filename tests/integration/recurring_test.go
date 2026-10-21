//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/recurring"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func TestRecurring_CreatesDraftsAndFinalizedInvoicesOnSchedule(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	src := finalizedSale(t, ctx, salesSvc, fx, "1", "100")
	notif, _ := newNotifications(t, nil)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	rec := recurring.NewService(sharedPool, salesSvc, notif, checker, audit.NewPGRecorder(sharedPool), nil, func() string { return "" })

	today := time.Now().UTC().Truncate(24 * time.Hour)
	if _, err := rec.Create(ctx, fx.Principal, recurring.CreateParams{SourceDocumentID: src.ID, Frequency: recurring.Monthly, IntervalCount: 1, FirstRunDate: today.AddDate(0, 0, -1)}); err == nil {
		t.Fatal("a first run date in the past must be refused")
	}
	draftSched, err := rec.Create(ctx, fx.Principal, recurring.CreateParams{SourceDocumentID: src.ID, Frequency: recurring.Monthly, IntervalCount: 1, FirstRunDate: today})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	n, err := rec.RunOnce(ctx, fx.Principal.OrganisationID)
	if err != nil || n != 1 {
		t.Fatalf("first run must create one invoice: %d %v", n, err)
	}
	if again, _ := rec.RunOnce(ctx, fx.Principal.OrganisationID); again != 0 {
		t.Fatalf("a second run the same day must create nothing, created %d", again)
	}
	list, _ := rec.List(ctx, fx.Principal)
	if len(list) != 1 || list[0].RunCount != 1 || list[0].NextRunDate != recurring.Nth(today, recurring.Monthly, 1, 1).Format("2006-01-02") {
		t.Fatalf("schedule must advance a month: %+v", list)
	}
	var drafts int
	_ = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM sales_documents WHERE reference_document_id = $1 AND status = 'DRAFT'`, src.ID).Scan(&drafts)
	})
	if drafts != 1 {
		t.Fatalf("one draft clone expected, found %d", drafts)
	}

	// Missed periods are caught up (here: three monthly runs due), each as its own invoice, auto-finalized.
	if err := rec.SetActive(ctx, fx.Principal, draftSched.ID, false); err != nil {
		t.Fatal(err)
	}
	auto, err := rec.Create(ctx, fx.Principal, recurring.CreateParams{SourceDocumentID: src.ID, Frequency: recurring.Weekly, IntervalCount: 1, FirstRunDate: today, AutoFinalize: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE recurring_schedules SET anchor_date = current_date - 14, next_run_date = current_date - 14 WHERE id = $1`, auto.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	n, err = rec.RunOnce(ctx, fx.Principal.OrganisationID)
	if err != nil || n != 3 {
		t.Fatalf("three missed weekly runs expected: %d %v", n, err)
	}
	var finalized int
	_ = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM sales_documents WHERE reference_document_id = $1 AND status = 'FINALIZED'`, src.ID).Scan(&finalized)
	})
	if finalized != 3 {
		t.Fatalf("three finalized invoices expected, found %d", finalized)
	}
	if err := rec.Delete(ctx, fx.Principal, auto.ID); err != nil {
		t.Fatal(err)
	}
	_ = uuid.Nil
	_ = salesdomain.StatusDraft
}

func TestRecurring_OnlyFinalizedTaxInvoicesCanRepeat(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	d, err := salesSvc.CreateDocument(ctx, fx.Principal, newDraftParams(t, fx))
	if err != nil {
		t.Fatal(err)
	}
	notif, _ := newNotifications(t, nil)
	rec := recurring.NewService(sharedPool, salesSvc, notif, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool), nil, func() string { return "" })
	if _, err := rec.Create(ctx, fx.Principal, recurring.CreateParams{SourceDocumentID: d.ID, Frequency: recurring.Monthly, IntervalCount: 1, FirstRunDate: time.Now()}); err == nil {
		t.Fatal("a draft cannot be repeated")
	}
}

func newDraftParams(t *testing.T, fx accountingFixture) salesapp.CreateDocumentParams {
	t.Helper()
	return salesapp.CreateDocumentParams{LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, CurrencyCode: "EUR", BaseCurrencyCode: "EUR", ExchangeRate: mustDecimal(t, "1")}
}
