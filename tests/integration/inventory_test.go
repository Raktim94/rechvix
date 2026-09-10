//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	cataloguepg "rechvix/internal/modules/catalogue/pg"
	identityapp "rechvix/internal/modules/identity/app"
	inventoryapp "rechvix/internal/modules/inventory/app"
	inventorydomain "rechvix/internal/modules/inventory/domain"
	inventorypg "rechvix/internal/modules/inventory/pg"
	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func decimalPtr(d decimal.Decimal) *decimal.Decimal { return &d }

func newTestInventoryService(t *testing.T) *inventoryapp.Service {
	t.Helper()
	svc := inventoryapp.NewService(
		sharedPool,
		inventorypg.NewStockMovementRepo(sharedPool),
		inventorypg.NewStockBalanceRepo(sharedPool),
		inventorypg.NewStockReservationRepo(sharedPool),
		inventorypg.NewStockBatchRepo(sharedPool),
		inventorypg.NewStockCostLotRepo(sharedPool),
		inventorypg.NewSerialNumberRepo(sharedPool),
		inventorypg.NewStockPolicyRepo(sharedPool),
		inventorypg.NewStockTransferRepo(sharedPool),
		inventorypg.NewStockAdjustmentRepo(sharedPool),
		cataloguepg.NewProductVariantRepo(sharedPool),
		cataloguepg.NewProductRepo(sharedPool),
		cataloguepg.NewUnitConversionRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		audit.NewPGRecorder(sharedPool),
	)
	// Same resolver apps/server/main.go wires in — every principal-gated
	// write (RecordOpeningStock/RecordAdjustment/RecordTransfer/Reserve/
	// SetStockPolicy) fails closed without it, see
	// inventoryapp.WarehouseLegalEntityFunc's own doc comment.
	orgSvc := newTestOrgService(t)
	svc.WithWarehouseLegalEntityResolver(func(ctx context.Context, orgID, warehouseID uuid.UUID) (uuid.UUID, error) {
		var legalEntityID uuid.UUID
		err := sharedPool.RunScoped(ctx, orgID, func(ctx context.Context) error {
			warehouse, err := orgSvc.GetWarehouseForOtherModule(ctx, orgID, warehouseID)
			if err != nil {
				return err
			}
			branch, err := orgSvc.GetBranchForOtherModule(ctx, orgID, warehouse.BranchID)
			if err != nil {
				return err
			}
			legalEntityID = branch.LegalEntityID
			return nil
		})
		return legalEntityID, err
	})
	return svc
}

// inventoryFixture is what a movement/transfer/reservation test needs:
// a fresh tenant, one product variant stocked in PCS, and a BOX->PCS
// unit conversion.
type inventoryFixture struct {
	Principal   permissions.Principal
	BranchID    uuid.UUID
	WarehouseID uuid.UUID
	VariantID   uuid.UUID
	PCS         uuid.UUID
	BOX         uuid.UUID
}

func setupInventoryFixture(t *testing.T, ctx context.Context) inventoryFixture {
	t.Helper()
	identitySvc, _ := newTestIdentityService(t)
	catalogueSvc := catalogueapp.NewService(
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

	email := "inventory-" + uuid.NewString()[:8] + "@example.com"
	boot := bootstrapTestTenant(t, ctx, identitySvc, email, "correct horse battery staple 42")
	principal := permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}

	pcs, err := catalogueSvc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure(PCS): %v", err)
	}
	box, err := catalogueSvc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "BOX", Name: "Box"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure(BOX): %v", err)
	}
	if _, err := catalogueSvc.CreateUnitConversion(ctx, principal, catalogueapp.CreateUnitConversionParams{
		FromUnitID: box.ID, ToUnitID: pcs.ID, Factor: mustDecimal(t, "25"),
	}); err != nil {
		t.Fatalf("CreateUnitConversion: %v", err)
	}
	product, err := catalogueSvc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{BaseUOMID: pcs.ID, Name: "Inventory Test Widget " + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	variant, err := catalogueSvc.CreateVariant(ctx, principal, catalogueapp.CreateVariantParams{ProductID: product.ID, SKUCode: "INV-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("CreateVariant: %v", err)
	}

	return inventoryFixture{Principal: principal, BranchID: boot.BranchID, WarehouseID: boot.WarehouseID, VariantID: variant.ID, PCS: pcs.ID, BOX: box.ID}
}

func TestInventory_OpeningStock_And_Balance(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx)

	mv, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "100"), UnitCost: decimalPtr(mustDecimal(t, "10")),
	})
	if err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}
	if mv.MovementType != inventorydomain.MovementOpening {
		t.Fatalf("movement type = %s, want OPENING", mv.MovementType)
	}

	bal, err := svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "100")) {
		t.Fatalf("QuantityOnHand = %s, want 100", bal.QuantityOnHand)
	}
	if !bal.AverageCost.Equal(mustDecimal(t, "10")) {
		t.Fatalf("AverageCost = %s, want 10", bal.AverageCost)
	}
}

// TestInventory_UnitConversionAwareReceipt is the regression test for a
// real bug caught during development: unit_cost is entered per the
// TRANSACTED unit (e.g. cost per BOX), but stock_balances.average_cost is
// per the product's BASE unit (PCS) — feeding the BOX-denominated cost
// straight into the weighted-average formula without normalizing by the
// conversion factor would inflate the average cost by 25x (the BOX->PCS
// ratio). Receives 1 BOX (=25 PCS per the fixture's conversion) at
// ₹2500/BOX and asserts the resulting average cost is ₹100/PCS, not
// ₹2500/PCS.
func TestInventory_UnitConversionAwareReceipt(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx)

	mv, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.BOX,
		Quantity: mustDecimal(t, "1"), UnitCost: decimalPtr(mustDecimal(t, "2500")),
	})
	if err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}
	if !mv.BaseQuantity.Equal(mustDecimal(t, "25")) {
		t.Fatalf("BaseQuantity = %s, want 25 (1 BOX * 25 PCS/BOX)", mv.BaseQuantity)
	}

	bal, err := svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "25")) {
		t.Fatalf("QuantityOnHand = %s, want 25", bal.QuantityOnHand)
	}
	want := mustDecimal(t, "100") // 2500 per BOX / 25 PCS per BOX = 100 per PCS
	if !bal.AverageCost.Equal(want) {
		t.Fatalf("AverageCost = %s, want %s (2500/BOX normalized to per-PCS) — unit-conversion cost normalization regressed", bal.AverageCost, want)
	}
}

func TestInventory_Adjustment_InsufficientStockRejected(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx)

	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "5"), UnitCost: decimalPtr(mustDecimal(t, "1")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}

	_, _, err := svc.RecordAdjustment(ctx, fx.Principal, inventoryapp.RecordAdjustmentParams{
		WarehouseID: fx.WarehouseID, Reason: "damaged in transit",
		Lines: []inventoryapp.AdjustmentLineParams{
			{ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "10"), MovementType: inventorydomain.MovementDamage},
		},
	})
	if !errors.Is(err, inventorydomain.ErrInsufficientStock) {
		t.Fatalf("RecordAdjustment(qty=10, on-hand=5) = %v, want ErrInsufficientStock", err)
	}

	bal, err := svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "5")) {
		t.Fatalf("QuantityOnHand after rejected adjustment = %s, want unchanged 5", bal.QuantityOnHand)
	}
}

// TestInventory_CostLots_SamePriceAccumulates_DifferentPriceOpensNewLot
// is the regression test for the OCR purchase-intake feature's core
// requirement: a distributor delivering the same product at the same
// price as before should just top up the existing cost lot, but a
// delivery at a different price must open a distinct one so a later
// sale's margin still reflects what was actually paid for the specific
// units sold. Also exercises ConsumeFIFO (via an outward MovementDamage
// adjustment) draining the oldest lot first.
func TestInventory_CostLots_SamePriceAccumulates_DifferentPriceOpensNewLot(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx)
	costLots := inventorypg.NewStockCostLotRepo(sharedPool)

	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "10"), UnitCost: decimalPtr(mustDecimal(t, "50")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock #1 (10 @ 50): %v", err)
	}
	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "5"), UnitCost: decimalPtr(mustDecimal(t, "50")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock #2 (5 @ 50, same price): %v", err)
	}
	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "8"), UnitCost: decimalPtr(mustDecimal(t, "60")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock #3 (8 @ 60, different price): %v", err)
	}

	var lots []*inventorydomain.StockCostLot
	if err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		var err error
		lots, err = costLots.ListRemaining(ctx, fx.Principal.OrganisationID, fx.WarehouseID, fx.VariantID)
		return err
	}); err != nil {
		t.Fatalf("ListRemaining: %v", err)
	}
	if len(lots) != 2 {
		t.Fatalf("lot count = %d, want 2 (one per distinct price)", len(lots))
	}
	if !lots[0].UnitCost.Equal(mustDecimal(t, "50")) || !lots[0].QuantityRemaining.Equal(mustDecimal(t, "15")) {
		t.Fatalf("oldest lot = cost %s qty %s, want cost 50 qty 15 (10+5 merged)", lots[0].UnitCost, lots[0].QuantityRemaining)
	}
	if !lots[1].UnitCost.Equal(mustDecimal(t, "60")) || !lots[1].QuantityRemaining.Equal(mustDecimal(t, "8")) {
		t.Fatalf("newest lot = cost %s qty %s, want cost 60 qty 8 (separate lot)", lots[1].UnitCost, lots[1].QuantityRemaining)
	}

	// Drain 10 units — should come entirely from the older (cost-50) lot,
	// leaving 5 there and the cost-60 lot untouched.
	if _, _, err := svc.RecordAdjustment(ctx, fx.Principal, inventoryapp.RecordAdjustmentParams{
		WarehouseID: fx.WarehouseID, Reason: "rls/costing test drawdown",
		Lines: []inventoryapp.AdjustmentLineParams{
			{ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "10"), MovementType: inventorydomain.MovementDamage},
		},
	}); err != nil {
		t.Fatalf("RecordAdjustment(damage 10): %v", err)
	}

	if err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		var err error
		lots, err = costLots.ListRemaining(ctx, fx.Principal.OrganisationID, fx.WarehouseID, fx.VariantID)
		return err
	}); err != nil {
		t.Fatalf("ListRemaining after drawdown: %v", err)
	}
	if len(lots) != 2 {
		t.Fatalf("lot count after drawdown = %d, want 2 (older lot still has 5 remaining)", len(lots))
	}
	if !lots[0].QuantityRemaining.Equal(mustDecimal(t, "5")) {
		t.Fatalf("oldest lot remaining after drawdown = %s, want 5 (15-10)", lots[0].QuantityRemaining)
	}
	if !lots[1].QuantityRemaining.Equal(mustDecimal(t, "8")) {
		t.Fatalf("newest lot remaining after drawdown = %s, want unchanged 8", lots[1].QuantityRemaining)
	}
}

func TestInventory_Transfer_MovesStockBetweenWarehouses(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	orgSvc := newTestOrgService(t)
	fx := setupInventoryFixture(t, ctx)

	warehouseB, err := orgSvc.CreateWarehouse(ctx, fx.Principal, orgapp.CreateWarehouseParams{
		BranchID: fx.BranchID, Code: "WH-B-" + uuid.NewString()[:8], Name: "Warehouse B",
	})
	if err != nil {
		t.Fatalf("CreateWarehouse: %v", err)
	}

	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "40"), UnitCost: decimalPtr(mustDecimal(t, "5")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}

	if _, err := svc.RecordTransfer(ctx, fx.Principal, inventoryapp.RecordTransferParams{
		FromWarehouseID: fx.WarehouseID, ToWarehouseID: warehouseB.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "15"),
	}); err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}

	balA, err := svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance(A): %v", err)
	}
	balB, err := svc.GetBalance(ctx, fx.Principal, warehouseB.ID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance(B): %v", err)
	}
	if !balA.QuantityOnHand.Equal(mustDecimal(t, "25")) {
		t.Fatalf("warehouse A on-hand = %s, want 25 (40-15)", balA.QuantityOnHand)
	}
	if !balB.QuantityOnHand.Equal(mustDecimal(t, "15")) {
		t.Fatalf("warehouse B on-hand = %s, want 15", balB.QuantityOnHand)
	}
}

func TestInventory_Reservation_ReducesAvailable(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx)

	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "20"), UnitCost: decimalPtr(mustDecimal(t, "1")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}

	newRefID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Reserve(ctx, fx.Principal, inventoryapp.ReserveParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, Quantity: mustDecimal(t, "12"),
		ReferenceType: "test", ReferenceID: newRefID,
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	bal, err := svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.Available().Equal(mustDecimal(t, "8")) {
		t.Fatalf("Available = %s, want 8 (20-12)", bal.Available())
	}

	if err := svc.ReleaseReservation(ctx, fx.Principal, res.ID, fx.WarehouseID, fx.VariantID); err != nil {
		t.Fatalf("ReleaseReservation: %v", err)
	}
	bal, err = svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance after release: %v", err)
	}
	if !bal.Available().Equal(mustDecimal(t, "20")) {
		t.Fatalf("Available after release = %s, want 20", bal.Available())
	}
}

// TestInventory_ConcurrentOversell_OnlyOneWins is Scenario D's core
// assertion: several concurrent adjustments all trying to take the last
// unit of stock must not more-than-one succeed — exactly one must post,
// the rest must see ErrInsufficientStock, and the final balance must be
// non-negative and consistent (never the result of two racing writes
// both applying on top of the same stale read).
func TestInventory_ConcurrentOversell_OnlyOneWins(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx)

	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "1"), UnitCost: decimalPtr(mustDecimal(t, "1")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := svc.RecordAdjustment(ctx, fx.Principal, inventoryapp.RecordAdjustmentParams{
				WarehouseID: fx.WarehouseID, Reason: "concurrent sale simulation",
				Lines: []inventoryapp.AdjustmentLineParams{
					{ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "1"), MovementType: inventorydomain.MovementAdjustmentOut},
				},
			})
			results[i] = err
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, inventorydomain.ErrInsufficientStock) {
			t.Fatalf("unexpected error from concurrent adjustment: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1 (only one of %d concurrent takers of the last unit should win)", successes, attempts)
	}

	bal, err := svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if !bal.QuantityOnHand.Equal(mustDecimal(t, "0")) {
		t.Fatalf("final QuantityOnHand = %s, want exactly 0 (not negative, not still 1)", bal.QuantityOnHand)
	}
}

// TestInventory_ConcurrentTransfers_NoLostOrDuplicatedStock runs several
// concurrent 1-unit transfers out of a warehouse stocked with exactly
// enough for all of them, and asserts the total that lands in
// destinations equals what left the source — no transfer silently lost a
// unit or duplicated one under concurrency.
func TestInventory_ConcurrentTransfers_NoLostOrDuplicatedStock(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	orgSvc := newTestOrgService(t)
	fx := setupInventoryFixture(t, ctx)

	const n = 6
	if _, err := svc.RecordOpeningStock(ctx, fx.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "6"), UnitCost: decimalPtr(mustDecimal(t, "1")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock: %v", err)
	}

	destWarehouses := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		w, err := orgSvc.CreateWarehouse(ctx, fx.Principal, orgapp.CreateWarehouseParams{
			BranchID: fx.BranchID, Code: "WH-C" + uuid.NewString()[:6], Name: "Concurrent Dest",
		})
		if err != nil {
			t.Fatalf("CreateWarehouse: %v", err)
		}
		destWarehouses[i] = w.ID
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.RecordTransfer(ctx, fx.Principal, inventoryapp.RecordTransferParams{
				FromWarehouseID: fx.WarehouseID, ToWarehouseID: destWarehouses[i], ProductVariantID: fx.VariantID, UnitID: fx.PCS,
				Quantity: mustDecimal(t, "1"),
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("transfer %d failed: %v", i, err)
		}
	}

	balA, err := svc.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatalf("GetBalance(A): %v", err)
	}
	if !balA.QuantityOnHand.Equal(mustDecimal(t, "0")) {
		t.Fatalf("source warehouse on-hand = %s, want exactly 0 (6 - 6 transfers of 1)", balA.QuantityOnHand)
	}
	total := mustDecimal(t, "0")
	for _, w := range destWarehouses {
		bal, err := svc.GetBalance(ctx, fx.Principal, w, fx.VariantID)
		if err != nil {
			t.Fatalf("GetBalance(dest): %v", err)
		}
		total = total.Add(bal.QuantityOnHand)
	}
	if !total.Equal(mustDecimal(t, "6")) {
		t.Fatalf("total destination stock = %s, want exactly 6 — a concurrent transfer lost or duplicated stock", total)
	}
}

func TestInventory_RLS_BlocksCrossOrganisationBalanceAndMovementReads(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fxA := setupInventoryFixture(t, ctx)
	fxB := setupInventoryFixture(t, ctx)

	if _, err := svc.RecordOpeningStock(ctx, fxA.Principal, inventoryapp.RecordMovementParams{
		WarehouseID: fxA.WarehouseID, ProductVariantID: fxA.VariantID, UnitID: fxA.PCS,
		Quantity: mustDecimal(t, "10"), UnitCost: decimalPtr(mustDecimal(t, "1")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock as A: %v", err)
	}

	// B has permission (its own inventory.view grant) but must not be
	// able to read A's balance/movements even by A's exact IDs — RLS,
	// not just "B has no data of its own yet".
	balAsB, err := svc.GetBalance(ctx, fxB.Principal, fxA.WarehouseID, fxA.VariantID)
	if err != nil {
		t.Fatalf("GetBalance as B for A's warehouse/variant: %v", err)
	}
	if !balAsB.QuantityOnHand.IsZero() {
		t.Fatalf("RLS FAILED: org B read org A's stock balance (%s), want zero/invisible", balAsB.QuantityOnHand)
	}

	movementsAsB, err := svc.ListMovements(ctx, fxB.Principal, fxA.WarehouseID, fxA.VariantID, 50)
	if err != nil {
		t.Fatalf("ListMovements as B: %v", err)
	}
	if len(movementsAsB) != 0 {
		t.Fatalf("RLS FAILED: org B saw %d of org A's stock movements, want 0", len(movementsAsB))
	}

	// Sanity: A can still see its own data.
	balAsA, err := svc.GetBalance(ctx, fxA.Principal, fxA.WarehouseID, fxA.VariantID)
	if err != nil || !balAsA.QuantityOnHand.Equal(mustDecimal(t, "10")) {
		t.Fatalf("GetBalance as A for its own stock: bal=%v err=%v, want 10/nil", balAsA, err)
	}
}

// TestInventory_CompanyScopedAccess covers the write-action gap this
// session closed: RecordOpeningStock/RecordAdjustment previously stayed
// unscoped (Require(..., Scope{})) even after ListDocuments/company
// filtering landed, meaning a company-restricted member could view stock
// but never adjust it, even in their own granted company — see
// inventory/app.Service.manage/adjustPerm's own doc comments for the
// fix (resolving the target warehouse's company via the
// WarehouseLegalEntityFunc hook apps/server's composition root wires
// in, same as newTestInventoryService above does for these tests).
func TestInventory_CompanyScopedAccess(t *testing.T) {
	ctx := context.Background()
	svc := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx) // company A, via bootstrap
	orgSvc := newTestOrgService(t)
	identitySvc, _ := newTestIdentityService(t)

	var legalEntityA uuid.UUID
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		branchA, err := orgSvc.GetBranchForOtherModule(ctx, fx.Principal.OrganisationID, fx.BranchID)
		if err != nil {
			return err
		}
		legalEntityA = branchA.LegalEntityID
		return nil
	})
	if err != nil {
		t.Fatalf("GetBranchForOtherModule (company A): %v", err)
	}

	unique := uuid.NewString()[:8]
	companyB, err := orgSvc.CreateLegalEntity(ctx, fx.Principal, orgapp.CreateLegalEntityParams{
		LegalName: "Inventory Company B " + unique, CountryCode: "IN", BaseCurrencyCode: "INR",
		GSTIN: "27EEEEE0000E1Z5", GSTStateCode: "27",
	})
	if err != nil {
		t.Fatalf("CreateLegalEntity (company B): %v", err)
	}
	branchB, err := orgSvc.CreateBranch(ctx, fx.Principal, orgapp.CreateBranchParams{
		LegalEntityID: companyB.ID, Code: "IVB-" + unique, Name: "Company B Branch",
	})
	if err != nil {
		t.Fatalf("CreateBranch (company B): %v", err)
	}
	warehouseB, err := orgSvc.CreateWarehouse(ctx, fx.Principal, orgapp.CreateWarehouseParams{
		BranchID: branchB.ID, Code: "IVWB-" + unique, Name: "Company B Warehouse",
	})
	if err != nil {
		t.Fatalf("CreateWarehouse (company B): %v", err)
	}

	memberID, err := identitySvc.CreateTeamMember(ctx, fx.Principal, identityapp.CreateTeamMemberParams{
		FullName: "Inventory Restricted Member", Email: "inv-restricted-" + unique + "@example.com", Password: "another very long password 99",
		LegalEntityIDs: []uuid.UUID{legalEntityA}, // company A only
	})
	if err != nil {
		t.Fatalf("CreateTeamMember: %v", err)
	}
	restricted := permissions.Principal{UserID: memberID, OrganisationID: fx.Principal.OrganisationID}

	// Opening stock in the member's OWN company's warehouse must
	// succeed — this is exactly the "restricted but still functional in
	// their own company" gap that was previously broken.
	if _, err := svc.RecordOpeningStock(ctx, restricted, inventoryapp.RecordMovementParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "20"), UnitCost: decimalPtr(mustDecimal(t, "10")),
	}); err != nil {
		t.Fatalf("RecordOpeningStock in own company's warehouse (restricted member): %v", err)
	}
	// An adjustment in the same warehouse must also succeed.
	if _, _, err := svc.RecordAdjustment(ctx, restricted, inventoryapp.RecordAdjustmentParams{
		WarehouseID: fx.WarehouseID, Reason: "recount",
		Lines: []inventoryapp.AdjustmentLineParams{{ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "1"), MovementType: inventorydomain.MovementAdjustmentIn}},
	}); err != nil {
		t.Fatalf("RecordAdjustment in own company's warehouse (restricted member): %v", err)
	}

	// The same actions against company B's warehouse must be rejected —
	// the fix grants full function within the member's OWN company, not
	// every company in the organisation.
	if _, err := svc.RecordOpeningStock(ctx, restricted, inventoryapp.RecordMovementParams{
		WarehouseID: warehouseB.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
		Quantity: mustDecimal(t, "20"), UnitCost: decimalPtr(mustDecimal(t, "10")),
	}); err == nil {
		t.Fatal("RecordOpeningStock in company B's warehouse succeeded for a member restricted to company A — should have failed closed")
	}
	if _, _, err := svc.RecordAdjustment(ctx, restricted, inventoryapp.RecordAdjustmentParams{
		WarehouseID: warehouseB.ID, Reason: "recount",
		Lines: []inventoryapp.AdjustmentLineParams{{ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "1"), MovementType: inventorydomain.MovementAdjustmentIn}},
	}); err == nil {
		t.Fatal("RecordAdjustment in company B's warehouse succeeded for a member restricted to company A — should have failed closed")
	}

	// A transfer FROM the member's own warehouse INTO company B's must
	// also be rejected — RecordTransfer checks both sides, not just the
	// source (see its own doc comment).
	if _, err := svc.RecordTransfer(ctx, restricted, inventoryapp.RecordTransferParams{
		FromWarehouseID: fx.WarehouseID, ToWarehouseID: warehouseB.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "1"),
	}); err == nil {
		t.Fatal("RecordTransfer into company B's warehouse succeeded for a member restricted to company A — should have failed closed")
	}
}
