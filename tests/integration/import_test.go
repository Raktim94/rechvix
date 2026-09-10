//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	cataloguedomain "rechvix/internal/modules/catalogue/domain"
	cataloguepg "rechvix/internal/modules/catalogue/pg"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

func newTestCatalogueServiceForImport(t *testing.T) *catalogueapp.Service {
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

func TestCatalogue_ImportProducts_ValidatesDedupesAndCommits(t *testing.T) {
	ctx := context.Background()
	svc := newTestCatalogueServiceForImport(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	if _, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"}); err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}
	existingName := "PreExisting Widget " + uuid.NewString()[:8]
	if _, err := svc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{
		Name: existingName, BaseUOMID: mustGetUnitID(t, ctx, svc, principal, "PCS"),
	}); err != nil {
		t.Fatalf("CreateProduct (pre-existing): %v", err)
	}

	newName := "Imported Widget " + uuid.NewString()[:8]
	newUnitName := "New Unit Widget " + uuid.NewString()[:8]
	newUnitCode := "NEWUNIT" + uuid.NewString()[:8]
	rows := []importer.Row{
		{Number: 1, Fields: map[string]string{"name": newName, "hsn_sac_code": "8471", "base_uom_code": "PCS"}}, // valid, new
		{Number: 2, Fields: map[string]string{"name": existingName, "base_uom_code": "PCS"}},                    // duplicate
		{Number: 3, Fields: map[string]string{"name": "", "base_uom_code": "PCS"}},                              // missing name
		{Number: 4, Fields: map[string]string{"name": "No Unit Widget", "base_uom_code": ""}},                   // missing base_uom_code
		// base_uom_code naming no unit this organisation has yet — must
		// still succeed, auto-creating that unit, not error like a bad
		// category_id/brand_id foreign key would.
		{Number: 5, Fields: map[string]string{"name": newUnitName, "base_uom_code": newUnitCode}},
	}

	// Dry run first: nothing committed, but the report must already show
	// the correct outcome per row.
	dryReport, err := svc.ImportProducts(ctx, principal, rows, true)
	if err != nil {
		t.Fatalf("ImportProducts(dryRun): %v", err)
	}
	if dryReport.Committed != 0 {
		t.Fatalf("dry run Committed = %d, want 0", dryReport.Committed)
	}
	if dryReport.Valid != 2 || dryReport.Duplicates != 1 || dryReport.Errors != 2 {
		t.Fatalf("dry run counts = %+v, want Valid=2 Duplicates=1 Errors=2", dryReport)
	}

	list, err := svc.ListProducts(ctx, principal)
	if err != nil {
		t.Fatalf("ListProducts after dry run: %v", err)
	}
	for _, p := range list {
		if p.Name == newName {
			t.Fatalf("dry run must not have committed %q, but it exists", newName)
		}
	}
	units, err := svc.ListUnitsOfMeasure(ctx, principal)
	if err != nil {
		t.Fatalf("ListUnitsOfMeasure after dry run: %v", err)
	}
	for _, u := range units {
		if u.Code == newUnitCode {
			t.Fatalf("dry run must not have created unit %q, but it exists", newUnitCode)
		}
	}

	// Real run: same rows, dryRun=false.
	report, err := svc.ImportProducts(ctx, principal, rows, false)
	if err != nil {
		t.Fatalf("ImportProducts: %v", err)
	}
	if report.Committed != 2 || report.Duplicates != 1 || report.Errors != 2 {
		t.Fatalf("real run counts = %+v, want Committed=2 Duplicates=1 Errors=2", report)
	}

	units, err = svc.ListUnitsOfMeasure(ctx, principal)
	if err != nil {
		t.Fatalf("ListUnitsOfMeasure: %v", err)
	}
	var newUnit *cataloguedomain.UnitOfMeasure
	for _, u := range units {
		if u.Code == newUnitCode {
			newUnit = u
		}
	}
	if newUnit == nil {
		t.Fatalf("unit %q was not auto-created by the import", newUnitCode)
	}
	if newUnit.Name != newUnitCode {
		t.Fatalf("auto-created unit Name = %q, want %q (the code itself, since a CSV row names nothing else)", newUnit.Name, newUnitCode)
	}

	list, err = svc.ListProducts(ctx, principal)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	var imported *cataloguedomain.Product
	for _, p := range list {
		if p.Name == newName {
			imported = p
		}
	}
	if imported == nil {
		t.Fatalf("imported product %q not found after commit", newName)
	}

	// A product with zero variants is invisible everywhere else in the
	// app (billing lookup, inventory, purchases all key off
	// ProductVariantID, never ProductID) — this is the regression this
	// test now guards against, found via a real end-to-end smoke test
	// after wiring the first frontend UI onto this endpoint (docs/TODO.md
	// Stage 14): imported products used to commit with zero variants.
	variants, err := svc.ListVariantsByProduct(ctx, principal, imported.ID)
	if err != nil {
		t.Fatalf("ListVariantsByProduct: %v", err)
	}
	if len(variants) != 1 {
		t.Fatalf("imported product has %d variants, want exactly 1", len(variants))
	}
	if variants[0].SKUCode == "" {
		t.Fatal("imported product's auto-created variant has an empty SKU code")
	}
}

// TestCatalogue_ImportProducts_GeneratesUniqueSKUsOnCollision proves the
// auto-generated-SKU path (used whenever a row has no sku_code column,
// or leaves it blank) resolves a collision instead of failing the whole
// row — both against a SKU that already exists in the organisation and
// against another row in the SAME import batch that would generate the
// identical slug (two products named identically except for
// case/punctuation, a realistic spreadsheet scenario).
func TestCatalogue_ImportProducts_GeneratesUniqueSKUsOnCollision(t *testing.T) {
	ctx := context.Background()
	svc := newTestCatalogueServiceForImport(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	if _, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"}); err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}

	unique := uuid.NewString()[:8]
	baseName := "Widget Alpha " + unique // slugifies to e.g. WIDGET-ALPHA-<unique>

	// Pre-existing product whose variant SKU already occupies the slug
	// the FIRST import row below would otherwise generate.
	preexisting, err := svc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{
		Name: "Some Other Product " + unique, BaseUOMID: mustGetUnitID(t, ctx, svc, principal, "PCS"),
	})
	if err != nil {
		t.Fatalf("CreateProduct (pre-existing): %v", err)
	}
	occupiedSKU := slugifyForTest(baseName)
	if _, err := svc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: preexisting.ID, SKUCode: occupiedSKU}); err != nil {
		t.Fatalf("CreateVariant (occupying the slug): %v", err)
	}

	rows := []importer.Row{
		{Number: 1, Fields: map[string]string{"name": baseName, "base_uom_code": "PCS"}},        // collides with occupiedSKU
		{Number: 2, Fields: map[string]string{"name": baseName + "!!", "base_uom_code": "PCS"}}, // different product name, SAME slug as row 1's fallback
	}
	report, err := svc.ImportProducts(ctx, principal, rows, false)
	if err != nil {
		t.Fatalf("ImportProducts: %v", err)
	}
	if report.Committed != 2 {
		t.Fatalf("report = %+v, want Committed=2 (both rows resolve to distinct SKUs despite the collision)", report)
	}

	list, err := svc.ListProducts(ctx, principal)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	skus := make(map[string]int)
	for _, p := range list {
		if p.Name != baseName && p.Name != baseName+"!!" {
			continue
		}
		variants, err := svc.ListVariantsByProduct(ctx, principal, p.ID)
		if err != nil {
			t.Fatalf("ListVariantsByProduct(%s): %v", p.Name, err)
		}
		if len(variants) != 1 {
			t.Fatalf("product %q has %d variants, want 1", p.Name, len(variants))
		}
		skus[variants[0].SKUCode]++
	}
	if len(skus) != 2 {
		t.Fatalf("expected 2 distinct SKUs across the two imported products, got %v", skus)
	}
	for sku, count := range skus {
		if count != 1 {
			t.Fatalf("SKU %q used by %d variants, want exactly 1 (uniqueness violated)", sku, count)
		}
		if sku == occupiedSKU {
			t.Fatalf("an imported product ended up with the pre-occupied SKU %q — collision not actually avoided", occupiedSKU)
		}
	}
}

// slugifyForTest mirrors catalogue/app.slugifySKU exactly (unexported,
// so this test can't call it directly) — used only to compute what SKU
// a given name WOULD generate, so the test can deliberately occupy it
// first.
func slugifyForTest(name string) string {
	var out []byte
	lastWasDash := false
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			out = append(out, byte(r))
			lastWasDash = false
		case !lastWasDash:
			out = append(out, '-')
			lastWasDash = true
		}
	}
	s := strings.Trim(string(out), "-")
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}

// TestCatalogue_ImportProducts_CategoryBrandBarcode proves the
// category_name/brand_name/barcode columns: an existing category is
// reused by case-insensitive name match rather than duplicated, a new
// brand named identically on two rows in the SAME file is created only
// once and shared, and a barcode collision — whether against an
// existing product or another row in this same file — fails just that
// row instead of aborting the whole import.
func TestCatalogue_ImportProducts_CategoryBrandBarcode(t *testing.T) {
	ctx := context.Background()
	svc := newTestCatalogueServiceForImport(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	if _, err := svc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"}); err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}
	unique := uuid.NewString()[:8]

	existingCategory, err := svc.CreateCategory(ctx, principal, "Snacks "+unique, nil)
	if err != nil {
		t.Fatalf("CreateCategory (pre-existing): %v", err)
	}

	preexisting, err := svc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{
		Name: "Some Other Product " + unique, BaseUOMID: mustGetUnitID(t, ctx, svc, principal, "PCS"),
	})
	if err != nil {
		t.Fatalf("CreateProduct (pre-existing): %v", err)
	}
	preexistingVariant, err := svc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: preexisting.ID, SKUCode: "PRE-EXIST-" + unique})
	if err != nil {
		t.Fatalf("CreateVariant (pre-existing): %v", err)
	}
	takenBarcode := "TAKEN-" + unique
	if _, err := svc.AddBarcode(ctx, principal, catalogueapp.AddBarcodeParams{
		VariantID: preexistingVariant.ID, UnitID: mustGetUnitID(t, ctx, svc, principal, "PCS"), Barcode: takenBarcode,
	}); err != nil {
		t.Fatalf("AddBarcode (pre-existing): %v", err)
	}

	newBrand := "Crunchy Co " + unique
	nameA := "Chips A " + unique
	nameB := "Chips B " + unique
	nameC := "Chips C " + unique
	barcodeA := "NEW-A-" + unique
	barcodeB := "NEW-B-" + unique

	rows := []importer.Row{
		// reuses the pre-existing category by name (mixed case), creates
		// the brand fresh.
		{Number: 1, Fields: map[string]string{"name": nameA, "base_uom_code": "PCS", "category_name": strings.ToUpper("Snacks " + unique), "brand_name": newBrand, "barcode": barcodeA}},
		// same new brand name as row 1 — must resolve to the SAME brand,
		// not a second one.
		{Number: 2, Fields: map[string]string{"name": nameB, "base_uom_code": "PCS", "brand_name": newBrand, "barcode": barcodeB}},
		// barcode collides with another row in THIS file (row 1's).
		{Number: 3, Fields: map[string]string{"name": nameC, "base_uom_code": "PCS", "barcode": barcodeA}},
		// barcode collides with the pre-existing product's barcode.
		{Number: 4, Fields: map[string]string{"name": "Chips D " + unique, "base_uom_code": "PCS", "barcode": takenBarcode}},
	}

	report, err := svc.ImportProducts(ctx, principal, rows, false)
	if err != nil {
		t.Fatalf("ImportProducts: %v", err)
	}
	if report.Committed != 2 || report.Errors != 2 {
		t.Fatalf("report = %+v, want Committed=2 Errors=2", report)
	}

	list, err := svc.ListProducts(ctx, principal)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	var productA, productB *cataloguedomain.Product
	for _, p := range list {
		switch p.Name {
		case nameA:
			productA = p
		case nameB:
			productB = p
		}
	}
	if productA == nil || productB == nil {
		t.Fatalf("expected both %q and %q to have been imported", nameA, nameB)
	}
	if productA.CategoryID == nil || *productA.CategoryID != existingCategory.ID {
		t.Fatalf("product %q CategoryID = %v, want the pre-existing category %s (reused by name, not duplicated)", nameA, productA.CategoryID, existingCategory.ID)
	}
	if productA.BrandID == nil || productB.BrandID == nil || *productA.BrandID != *productB.BrandID {
		t.Fatalf("products %q and %q have different BrandID (%v, %v) — the shared brand_name should resolve to one brand", nameA, nameB, productA.BrandID, productB.BrandID)
	}

	brands, err := svc.ListBrands(ctx, principal)
	if err != nil {
		t.Fatalf("ListBrands: %v", err)
	}
	brandCount := 0
	for _, br := range brands {
		if br.Name == newBrand {
			brandCount++
		}
	}
	if brandCount != 1 {
		t.Fatalf("brand %q exists %d times, want exactly 1 (two rows sharing the same brand_name must not create it twice)", newBrand, brandCount)
	}

	if _, err := svc.LookupBarcode(ctx, principal, barcodeA); err != nil {
		t.Fatalf("LookupBarcode(%q): %v", barcodeA, err)
	}
	if _, err := svc.LookupBarcode(ctx, principal, barcodeB); err != nil {
		t.Fatalf("LookupBarcode(%q): %v", barcodeB, err)
	}
}

func TestContacts_ImportParties_ValidatesDedupesAndCommits(t *testing.T) {
	ctx := context.Background()
	svc := contactsapp.NewService(
		sharedPool,
		contactspg.NewPartyRepo(sharedPool),
		contactspg.NewAddressRepo(sharedPool),
		contactspg.NewTaxRegistrationRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		audit.NewPGRecorder(sharedPool),
	)
	principal := bootstrapOwnerPrincipal(t, ctx)

	existingName := "PreExisting Traders " + uuid.NewString()[:8]
	if _, err := svc.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyCustomer, LegalName: existingName, CurrencyCode: "INR",
	}); err != nil {
		t.Fatalf("CreateParty (pre-existing): %v", err)
	}

	newName := "Imported Traders " + uuid.NewString()[:8]
	rows := []importer.Row{
		{Number: 1, Fields: map[string]string{"party_type": "SUPPLIER", "legal_name": newName, "currency_code": "INR"}},      // valid, new
		{Number: 2, Fields: map[string]string{"party_type": "CUSTOMER", "legal_name": existingName, "currency_code": "INR"}}, // duplicate (same type+name)
		{Number: 3, Fields: map[string]string{"party_type": "NOT_A_TYPE", "legal_name": "Whatever", "currency_code": "INR"}}, // bad party_type
		{Number: 4, Fields: map[string]string{"party_type": "CUSTOMER", "legal_name": "", "currency_code": "INR"}},           // missing name
	}

	report, err := svc.ImportParties(ctx, principal, rows, false)
	if err != nil {
		t.Fatalf("ImportParties: %v", err)
	}
	if report.Committed != 1 || report.Duplicates != 1 || report.Errors != 2 {
		t.Fatalf("counts = %+v, want Committed=1 Duplicates=1 Errors=2", report)
	}

	list, err := svc.ListParties(ctx, principal)
	if err != nil {
		t.Fatalf("ListParties: %v", err)
	}
	found := false
	for _, p := range list {
		if p.LegalName == newName {
			found = true
		}
	}
	if !found {
		t.Fatalf("imported party %q not found after commit", newName)
	}
}

func mustGetUnitID(t *testing.T, ctx context.Context, svc *catalogueapp.Service, principal permissions.Principal, code string) uuid.UUID {
	t.Helper()
	units, err := svc.ListUnitsOfMeasure(ctx, principal)
	if err != nil {
		t.Fatalf("ListUnitsOfMeasure: %v", err)
	}
	for _, u := range units {
		if u.Code == code {
			return u.ID
		}
	}
	t.Fatalf("unit %q not found", code)
	return uuid.UUID{}
}
