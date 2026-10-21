//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/modules/crm"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func newCRM(t *testing.T) *crm.Service {
	t.Helper()
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	rec := audit.NewPGRecorder(sharedPool)
	contacts := contactsapp.NewService(sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool), checker, rec)
	return crm.NewService(sharedPool, contacts, checker, rec)
}

func TestCRM_LeadToCustomerThroughThePipeline(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	svc := newCRM(t)

	stages, err := svc.Stages(ctx, fx.Principal)
	if err != nil || len(stages) != 6 || stages[0].Name != "New" || stages[4].Kind != "WON" {
		t.Fatalf("default stages: %+v %v", stages, err)
	}
	if _, err := svc.Create(ctx, fx.Principal, crm.Input{Title: "", Currency: "EUR", CompanyName: "x"}); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("a missing title is invalid: %v", err)
	}
	if _, err := svc.Create(ctx, fx.Principal, crm.Input{Title: "Lead", Currency: "EUR"}); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("a lead needs someone to talk to: %v", err)
	}
	opp, err := svc.Create(ctx, fx.Principal, crm.Input{Title: "Warehouse racking", CompanyName: "Acme Logistics SL", ContactName: "Marta", ContactEmail: "marta@acme.example", Value: decimal.NewFromInt(12000), Currency: "eur"})
	if err != nil {
		t.Fatal(err)
	}
	if opp.StageKind != "OPEN" || opp.Currency != "EUR" || opp.PartyID != nil {
		t.Fatalf("a new lead starts open without a customer: %+v", opp)
	}
	due := time.Now().Add(48 * time.Hour)
	task, err := svc.AddActivity(ctx, fx.Principal, opp.ID, "task", "Send the quote", "", &due)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddActivity(ctx, fx.Principal, opp.ID, "call", "Intro call", "Interested in 40 bays", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddActivity(ctx, fx.Principal, opp.ID, "fax", "x", "", nil); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("unknown activity kind: %v", err)
	}
	tasks, _ := svc.OpenTasks(ctx, fx.Principal)
	if len(tasks) != 1 || tasks[0].Title != "Warehouse racking" {
		t.Fatalf("one open task: %+v", tasks)
	}
	if err := svc.CompleteActivity(ctx, fx.Principal, task.ID, true); err != nil {
		t.Fatal(err)
	}
	if tasks, _ := svc.OpenTasks(ctx, fx.Principal); len(tasks) != 0 {
		t.Fatal("a completed task is no longer open")
	}

	cust, err := svc.MakeCustomer(ctx, fx.Principal, opp.ID)
	if err != nil || cust.PartyID == nil || cust.CustomerName != "Acme Logistics SL" {
		t.Fatalf("lead becomes a customer: %+v %v", cust, err)
	}
	again, _ := svc.MakeCustomer(ctx, fx.Principal, opp.ID)
	if again.PartyID == nil || *again.PartyID != *cust.PartyID {
		t.Fatal("making a customer twice must not create a second one")
	}

	var won crm.Stage
	for _, st := range stages {
		if st.Kind == "WON" {
			won = st
		}
	}
	moved, err := svc.Move(ctx, fx.Principal, opp.ID, won.ID)
	if err != nil || moved.ClosedAt == nil || moved.StageKind != "WON" {
		t.Fatalf("moving to Won closes it: %+v %v", moved, err)
	}
	reopened, _ := svc.Move(ctx, fx.Principal, opp.ID, stages[1].ID)
	if reopened.ClosedAt != nil {
		t.Fatal("moving back to an open stage reopens it")
	}
	acts, _ := svc.Activities(ctx, fx.Principal, opp.ID)
	if len(acts) != 2 {
		t.Fatalf("two activities recorded: %d", len(acts))
	}

	// Another organisation sees nothing of this pipeline.
	other := spainFixture(t, ctx, acct)
	if list, _ := svc.List(ctx, other.Principal); len(list) != 0 {
		t.Fatal("tenant isolation: other organisation sees opportunities")
	}
	if _, err := svc.Get(ctx, other.Principal, opp.ID); !errors.Is(err, crm.ErrNotFound) {
		t.Fatalf("another organisation cannot read it: %v", err)
	}
	if err := svc.Delete(ctx, fx.Principal, opp.ID); err != nil {
		t.Fatal(err)
	}
}
