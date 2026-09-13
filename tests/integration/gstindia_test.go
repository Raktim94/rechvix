//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	gstindiaapp "rechvix/internal/modules/gstindia/app"
	gstindiadomain "rechvix/internal/modules/gstindia/domain"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

func newTestGSTIndiaService(t *testing.T) (*gstindiaapp.Service, *gstindiapg.TaxRateRepo) {
	t.Helper()
	rateRepo := gstindiapg.NewTaxRateRepo(sharedPool)
	svc := gstindiaapp.NewService(sharedPool, rateRepo, gstindiapg.NewStateRepo(sharedPool), nil, nil, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool))
	return svc, rateRepo
}

func TestGSTIndia_TaxRateMaster_CreateAndResolve(t *testing.T) {
	ctx := context.Background()
	svc, rateRepo := newTestGSTIndiaService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	created, err := svc.CreateRate(ctx, principal, gstindiaapp.CreateRateParams{
		HSNSACCode: "9999", Classification: gstindiadomain.ClassificationTaxable,
		GSTRate: mustDecimal(t, "18"), CessRate: mustDecimal(t, "0"),
		ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateRate: %v", err)
	}

	err = sharedPool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		resolved, err := rateRepo.Resolve(ctx, principal.OrganisationID, "IN", "9999", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
		if err != nil {
			return err
		}
		if resolved.ID != created.ID {
			t.Errorf("Resolve returned a different row than was created: got %s, want %s", resolved.ID, created.ID)
		}
		if !resolved.GSTRate.Equal(mustDecimal(t, "18")) {
			t.Errorf("resolved GSTRate = %s, want 18", resolved.GSTRate)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("resolving rate: %v", err)
	}
}

func TestGSTIndia_RLS_BlocksCrossOrganisationRateRead(t *testing.T) {
	ctx := context.Background()
	svc, rateRepo := newTestGSTIndiaService(t)
	principalA := bootstrapOwnerPrincipal(t, ctx)
	principalB := bootstrapOwnerPrincipal(t, ctx)

	_, err := svc.CreateRate(ctx, principalA, gstindiaapp.CreateRateParams{
		HSNSACCode: "8888", Classification: gstindiadomain.ClassificationTaxable,
		GSTRate: mustDecimal(t, "12"), CessRate: mustDecimal(t, "0"),
		ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateRate as org A: %v", err)
	}

	err = sharedPool.RunScoped(ctx, principalB.OrganisationID, func(ctx context.Context) error {
		_, resolveErr := rateRepo.Resolve(ctx, principalA.OrganisationID, "IN", "8888", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
		if resolveErr == nil {
			t.Fatal("RLS FAILED: org B's transaction resolved org A's tax_rate_master row")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("running scoped as org B: %v", err)
	}
}

func TestGSTIndia_StateCodes_AreGlobalReferenceData(t *testing.T) {
	ctx := context.Background()
	stateRepo := gstindiapg.NewStateRepo(sharedPool)
	principalA := bootstrapOwnerPrincipal(t, ctx)
	principalB := bootstrapOwnerPrincipal(t, ctx)

	// gst_state_codes has no organisation_id/RLS by design (migrations/0015) —
	// it must be readable identically regardless of which (or whether any)
	// tenant scope is set.
	for _, scope := range []struct {
		name string
		run  func(fn database.TxFunc) error
	}{
		{"org A scope", func(fn database.TxFunc) error { return sharedPool.RunScoped(ctx, principalA.OrganisationID, fn) }},
		{"org B scope", func(fn database.TxFunc) error { return sharedPool.RunScoped(ctx, principalB.OrganisationID, fn) }},
		{"no scope", func(fn database.TxFunc) error { return sharedPool.Run(ctx, fn) }},
	} {
		t.Run(scope.name, func(t *testing.T) {
			err := scope.run(func(ctx context.Context) error {
				s, err := stateRepo.GetByCode(ctx, "27")
				if err != nil {
					return err
				}
				if s.Name != "Maharashtra" {
					t.Errorf("gst_state_codes[27].Name = %q, want Maharashtra", s.Name)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("reading gst_state_codes under %s: %v", scope.name, err)
			}
		})
	}
}
