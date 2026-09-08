//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/modules/gstindia"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	inventoryapp "rechvix/internal/modules/inventory/app"
	purchasesapp "rechvix/internal/modules/purchases/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
	purchasespg "rechvix/internal/modules/purchases/pg"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxationpg "rechvix/internal/modules/taxation/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

// accountingSvc is nil here deliberately — see sales_test.go's identical
// note on newTestSalesServices; Stage 6's own tests wire a real one.
// catalogue/taxation/contacts/organisation are real, though — unlike
// accounting, purchases.Service requires them unconditionally (AddLine
// always resolves a product's HSN via catalogue).
func newTestPurchasesService(t *testing.T, inventorySvc *inventoryapp.Service) *purchasesapp.Service {
	t.Helper()
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)
	contactsSvc := contactsapp.NewService(
		sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool),
		checker, recorder,
	)
	gstRateRepo := gstindiapg.NewTaxRateRepo(sharedPool)
	gstEngine := gstindia.NewEngine(gstRateRepo, gstindiapg.NewStateRepo(sharedPool))
	taxationSvc := taxationapp.NewService(
		sharedPool, gstEngine, taxationpg.NewTaxDocumentRepo(sharedPool), taxationpg.NewTaxLineRepo(sharedPool), taxationpg.NewTaxComponentRepo(sharedPool),
	)
	return purchasesapp.NewService(
		sharedPool,
		purchasespg.NewDocumentRepo(sharedPool),
		purchasespg.NewDocumentLineRepo(sharedPool),
		inventorySvc,
		newTestCatalogueService(t),
		taxationSvc,
		contactsSvc,
		newTestOrgService(t),
		nil,
		checker,
		recorder,
	)
}

type purchasesFixture struct {
	inventoryFixture
	SupplierPartyID uuid.UUID
}

func setupPurchasesFixture(t *testing.T, ctx context.Context) purchasesFixture {
	t.Helper()
	fx := setupInventoryFixture(t, ctx)
	contactsSvc := contactsapp.NewService(
		sharedPool,
		contactspg.NewPartyRepo(sharedPool),
		contactspg.NewAddressRepo(sharedPool),
		contactspg.NewTaxRegistrationRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		audit.NewPGRecorder(sharedPool),
	)
	supplier, err := contactsSvc.CreateParty(ctx, fx.Principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartySupplier, LegalName: "Test Supplier " + uuid.NewString()[:8], CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateParty(supplier): %v", err)
	}
	return purchasesFixture{inventoryFixture: fx, SupplierPartyID: supplier.ID}
}

func TestPurchases_GoodsReceipt_FinalizePostsStockMovement(t *testing.T) {
	ctx := context.Background()
	inventorySvc := newTestInventoryService(t)
	purchasesSvc := newTestPurchasesService(t, inventorySvc)
	fx := setupPurchasesFixture(t, ctx)

	doc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
		DocumentType: purchasesdomain.DocGoodsReceipt, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if doc.Status != purchasesdomain.StatusDraft {
		t.Fatalf("new document status = %s, want DRAFT", doc.Status)
	}
	if doc.DocumentNumber == "" {
		t.Fatal("document number was not allocated")
	}

	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "50"), UnitPrice: mustDecimal(t, "20"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}

	finalized, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID)
	if err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}
	if finalized.Status != purchasesdomain.StatusFinalized {
		t.Fatalf("finalized status = %s, want FINALIZED", finalized.Status)
	}

	bal, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "50")) {
		t.Fatalf("QuantityOnHand after GRN finalize = %s, want 50", bal.QuantityOnHand)
	}
	if !bal.AverageCost.Equal(mustDecimal(t, "20")) {
		t.Fatalf("AverageCost after GRN finalize = %s, want 20", bal.AverageCost)
	}

	movements, err := inventorySvc.ListMovements(ctx, fx.Principal, fx.WarehouseID, fx.VariantID, 10)
	if err != nil {
		t.Fatalf("ListMovements: %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("movement count = %d, want 1", len(movements))
	}
	if movements[0].ReferenceType != "purchase_document" || movements[0].ReferenceID == nil || *movements[0].ReferenceID != doc.ID {
		t.Fatalf("movement reference = %q/%v, want purchase_document/%s", movements[0].ReferenceType, movements[0].ReferenceID, doc.ID)
	}
}

// TestPurchases_StandaloneInvoice_FinalizePostsStockMovement verifies the
// fix for a production-blocking gap: the shipped app's only
// purchase-creation UI (apps/web PurchasesPage) creates PURCHASE_INVOICE
// documents with no GRN step, so a standalone invoice (no
// ReferenceDocumentID) must itself put stock on the shelf — otherwise no
// UI-reachable path ever increases stock_balances and every subsequent
// sale fails with "insufficient stock" (see domain.StockAffecting).
func TestPurchases_StandaloneInvoice_FinalizePostsStockMovement(t *testing.T) {
	ctx := context.Background()
	inventorySvc := newTestInventoryService(t)
	purchasesSvc := newTestPurchasesService(t, inventorySvc)
	fx := setupPurchasesFixture(t, ctx)

	doc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
		DocumentType: purchasesdomain.DocPurchaseInvoice, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "50"), UnitPrice: mustDecimal(t, "20"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}

	bal, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "50")) {
		t.Fatalf("QuantityOnHand after standalone invoice finalize = %s, want 50", bal.QuantityOnHand)
	}
	if !bal.AverageCost.Equal(mustDecimal(t, "20")) {
		t.Fatalf("AverageCost after standalone invoice finalize = %s, want 20", bal.AverageCost)
	}
}

// TestPurchases_InvoiceReferencingGoodsReceipt_FinalizeDoesNotDoubleCountStock
// is the double-counting guard for a future GRN-then-invoice flow: once a
// GOODS_RECEIPT has already put stock on the shelf, an invoice that bills
// against it (ReferenceDocumentID set) must not post stock again.
func TestPurchases_InvoiceReferencingGoodsReceipt_FinalizeDoesNotDoubleCountStock(t *testing.T) {
	ctx := context.Background()
	inventorySvc := newTestInventoryService(t)
	purchasesSvc := newTestPurchasesService(t, inventorySvc)
	fx := setupPurchasesFixture(t, ctx)

	grn, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
		DocumentType: purchasesdomain.DocGoodsReceipt, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument(GRN): %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: grn.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "50"), UnitPrice: mustDecimal(t, "20"),
	}); err != nil {
		t.Fatalf("AddLine(GRN): %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, grn.ID); err != nil {
		t.Fatalf("FinalizeDocument(GRN): %v", err)
	}

	invoice, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
		DocumentType: purchasesdomain.DocPurchaseInvoice, CurrencyCode: "INR", ReferenceDocumentID: &grn.ID,
	})
	if err != nil {
		t.Fatalf("CreateDocument(invoice): %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: invoice.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "50"), UnitPrice: mustDecimal(t, "20"),
	}); err != nil {
		t.Fatalf("AddLine(invoice): %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, invoice.ID); err != nil {
		t.Fatalf("FinalizeDocument(invoice): %v", err)
	}

	bal, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "50")) {
		t.Fatalf("QuantityOnHand after GRN + referencing invoice = %s, want 50 (invoice must not double-count the GRN's receipt)", bal.QuantityOnHand)
	}
}

// TestPurchases_PurchaseOrder_FinalizeDoesNotPostStock verifies
// domain.StockAffecting's split: a PURCHASE_ORDER is a commitment, not a
// receipt, so finalizing one must not move any stock (only GOODS_RECEIPT
// and PURCHASE_RETURN do).
func TestPurchases_PurchaseOrder_FinalizeDoesNotPostStock(t *testing.T) {
	ctx := context.Background()
	inventorySvc := newTestInventoryService(t)
	purchasesSvc := newTestPurchasesService(t, inventorySvc)
	fx := setupPurchasesFixture(t, ctx)

	doc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
		DocumentType: purchasesdomain.DocPurchaseOrder, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "50"), UnitPrice: mustDecimal(t, "20"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err != nil {
		t.Fatalf("FinalizeDocument: %v", err)
	}

	bal, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.IsZero() {
		t.Fatalf("QuantityOnHand after PO finalize = %s, want 0 (a purchase order must not move stock)", bal.QuantityOnHand)
	}
}

func TestPurchases_FinalizeTwice_RejectsSecondCall(t *testing.T) {
	ctx := context.Background()
	inventorySvc := newTestInventoryService(t)
	purchasesSvc := newTestPurchasesService(t, inventorySvc)
	fx := setupPurchasesFixture(t, ctx)

	doc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
		DocumentType: purchasesdomain.DocGoodsReceipt, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := purchasesSvc.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
		DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "10"), UnitPrice: mustDecimal(t, "5"),
	}); err != nil {
		t.Fatalf("AddLine: %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err != nil {
		t.Fatalf("first FinalizeDocument: %v", err)
	}
	if _, err := purchasesSvc.FinalizeDocument(ctx, fx.Principal, doc.ID); err != purchasesdomain.ErrDocumentNotDraft {
		t.Fatalf("second FinalizeDocument = %v, want ErrDocumentNotDraft (brief §31 immutability)", err)
	}

	// And re-finalizing must not have posted a second stock movement.
	bal, err := inventorySvc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "10")) {
		t.Fatalf("QuantityOnHand after double-finalize attempt = %s, want 10 (unchanged by the rejected second call)", bal.QuantityOnHand)
	}
}

// TestPurchases_ConcurrentDocumentCreation_UniqueSequentialNumbers is
// Scenario I's building block for purchases: concurrently creating many
// documents of the same type must allocate a distinct, gap-free set of
// sequence numbers — no duplicate handed to two documents.
func TestPurchases_ConcurrentDocumentCreation_UniqueSequentialNumbers(t *testing.T) {
	ctx := context.Background()
	inventorySvc := newTestInventoryService(t)
	purchasesSvc := newTestPurchasesService(t, inventorySvc)
	fx := setupPurchasesFixture(t, ctx)

	const n = 12
	var wg sync.WaitGroup
	numbers := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			doc, err := purchasesSvc.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
				BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
				DocumentType: purchasesdomain.DocPurchaseOrder, CurrencyCode: "INR",
			})
			errs[i] = err
			if err == nil {
				numbers[i] = doc.DocumentNumber
			}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("CreateDocument %d failed: %v", i, err)
		}
		if seen[numbers[i]] {
			t.Fatalf("duplicate document number allocated: %q", numbers[i])
		}
		seen[numbers[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d unique document numbers, want %d", len(seen), n)
	}
}

func TestPurchases_RLS_BlocksCrossOrganisationDocumentRead(t *testing.T) {
	ctx := context.Background()
	inventorySvc := newTestInventoryService(t)
	purchasesSvc := newTestPurchasesService(t, inventorySvc)
	fxA := setupPurchasesFixture(t, ctx)
	fxB := setupPurchasesFixture(t, ctx)

	doc, err := purchasesSvc.CreateDocument(ctx, fxA.Principal, purchasesapp.CreateDocumentParams{
		BranchID: fxA.BranchID, WarehouseID: fxA.WarehouseID, SupplierPartyID: fxA.SupplierPartyID,
		DocumentType: purchasesdomain.DocPurchaseOrder, CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateDocument as A: %v", err)
	}

	if _, _, err := purchasesSvc.GetDocument(ctx, fxB.Principal, doc.ID); err != purchasesdomain.ErrNotFound {
		t.Fatalf("GetDocument as B for A's document = %v, want ErrNotFound", err)
	}

	if _, _, err := purchasesSvc.GetDocument(ctx, fxA.Principal, doc.ID); err != nil {
		t.Fatalf("GetDocument as A for its own document: %v", err)
	}
}
