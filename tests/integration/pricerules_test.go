//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"rechvix/internal/modules/pricerules"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingpg "rechvix/internal/modules/pricing/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func priceOf(t *testing.T, ctx context.Context, fx accountingFixture) (decimal.Decimal, bool) {
	t.Helper()
	var p decimal.Decimal
	var found bool
	_ = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		err := sharedPool.Q(ctx).QueryRow(ctx, `SELECT price_amount FROM price_list_items WHERE product_variant_id = $1 AND unit_id = $2`, fx.VariantID, fx.PCS).Scan(&p)
		found = err == nil
		return nil
	})
	return p, found
}

func TestPriceRules_PurchaseCostMovesSellingPrice(t *testing.T) {
	ctx := context.Background()
	_, purchasesSvc, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	pricing := pricingapp.NewService(sharedPool, pricingpg.NewPriceListRepo(sharedPool), pricingpg.NewPriceListItemRepo(sharedPool), checker, audit.NewPGRecorder(sharedPool))
	if _, err := pricing.EnsureDefaultPriceList(ctx, fx.Principal, "EUR"); err != nil {
		t.Fatal(err)
	}
	rules := pricerules.NewService(sharedPool, checker, audit.NewPGRecorder(sharedPool))
	purchasesSvc.WithPriceUpdater(rules)

	// Rule off: a purchase changes nothing.
	if _, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "10", "100"); err != nil {
		t.Fatal(err)
	}
	if _, ok := priceOf(t, ctx, fx); ok {
		t.Fatal("with the rule off no price may be created")
	}

	if err := rules.Save(ctx, fx.Principal, pricerules.Rule{Enabled: true, MarkupPercent: decimal.NewFromInt(25), Mode: pricerules.ModeRaiseOnly}); err != nil {
		t.Fatal(err)
	}
	if _, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "10", "100"); err != nil {
		t.Fatal(err)
	}
	if p, ok := priceOf(t, ctx, fx); !ok || !p.Equal(decimal.NewFromInt(125)) {
		t.Fatalf("100 cost + 25%% = 125, got %v (found %v)", p, ok)
	}
	// Raise-only: a cheaper purchase leaves the price alone; a dearer one raises it.
	if _, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "10", "80"); err != nil {
		t.Fatal(err)
	}
	if p, _ := priceOf(t, ctx, fx); !p.Equal(decimal.NewFromInt(125)) {
		t.Fatalf("a cheaper purchase must not lower the price in RAISE_ONLY mode, got %s", p)
	}
	if _, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "10", "120"); err != nil {
		t.Fatal(err)
	}
	if p, _ := priceOf(t, ctx, fx); !p.Equal(decimal.NewFromInt(150)) {
		t.Fatalf("120 cost + 25%% = 150, got %s", p)
	}
	// ALWAYS follows the cost down too.
	if err := rules.Save(ctx, fx.Principal, pricerules.Rule{Enabled: true, MarkupPercent: decimal.NewFromInt(25), Mode: pricerules.ModeAlways}); err != nil {
		t.Fatal(err)
	}
	if _, err := purchaseInvoice(t, ctx, purchasesSvc, fx, fx.CustomerID, "EUR", "10", "80"); err != nil {
		t.Fatal(err)
	}
	if p, _ := priceOf(t, ctx, fx); !p.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("ALWAYS: 80 cost + 25%% = 100, got %s", p)
	}
	if err := rules.Save(ctx, fx.Principal, pricerules.Rule{Enabled: true, MarkupPercent: decimal.NewFromInt(-5), Mode: pricerules.ModeAlways}); err == nil {
		t.Fatal("a negative markup must be refused")
	}
}
