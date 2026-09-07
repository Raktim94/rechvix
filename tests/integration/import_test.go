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
	rows := []importer.Row{
		{Number: 1, Fields: map[string]string{"name": newName, "hsn_sac_code": "8471", "base_uom_code": "PCS"}}, // valid, new
		{Number: 2, Fields: map[string]string{"name": existingName, "base_uom_code": "PCS"}},                    // duplicate
		{Number: 3, Fields: map[string]string{"name": "", "base_uom_code": "PCS"}},                              // missing name
		{Number: 4, Fields: map[string]string{"name": "Bad Unit Widget", "base_uom_code": "NOSUCHUNIT"}},        // bad unit
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
	if dryReport.Valid != 1 || dryReport.Duplicates != 1 || dryReport.Errors != 2 {
		t.Fatalf("dry run counts = %+v, want Valid=1 Duplicates=1 Errors=2", dryReport)
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

	// Real run: same rows, dryRun=false.
	report, err := svc.ImportProducts(ctx, principal, rows, false)
	if err != nil {
		t.Fatalf("ImportProducts: %v", err)
	}
	if report.Committed != 1 || report.Duplicates != 1 || report.Errors != 2 {
		t.Fatalf("real run counts = %+v, want Committed=1 Duplicates=1 Errors=2", report)
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
