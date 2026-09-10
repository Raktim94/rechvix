//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingdomain "rechvix/internal/modules/pricing/domain"
	pricingpg "rechvix/internal/modules/pricing/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

func newTestPricingService(t *testing.T) *pricingapp.Service {
	t.Helper()
	return pricingapp.NewService(
		sharedPool,
		pricingpg.NewPriceListRepo(sharedPool),
		pricingpg.NewPriceListItemRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		audit.NewPGRecorder(sharedPool),
	)
}

// seedVariant creates a minimal unit + product + variant in the given
// tenant, for tests that only need a valid product_variant_id/unit_id to
// satisfy price_list_items' foreign keys — the catalogue data itself
// isn't what's under test here.
func seedVariant(t *testing.T, ctx context.Context, principal permissions.Principal) (unitID, variantID uuid.UUID) {
	t.Helper()
	catSvc := newTestCatalogueService(t)
	unit, err := catSvc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("seedVariant: CreateUnitOfMeasure: %v", err)
	}
	product, err := catSvc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{BaseUOMID: unit.ID, Name: "Priced Widget"})
	if err != nil {
		t.Fatalf("seedVariant: CreateProduct: %v", err)
	}
	variant, err := catSvc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: product.ID, SKUCode: "PRICED-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("seedVariant: CreateVariant: %v", err)
	}
	return unit.ID, variant.ID
}

// seedSecondVariant creates a second product+variant in the same tenant
// against an already-existing unit — for tests needing two distinct
// variants without seedVariant's fixed "PCS" unit code colliding.
func seedSecondVariant(t *testing.T, ctx context.Context, principal permissions.Principal, unitID uuid.UUID) (variantID uuid.UUID) {
	t.Helper()
	catSvc := newTestCatalogueService(t)
	product, err := catSvc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{BaseUOMID: unitID, Name: "Second Priced Widget"})
	if err != nil {
		t.Fatalf("seedSecondVariant: CreateProduct: %v", err)
	}
	variant, err := catSvc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: product.ID, SKUCode: "PRICED2-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("seedSecondVariant: CreateVariant: %v", err)
	}
	return variant.ID
}

func TestPricing_PriceList_SetAndResolve(t *testing.T) {
	ctx := context.Background()
	svc := newTestPricingService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)
	unitID, variantID := seedVariant(t, ctx, principal)

	priceList, err := svc.CreatePriceList(ctx, principal, pricingapp.CreatePriceListParams{Name: "Retail", CurrencyCode: "INR", IsDefault: true})
	if err != nil {
		t.Fatalf("CreatePriceList: %v", err)
	}

	price, err := money.Parse("76.271186", "INR")
	if err != nil {
		t.Fatalf("money.Parse: %v", err)
	}
	if _, err := svc.SetPrice(ctx, principal, pricingapp.SetPriceParams{PriceListID: priceList.ID, ProductVariantID: variantID, UnitID: unitID, Price: price}); err != nil {
		t.Fatalf("SetPrice: %v", err)
	}

	resolved, err := svc.ResolvePrice(ctx, principal, priceList.ID, variantID, unitID)
	if err != nil {
		t.Fatalf("ResolvePrice: %v", err)
	}
	if !resolved.Price.Decimal().Equal(price.Decimal()) {
		t.Fatalf("resolved price = %s, want %s (full precision must survive NUMERIC round trip)", resolved.Price, price)
	}
	if resolved.Price.Currency() != "INR" {
		t.Fatalf("resolved currency = %s, want INR", resolved.Price.Currency())
	}

	// Revising the price (same price list + variant + unit) must replace
	// the existing row, not accumulate a second one — pricingpg.Upsert's
	// whole reason to exist (migrations/0010_pricing.up.sql UNIQUE
	// constraint).
	revised, err := money.Parse("80.00", "INR")
	if err != nil {
		t.Fatalf("money.Parse: %v", err)
	}
	if _, err := svc.SetPrice(ctx, principal, pricingapp.SetPriceParams{PriceListID: priceList.ID, ProductVariantID: variantID, UnitID: unitID, Price: revised}); err != nil {
		t.Fatalf("SetPrice (revision): %v", err)
	}
	items, err := svc.ListPrices(ctx, principal, priceList.ID)
	if err != nil {
		t.Fatalf("ListPrices: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one price_list_item after revising the same variant+unit, got %d", len(items))
	}
	if !items[0].Price.Decimal().Equal(revised.Decimal()) {
		t.Fatalf("after revision, price = %s, want %s", items[0].Price, revised)
	}
}

func TestPricing_ResolvePrice_NotFoundWhenUnset(t *testing.T) {
	ctx := context.Background()
	svc := newTestPricingService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)
	unitID, variantID := seedVariant(t, ctx, principal)

	priceList, err := svc.CreatePriceList(ctx, principal, pricingapp.CreatePriceListParams{Name: "Empty List", CurrencyCode: "INR"})
	if err != nil {
		t.Fatalf("CreatePriceList: %v", err)
	}

	if _, err := svc.ResolvePrice(ctx, principal, priceList.ID, variantID, unitID); !errors.Is(err, pricingdomain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound resolving an unset price, got %v", err)
	}
}

// TestPricing_RLS_BlocksCrossOrganisationPriceListRead is the pricing
// module's Scenario G building block.
func TestPricing_RLS_BlocksCrossOrganisationPriceListRead(t *testing.T) {
	ctx := context.Background()
	svc := newTestPricingService(t)
	principalA := bootstrapOwnerPrincipal(t, ctx)
	principalB := bootstrapOwnerPrincipal(t, ctx)

	priceList, err := svc.CreatePriceList(ctx, principalA, pricingapp.CreatePriceListParams{Name: "Org A Private List", CurrencyCode: "INR"})
	if err != nil {
		t.Fatalf("CreatePriceList as A: %v", err)
	}

	if _, err := svc.GetPriceList(ctx, principalB, priceList.ID); !errors.Is(err, pricingdomain.ErrNotFound) {
		t.Fatalf("GetPriceList as B for A's price list: got err=%v, want ErrNotFound", err)
	}
	if _, err := svc.GetPriceList(ctx, principalA, priceList.ID); err != nil {
		t.Fatalf("GetPriceList as A for its own price list: %v", err)
	}
}

// TestPricing_EnsureDefaultPriceList_CreatesOnceThenIdempotent covers the
// auto-provisioning fix for "price not displaying" — a fresh organisation
// has no price list until something needs one (CSV import, the New
// Product form's Price field). The first call must create exactly one
// "Default" list; every later call must return that same list, never a
// second one.
func TestPricing_EnsureDefaultPriceList_CreatesOnceThenIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := newTestPricingService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	first, err := svc.EnsureDefaultPriceList(ctx, principal, "INR")
	if err != nil {
		t.Fatalf("EnsureDefaultPriceList (first call): %v", err)
	}
	if first.Name != "Default" || !first.IsDefault {
		t.Fatalf("first call created %+v, want Name=Default IsDefault=true", first)
	}

	second, err := svc.EnsureDefaultPriceList(ctx, principal, "INR")
	if err != nil {
		t.Fatalf("EnsureDefaultPriceList (second call): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second call returned a different price list (%s), want the same one (%s) — must not create a duplicate", second.ID, first.ID)
	}

	lists, err := svc.ListPriceLists(ctx, principal)
	if err != nil {
		t.Fatalf("ListPriceLists: %v", err)
	}
	if len(lists) != 1 {
		t.Fatalf("expected exactly one price list after two EnsureDefaultPriceList calls, got %d", len(lists))
	}
}

// TestPricing_EnsureDefaultPriceList_RespectsExistingDefault covers an
// organisation that already created its own price list(s) directly via
// CreatePriceList (pre-dating this method, or a deliberate multi-list
// setup) — EnsureDefaultPriceList must never create a second list once
// any exist, and must prefer the one marked IsDefault over just the first.
func TestPricing_EnsureDefaultPriceList_RespectsExistingDefault(t *testing.T) {
	ctx := context.Background()
	svc := newTestPricingService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	if _, err := svc.CreatePriceList(ctx, principal, pricingapp.CreatePriceListParams{Name: "Wholesale", CurrencyCode: "INR"}); err != nil {
		t.Fatalf("CreatePriceList(Wholesale): %v", err)
	}
	retail, err := svc.CreatePriceList(ctx, principal, pricingapp.CreatePriceListParams{Name: "Retail", CurrencyCode: "INR", IsDefault: true})
	if err != nil {
		t.Fatalf("CreatePriceList(Retail, IsDefault): %v", err)
	}

	result, err := svc.EnsureDefaultPriceList(ctx, principal, "INR")
	if err != nil {
		t.Fatalf("EnsureDefaultPriceList: %v", err)
	}
	if result.ID != retail.ID {
		t.Fatalf("EnsureDefaultPriceList returned %s (%s), want the IsDefault list %s (Retail)", result.ID, result.Name, retail.ID)
	}

	lists, err := svc.ListPriceLists(ctx, principal)
	if err != nil {
		t.Fatalf("ListPriceLists: %v", err)
	}
	if len(lists) != 2 {
		t.Fatalf("expected the pre-existing two lists to remain untouched, got %d", len(lists))
	}
}

// TestPricing_DeletePricesForVariant covers catalogue's DeletePriceHookFunc
// counterpart directly (see catalogue_test.go's
// TestCatalogue_DeleteProductsIfUnused_HardDeletesWhenNoHistory for the
// end-to-end hook-wired version) — every price entry for a variant, across
// every price list, is removed, and other variants' prices are untouched.
func TestPricing_DeletePricesForVariant(t *testing.T) {
	ctx := context.Background()
	svc := newTestPricingService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)
	unitID, variantID := seedVariant(t, ctx, principal)
	otherVariantID := seedSecondVariant(t, ctx, principal, unitID)

	listA, err := svc.CreatePriceList(ctx, principal, pricingapp.CreatePriceListParams{Name: "List A", CurrencyCode: "INR"})
	if err != nil {
		t.Fatalf("CreatePriceList(List A): %v", err)
	}
	listB, err := svc.CreatePriceList(ctx, principal, pricingapp.CreatePriceListParams{Name: "List B", CurrencyCode: "INR"})
	if err != nil {
		t.Fatalf("CreatePriceList(List B): %v", err)
	}
	price, err := money.Parse("50.00", "INR")
	if err != nil {
		t.Fatalf("money.Parse: %v", err)
	}
	for _, pl := range []uuid.UUID{listA.ID, listB.ID} {
		if _, err := svc.SetPrice(ctx, principal, pricingapp.SetPriceParams{PriceListID: pl, ProductVariantID: variantID, UnitID: unitID, Price: price}); err != nil {
			t.Fatalf("SetPrice(variantID, list %s): %v", pl, err)
		}
	}
	if _, err := svc.SetPrice(ctx, principal, pricingapp.SetPriceParams{PriceListID: listA.ID, ProductVariantID: otherVariantID, UnitID: unitID, Price: price}); err != nil {
		t.Fatalf("SetPrice(otherVariantID): %v", err)
	}

	if err := svc.DeletePricesForVariant(ctx, principal, variantID); err != nil {
		t.Fatalf("DeletePricesForVariant: %v", err)
	}

	if _, err := svc.ResolvePrice(ctx, principal, listA.ID, variantID, unitID); !errors.Is(err, pricingdomain.ErrNotFound) {
		t.Fatalf("ResolvePrice(listA, variantID) after delete: got err=%v, want ErrNotFound", err)
	}
	if _, err := svc.ResolvePrice(ctx, principal, listB.ID, variantID, unitID); !errors.Is(err, pricingdomain.ErrNotFound) {
		t.Fatalf("ResolvePrice(listB, variantID) after delete: got err=%v, want ErrNotFound", err)
	}
	if _, err := svc.ResolvePrice(ctx, principal, listA.ID, otherVariantID, unitID); err != nil {
		t.Fatalf("ResolvePrice(listA, otherVariantID) after deleting a different variant's prices: %v (should be untouched)", err)
	}
}
