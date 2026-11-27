//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	inventoryapp "rechvix/internal/modules/inventory/app"
	inventorydomain "rechvix/internal/modules/inventory/domain"
	purchasesapp "rechvix/internal/modules/purchases/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
)

func TestInventory_ConcurrentFirstReceipts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inventory := newTestInventoryService(t)
	purchases := newTestPurchasesService(t, inventory)
	fx := setupPurchasesFixture(t, ctx)

	var receiptIDs []uuid.UUID
	for _, line := range []struct{ qty, cost string }{{"5", "12"}, {"7", "24"}} {
		doc, err := purchases.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
			BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierPartyID,
			DocumentType: purchasesdomain.DocGoodsReceipt, CurrencyCode: "INR",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := purchases.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
			DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
			Quantity: mustDecimal(t, line.qty), UnitPrice: mustDecimal(t, line.cost),
		}); err != nil {
			t.Fatal(err)
		}
		receiptIDs = append(receiptIDs, doc.ID)
	}

	finalizeFirstReceiptsTogether(t, ctx, purchases, fx, receiptIDs)

	movements, err := inventory.ListMovements(ctx, fx.Principal, fx.WarehouseID, fx.VariantID, 10)
	if err != nil {
		t.Fatal(err)
	}
	total := decimal.Zero
	for _, movement := range movements {
		total = total.Add(movement.BaseQuantity)
	}
	if len(movements) != 2 || !total.Equal(mustDecimal(t, "12")) {
		t.Fatalf("movement history = %d entries totaling %s, want two totaling 12", len(movements), total)
	}
	balance, err := inventory.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.QuantityOnHand.Equal(total) {
		t.Errorf("stock on hand = %s, movement history totals %s", balance.QuantityOnHand, total)
	}
	if !balance.AverageCost.Equal(mustDecimal(t, "19")) {
		t.Errorf("average cost = %s, want 19 for 5 units at 12 and 7 at 24", balance.AverageCost)
	}
}

func finalizeFirstReceiptsTogether(t *testing.T, ctx context.Context, purchases *purchasesapp.Service, fx purchasesFixture, receiptIDs []uuid.UUID) {
	t.Helper()
	// Delay balance writes, but allow both transactions to read the initially empty balance.
	// Release the gate only when both receipts are in flight, independent of goroutine timing.
	results := make(chan error, len(receiptIDs))
	err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(lockCtx context.Context) error {
		db := sharedPool.Q(lockCtx)
		if _, err := db.Exec(lockCtx, "LOCK TABLE stock_balances IN SHARE MODE"); err != nil {
			return err
		}
		for _, id := range receiptIDs {
			go func() {
				_, err := purchases.FinalizeDocument(ctx, fx.Principal, id)
				results <- err
			}()
		}
		for {
			var waiting int
			if err := db.QueryRow(lockCtx, `WITH RECURSIVE waiters(pid) AS (
				SELECT pg_backend_pid()
				UNION
				SELECT l.pid FROM pg_locks l JOIN waiters w ON w.pid = ANY(pg_blocking_pids(l.pid))
				WHERE NOT l.granted
			) SELECT count(*) - 1 FROM waiters`).Scan(&waiting); err != nil {
				return err
			}
			if waiting >= len(receiptIDs) {
				return nil
			}
			select {
			case err := <-results:
				return fmt.Errorf("receipt finished before both balance writers were waiting: %v", err)
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for range receiptIDs {
		if err := <-results; err != nil {
			t.Fatalf("finalizing goods receipt: %v", err)
		}
	}
}

func TestInventory_UnstockedItemStaysAbsentAfterReadAndRejectedReservation(t *testing.T) {
	ctx := context.Background()
	inventory := newTestInventoryService(t)
	fx := setupInventoryFixture(t, ctx)

	balance, err := inventory.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil || !balance.QuantityOnHand.IsZero() {
		t.Fatalf("reading unstocked item: quantity = %s, error = %v", balance.QuantityOnHand, err)
	}
	_, err = inventory.Reserve(ctx, fx.Principal, inventoryapp.ReserveParams{
		WarehouseID: fx.WarehouseID, ProductVariantID: fx.VariantID, Quantity: decimal.NewFromInt(1),
	})
	if !errors.Is(err, inventorydomain.ErrInsufficientStock) {
		t.Fatalf("reserving unstocked item: got %v, want ErrInsufficientStock", err)
	}
	var exists bool
	err = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT FROM stock_balances
			WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3)`,
			fx.Principal.OrganisationID, fx.WarehouseID, fx.VariantID).Scan(&exists)
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("an ordinary read or a rejected reservation left a balance row for an unstocked item")
	}
}
