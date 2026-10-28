//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestCRM_AssignmentRejectsForeignReferences(t *testing.T) {
	ctx := context.Background()
	owner := setupStaffPrincipal(t, ctx)
	other := setupStaffPrincipal(t, ctx)
	svc := newCRM(t)
	in := crm.Input{Title: "Service renewal", CompanyName: "Workshop", Currency: "GBP", OwnerUserID: &owner.UserID}
	opp, err := svc.Create(ctx, owner, in)
	if err != nil {
		t.Fatal(err)
	}
	owners, err := svc.Owners(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range owners {
		if user.ID == other.UserID {
			t.Fatal("foreign owner disclosed")
		}
	}
	if len(owners) != 1 || owners[0].ID != owner.UserID {
		t.Fatalf("owner list: %+v", owners)
	}
	if err := svc.Assign(ctx, owner, opp.ID, &other.UserID); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("foreign assignment: %v", err)
	}
	if err := svc.Assign(ctx, other, opp.ID, &other.UserID); !errors.Is(err, crm.ErrNotFound) {
		t.Fatalf("foreign opportunity: %v", err)
	}
	in.OwnerUserID = &other.UserID
	if _, err := svc.Create(ctx, owner, in); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("foreign create: %v", err)
	}
	if _, err := svc.Update(ctx, owner, opp.ID, in); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("foreign update: %v", err)
	}
	in.OwnerUserID = &owner.UserID
	foreignParty := uuid.New()
	if err := sharedPool.RunScoped(ctx, other.OrganisationID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `INSERT INTO parties (id, organisation_id, party_type, legal_name, currency_code) VALUES ($1,$2,'CUSTOMER','Foreign workshop','GBP')`, foreignParty, other.OrganisationID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	in.PartyID = &foreignParty
	if _, err := svc.Create(ctx, owner, in); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("foreign customer create: %v", err)
	}
	if _, err := svc.Update(ctx, owner, opp.ID, in); !errors.Is(err, crm.ErrInvalid) {
		t.Fatalf("foreign customer update: %v", err)
	}
	if err := svc.Assign(ctx, owner, opp.ID, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := svc.Get(ctx, owner, opp.ID)
	if err != nil || saved.OwnerUserID != nil {
		t.Fatalf("unassign: %+v %v", saved, err)
	}
	if err := svc.Assign(ctx, owner, opp.ID, &owner.UserID); err != nil {
		t.Fatal(err)
	}
}

func TestCRM_ImportLeads_ValidatesDedupesAndPlacesStages(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	svc := newCRM(t)
	sfx := uuid.NewString()[:6]

	if _, err := svc.Create(ctx, fx.Principal, crm.Input{Title: "Existing " + sfx, ContactName: "Old", ContactPhone: "98765 43210", Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	file := rowsOf(
		map[string]string{"company_name": "Acme " + sfx, "contact_phone": "+91 99999 11111", "value": "1500.50", "stage": "Proposal", "expected_close": "31/12/2099"}, // ok, placed in Proposal
		map[string]string{"contact_name": "Same phone", "contact_phone": "09876543210"},                                                                               // duplicate of the existing lead by phone
		map[string]string{"company_name": "Acme " + sfx, "contact_phone": "+91 99999 11111"},                                                                          // duplicate of row 1 inside the file
		map[string]string{"company_name": "Bad value " + sfx, "value": "lots"},                                                                                        // error
		map[string]string{"company_name": "Bad stage " + sfx, "stage": "Nowhere"},                                                                                     // error
		map[string]string{"value": "5"}, // error: nobody to talk to
		map[string]string{"contact_name": "Plain " + sfx, "contact_email": "plain@example.org"}, // ok, defaults
	)
	dry, err := svc.Import(ctx, fx.Principal, file, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Valid != 2 || dry.Duplicates != 2 || dry.Errors != 3 || dry.Committed != 0 {
		t.Fatalf("dry run = %+v", dry)
	}
	before, _ := svc.List(ctx, fx.Principal)
	rep, err := svc.Import(ctx, fx.Principal, file, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Committed != 2 || rep.Duplicates != 2 || rep.Errors != 3 {
		t.Fatalf("import = %+v", rep)
	}
	after, _ := svc.List(ctx, fx.Principal)
	if len(after)-len(before) != 2 {
		t.Fatalf("expected two new leads, got %d", len(after)-len(before))
	}
	var acme *crm.Opportunity
	for i := range after {
		if after[i].CompanyName == "Acme "+sfx {
			acme = &after[i]
		}
	}
	if acme == nil || acme.Title != "Acme "+sfx || acme.Value.String() != "1500.5" || acme.ExpectedClose == nil {
		t.Fatalf("imported lead = %+v", acme)
	}
	again, _ := svc.Import(ctx, fx.Principal, file, false)
	if again.Committed != 0 || again.Duplicates != 4 {
		t.Fatalf("re-running the file must add nothing: %+v", again)
	}
}
