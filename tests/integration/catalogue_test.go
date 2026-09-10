//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	cataloguedomain "rechvix/internal/modules/catalogue/domain"
	cataloguepg "rechvix/internal/modules/catalogue/pg"
	inventoryapp "rechvix/internal/modules/inventory/app"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingdomain "rechvix/internal/modules/pricing/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

func newTestCatalogueService(t *testing.T) *catalogueapp.Service {
	t.Helper()
	return catalogueapp.NewService(
		sharedPool,
		cataloguepg.NewUnitOfMeasureRepo(sharedPool),
		cataloguepg.NewUnitConversionRepo(sharedPool),
		cataloguepg.NewCategoryRepo(sharedPool),
		cataloguepg.NewBrandRepo(sharedPool),
		cataloguepg.NewProductRepo(sharedPool),
		cataloguepg.NewProductVariantRepo(sharedPool),
		cataloguepg.NewBarcodeRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		audit.NewPGRecorder(sharedPool),
	)
}

// bootstrapOwnerPrincipal provisions a fresh tenant via the real
// identity.Bootstrap flow (not a shortcut) and returns an authenticated
// owner Principal for it — the owner role holds every permission by
// bootstrap design, so tests using this can exercise a module's real
// RBAC-checked application layer, not a permission-bypassing fake.
func bootstrapOwnerPrincipal(t *testing.T, ctx context.Context) permissions.Principal {
	t.Helper()
	identitySvc, _ := newTestIdentityService(t)
	email := "catalogue-" + uuid.NewString()[:8] + "@example.com"
	password := "correct horse battery staple 42"
	boot := bootstrapTestTenant(t, ctx, identitySvc, email, password)
	return permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}
}

func TestCatalogue_UnitConversion_And_ProductVariant_CRUD(t *testing.T) {
	ctx := context.Background()
	svc := newTestCatalogueService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	box, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "BOX", Name: "Box"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure(BOX): %v", err)
	}
	pcs, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure(PCS): %v", err)
	}

	conv, err := svc.CreateUnitConversion(ctx, principal, catalogueapp.CreateUnitConversionParams{
		FromUnitID: box.ID, ToUnitID: pcs.ID, Factor: mustDecimal(t, "25"),
	})
	if err != nil {
		t.Fatalf("CreateUnitConversion: %v", err)
	}
	if !conv.Factor.Equal(mustDecimal(t, "25")) {
		t.Fatalf("stored factor = %s, want 25", conv.Factor)
	}

	product, err := svc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{
		BaseUOMID: pcs.ID, Name: "Integration Test Widget", HSNSACCode: "8471",
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if product.HSNSACCode != "8471" {
		t.Fatalf("HSNSACCode = %q, want 8471", product.HSNSACCode)
	}

	variant, err := svc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{
		ProductID: product.ID, SKUCode: "WIDGET-" + uuid.NewString()[:8], Attributes: map[string]any{"colour": "blue"},
	})
	if err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}
	if variant.Attributes["colour"] != "blue" {
		t.Fatalf("variant attributes did not round-trip through jsonb: %+v", variant.Attributes)
	}

	barcode, err := svc.AddBarcode(ctx, principal, catalogueapp.AddBarcodeParams{
		VariantID: variant.ID, UnitID: pcs.ID, Barcode: "890" + uuid.NewString()[:10],
	})
	if err != nil {
		t.Fatalf("AddBarcode: %v", err)
	}

	looked, err := svc.LookupBarcode(ctx, principal, barcode.Barcode)
	if err != nil {
		t.Fatalf("LookupBarcode: %v", err)
	}
	if looked.VariantID != variant.ID {
		t.Fatalf("LookupBarcode returned variant %s, want %s", looked.VariantID, variant.ID)
	}
}

func TestCatalogue_SearchByName(t *testing.T) {
	ctx := context.Background()
	svc := newTestCatalogueService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	pcs, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}

	uniqueName := "SearchableWidget" + uuid.NewString()[:8]
	if _, err := svc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{BaseUOMID: pcs.ID, Name: uniqueName}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	results, err := svc.SearchProducts(ctx, principal, uniqueName, 10)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	found := false
	for _, p := range results {
		if p.Name == uniqueName {
			found = true
		}
	}
	if !found {
		t.Fatalf("SearchProducts(%q) did not return the matching product; got %d results", uniqueName, len(results))
	}
}

// TestCatalogue_RLS_BlocksCrossOrganisationProductRead is the catalogue
// module's building block for Scenario G, exercised through the real
// application layer (not raw RunScoped like rls_test.go's generic check)
// — Organisation B's principal must not be able to read Organisation A's
// product even by its exact primary key.
func TestCatalogue_RLS_BlocksCrossOrganisationProductRead(t *testing.T) {
	ctx := context.Background()
	svc := newTestCatalogueService(t)
	principalA := bootstrapOwnerPrincipal(t, ctx)
	principalB := bootstrapOwnerPrincipal(t, ctx)

	pcs, err := svc.CreateUnitOfMeasure(ctx, principalA, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure as A: %v", err)
	}
	product, err := svc.CreateProduct(ctx, principalA, catalogueapp.CreateProductParams{BaseUOMID: pcs.ID, Name: "Org A Private Product"})
	if err != nil {
		t.Fatalf("CreateProduct as A: %v", err)
	}

	if _, err := svc.GetProduct(ctx, principalB, product.ID); !errors.Is(err, cataloguedomain.ErrNotFound) {
		t.Fatalf("GetProduct as B for A's product: got err=%v, want ErrNotFound", err)
	}

	// Sanity check: A can still read its own product (proves the failure
	// above is RLS, not a bug that blocks everyone).
	if _, err := svc.GetProduct(ctx, principalA, product.ID); err != nil {
		t.Fatalf("GetProduct as A for its own product: %v", err)
	}
}

// TestCatalogue_DeleteProductsIfUnused_HardDeletesWhenNoHistory covers the
// "delete button should actually remove the product" fix — a product with
// zero transaction history is hard-deleted (row gone, not just flipped to
// INACTIVE), and its price/barcode data is cleaned up alongside it via
// DeletePriceHookFunc, matching production's apps/server/main.go wiring.
func TestCatalogue_DeleteProductsIfUnused_HardDeletesWhenNoHistory(t *testing.T) {
	ctx := context.Background()
	pricingSvc := newTestPricingService(t)
	svc := newTestCatalogueService(t).WithDeletePriceHook(func(ctx context.Context, principal permissions.Principal, variantID uuid.UUID) error {
		return pricingSvc.DeletePricesForVariant(ctx, principal, variantID)
	})
	principal := bootstrapOwnerPrincipal(t, ctx)

	pcs, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}
	product, err := svc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{BaseUOMID: pcs.ID, Name: "Unused Widget"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	variant, err := svc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: product.ID, SKUCode: "UNUSED-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}
	barcode, err := svc.AddBarcode(ctx, principal, catalogueapp.AddBarcodeParams{VariantID: variant.ID, UnitID: pcs.ID, Barcode: "890" + uuid.NewString()[:10]})
	if err != nil {
		t.Fatalf("AddBarcode: %v", err)
	}

	priceList, err := pricingSvc.EnsureDefaultPriceList(ctx, principal, "INR")
	if err != nil {
		t.Fatalf("EnsureDefaultPriceList: %v", err)
	}
	price, err := money.Parse("99.00", "INR")
	if err != nil {
		t.Fatalf("money.Parse: %v", err)
	}
	if _, err := pricingSvc.SetPrice(ctx, principal, pricingapp.SetPriceParams{PriceListID: priceList.ID, ProductVariantID: variant.ID, UnitID: pcs.ID, Price: price}); err != nil {
		t.Fatalf("SetPrice: %v", err)
	}

	outcome, err := svc.DeleteProductsIfUnused(ctx, principal, []uuid.UUID{product.ID})
	if err != nil {
		t.Fatalf("DeleteProductsIfUnused: %v", err)
	}
	if len(outcome.HardDeleted) != 1 || outcome.HardDeleted[0] != product.ID {
		t.Fatalf("HardDeleted = %v, want [%s]", outcome.HardDeleted, product.ID)
	}
	if len(outcome.Deactivated) != 0 {
		t.Fatalf("Deactivated = %v, want none", outcome.Deactivated)
	}

	if _, err := svc.GetProduct(ctx, principal, product.ID); !errors.Is(err, cataloguedomain.ErrNotFound) {
		t.Fatalf("GetProduct after hard delete: got err=%v, want ErrNotFound", err)
	}
	if _, err := svc.LookupBarcode(ctx, principal, barcode.Barcode); !errors.Is(err, cataloguedomain.ErrNotFound) {
		t.Fatalf("LookupBarcode after hard delete: got err=%v, want ErrNotFound", err)
	}
	if _, err := pricingSvc.ResolvePrice(ctx, principal, priceList.ID, variant.ID, pcs.ID); !errors.Is(err, pricingdomain.ErrNotFound) {
		t.Fatalf("ResolvePrice after hard delete: got err=%v, want ErrNotFound (deletePriceHook should have cleaned it up)", err)
	}
}

// TestCatalogue_DeleteProductsIfUnused_DeactivatesWhenHasHistory covers the
// safety fallback: a product with real transaction history (here, an
// opening-stock movement) must never be hard-deleted — it falls back to
// today's deactivate (soft-delete) behavior instead, keeping past
// stock/sales/purchase records intact and referencable.
func TestCatalogue_DeleteProductsIfUnused_DeactivatesWhenHasHistory(t *testing.T) {
	ctx := context.Background()
	svc := newTestCatalogueService(t)
	invSvc := newTestInventoryService(t)
	identitySvc, _ := newTestIdentityService(t)
	email := "catalogue-hist-" + uuid.NewString()[:8] + "@example.com"
	boot := bootstrapTestTenant(t, ctx, identitySvc, email, "correct horse battery staple 42")
	principal := permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}

	pcs, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}
	product, err := svc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{BaseUOMID: pcs.ID, Name: "Sold Widget"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	variant, err := svc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: product.ID, SKUCode: "SOLD-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}

	if _, err := invSvc.RecordOpeningStock(ctx, principal, inventoryapp.RecordMovementParams{
		WarehouseID: boot.WarehouseID, ProductVariantID: variant.ID, UnitID: pcs.ID,
		Quantity: mustDecimal(t, "10"), UnitCost: decimalPtr(mustDecimal(t, "5")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}

	outcome, err := svc.DeleteProductsIfUnused(ctx, principal, []uuid.UUID{product.ID})
	if err != nil {
		t.Fatalf("DeleteProductsIfUnused: %v", err)
	}
	if len(outcome.Deactivated) != 1 || outcome.Deactivated[0] != product.ID {
		t.Fatalf("Deactivated = %v, want [%s]", outcome.Deactivated, product.ID)
	}
	if len(outcome.HardDeleted) != 0 {
		t.Fatalf("HardDeleted = %v, want none", outcome.HardDeleted)
	}

	got, err := svc.GetProduct(ctx, principal, product.ID)
	if err != nil {
		t.Fatalf("GetProduct after deactivate: %v", err)
	}
	if got.Status != cataloguedomain.StatusInactive {
		t.Fatalf("Status = %s, want INACTIVE", got.Status)
	}
}
