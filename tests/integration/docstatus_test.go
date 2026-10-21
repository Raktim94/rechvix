//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"rechvix/internal/modules/docstatus"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func TestDocStatus_DefineAssignAndGuard(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	doc := finalizedSale(t, ctx, salesSvc, fx, "1", "50")
	svc := docstatus.NewService(sharedPool, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool))

	st, err := svc.Create(ctx, fx.Principal, "  Waiting for client ", "amber", 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "Waiting for client" {
		t.Fatalf("name must be trimmed: %q", st.Name)
	}
	if _, err := svc.Create(ctx, fx.Principal, "Waiting for client", "red", 2); !errors.Is(err, docstatus.ErrDuplicate) {
		t.Fatalf("a duplicate name must be refused, got %v", err)
	}
	if _, err := svc.Create(ctx, fx.Principal, "x", "pink", 2); !errors.Is(err, docstatus.ErrInvalid) {
		t.Fatalf("an unknown colour must be refused, got %v", err)
	}
	// A finalized (accounting-locked) document can still carry a workflow label.
	if err := svc.Assign(ctx, fx.Principal, doc.ID, &st.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	got, err := svc.For(ctx, fx.Principal, []uuid.UUID{doc.ID})
	if err != nil || len(got) != 1 || got[0].Name != "Waiting for client" || got[0].Color != "amber" {
		t.Fatalf("assignment not stored: %+v %v", got, err)
	}
	if err := svc.Delete(ctx, fx.Principal, st.ID); !errors.Is(err, docstatus.ErrInvalid) {
		t.Fatalf("a status in use must not be deleted: %v", err)
	}
	if err := svc.Update(ctx, fx.Principal, st.ID, "Waiting for client", "amber", 1, false); err != nil {
		t.Fatal(err)
	}
	other, _ := svc.Create(ctx, fx.Principal, "Shipped", "green", 2)
	_ = other
	if err := svc.Assign(ctx, fx.Principal, doc.ID, &st.ID); !errors.Is(err, docstatus.ErrInvalid) {
		t.Fatalf("a switched-off status cannot be newly assigned: %v", err)
	}
	if err := svc.Assign(ctx, fx.Principal, doc.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.For(ctx, fx.Principal, []uuid.UUID{doc.ID}); len(got) != 0 {
		t.Fatal("clearing the status must remove the assignment")
	}
	if err := svc.Delete(ctx, fx.Principal, st.ID); err != nil {
		t.Fatalf("an unused status can be deleted: %v", err)
	}
	// Another organisation cannot see or use this organisation's statuses.
	otherFx := spainFixture(t, ctx, acct)
	rows, err := svc.List(ctx, otherFx.Principal)
	if err != nil || len(rows) != 0 {
		t.Fatalf("tenant isolation: %+v %v", rows, err)
	}
	if err := svc.Assign(ctx, otherFx.Principal, doc.ID, &other.ID); err == nil {
		t.Fatal("another organisation must not label this document")
	}
}
