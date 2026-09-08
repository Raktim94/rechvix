//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	inventorydomain "rechvix/internal/modules/inventory/domain"
	inventorypg "rechvix/internal/modules/inventory/pg"
	orgapp "rechvix/internal/modules/organisation/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
	purchasespg "rechvix/internal/modules/purchases/pg"
	"rechvix/internal/platform/money"
)

// TestRLS_Sweep_Stage4Tables directly proves the RLS policy (not just the
// application-level org-scoped WHERE clause every repo method in this
// codebase also applies as defense-in-depth, per docs/architecture.md
// §10) blocks a cross-organisation read on every Stage 4 table not
// already covered by a dedicated app-layer RLS test
// (TestInventory_RLS_BlocksCrossOrganisationBalanceAndMovementReads and
// TestPurchases_RLS_BlocksCrossOrganisationDocumentRead cover
// stock_balances/stock_movements and purchase_documents respectively).
// Each subtest creates a row scoped to fixture A via RunScoped(orgA, ...),
// then queries for that exact row by primary key scoped to org B — RLS
// must make it invisible even though the query names the correct ID and
// applies no organisation_id predicate of its own.
func TestRLS_Sweep_Stage4Tables(t *testing.T) {
	ctx := context.Background()
	fxA := setupPurchasesFixture(t, ctx)
	fxB := setupPurchasesFixture(t, ctx)

	batches := inventorypg.NewStockBatchRepo(sharedPool)
	costLots := inventorypg.NewStockCostLotRepo(sharedPool)
	serials := inventorypg.NewSerialNumberRepo(sharedPool)
	transfers := inventorypg.NewStockTransferRepo(sharedPool)
	adjustments := inventorypg.NewStockAdjustmentRepo(sharedPool)
	reservations := inventorypg.NewStockReservationRepo(sharedPool)
	counters := purchasespg.NewDocumentRepo(sharedPool)

	t.Run("stock_batches", func(t *testing.T) {
		var id uuid.UUID
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			b, err := batches.GetOrCreate(ctx, fxA.Principal.OrganisationID, fxA.VariantID, "BATCH-"+uuid.NewString()[:8], nil, nil)
			if err != nil {
				return err
			}
			id = b.ID
			return nil
		})
		assertInvisibleToOtherOrg(t, ctx, "stock_batches", id, fxB.Principal.OrganisationID)
	})

	t.Run("stock_cost_lots", func(t *testing.T) {
		var id uuid.UUID
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			lot, err := costLots.UpsertReceipt(ctx, fxA.Principal.OrganisationID, fxA.WarehouseID, fxA.VariantID,
				mustDecimal(t, "12.50"), mustDecimal(t, "10"), "rls-sweep", nil)
			if err != nil {
				return err
			}
			id = lot.ID
			return nil
		})
		assertInvisibleToOtherOrg(t, ctx, "stock_cost_lots", id, fxB.Principal.OrganisationID)
	})

	t.Run("stock_serial_numbers", func(t *testing.T) {
		var id uuid.UUID
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			newID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			now := time.Now()
			sn := &inventorydomain.SerialNumber{ID: newID, OrganisationID: fxA.Principal.OrganisationID, ProductVariantID: fxA.VariantID,
				SerialCode: "SN-" + uuid.NewString()[:8], Status: inventorydomain.SerialInStock, CreatedAt: now, UpdatedAt: now}
			if err := serials.Create(ctx, sn); err != nil {
				return err
			}
			id = sn.ID
			return nil
		})
		assertInvisibleToOtherOrg(t, ctx, "stock_serial_numbers", id, fxB.Principal.OrganisationID)
	})

	t.Run("stock_transfers", func(t *testing.T) {
		orgSvc := newTestOrgService(t)
		destWarehouse, err := orgSvc.CreateWarehouse(ctx, fxA.Principal, orgapp.CreateWarehouseParams{
			BranchID: fxA.BranchID, Code: "WH-SWEEP-" + uuid.NewString()[:6], Name: "RLS Sweep Dest",
		})
		if err != nil {
			t.Fatalf("CreateWarehouse: %v", err)
		}
		var id uuid.UUID
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			newID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			// stock_transfers_check (from_warehouse_id <> to_warehouse_id)
			// requires two distinct warehouses even for this RLS-only probe.
			tr := &inventorydomain.StockTransfer{ID: newID, OrganisationID: fxA.Principal.OrganisationID,
				FromWarehouseID: fxA.WarehouseID, ToWarehouseID: destWarehouse.ID, Status: inventorydomain.TransferCompleted,
				CreatedBy: fxA.Principal.UserID, CreatedAt: time.Now()}
			if err := transfers.Create(ctx, tr); err != nil {
				return err
			}
			id = tr.ID
			return nil
		})
		assertInvisibleToOtherOrg(t, ctx, "stock_transfers", id, fxB.Principal.OrganisationID)
	})

	t.Run("stock_adjustments", func(t *testing.T) {
		var id uuid.UUID
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			newID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			adj := &inventorydomain.StockAdjustment{ID: newID, OrganisationID: fxA.Principal.OrganisationID, WarehouseID: fxA.WarehouseID,
				Reason: "rls sweep", CreatedBy: fxA.Principal.UserID, CreatedAt: time.Now()}
			if err := adjustments.Create(ctx, adj); err != nil {
				return err
			}
			id = adj.ID
			return nil
		})
		assertInvisibleToOtherOrg(t, ctx, "stock_adjustments", id, fxB.Principal.OrganisationID)
	})

	t.Run("stock_reservations", func(t *testing.T) {
		var id uuid.UUID
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			newID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			refID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			res := &inventorydomain.StockReservation{ID: newID, OrganisationID: fxA.Principal.OrganisationID, WarehouseID: fxA.WarehouseID,
				ProductVariantID: fxA.VariantID, Quantity: mustDecimal(t, "1"), ReferenceType: "rls-sweep", ReferenceID: refID,
				Status: inventorydomain.ReservationActive, CreatedAt: time.Now()}
			if err := reservations.Create(ctx, res); err != nil {
				return err
			}
			id = res.ID
			return nil
		})
		assertInvisibleToOtherOrg(t, ctx, "stock_reservations", id, fxB.Principal.OrganisationID)
	})

	t.Run("stock_policies", func(t *testing.T) {
		policies := inventorypg.NewStockPolicyRepo(sharedPool)
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			return policies.Upsert(ctx, inventorydomain.StockPolicy{
				OrganisationID: fxA.Principal.OrganisationID, WarehouseID: fxA.WarehouseID, ProductVariantID: fxA.VariantID,
				ReorderLevel: mustDecimal(t, "5"), SafetyStock: mustDecimal(t, "2"),
			})
		})
		var count int
		mustRunScoped(t, ctx, fxB.Principal.OrganisationID, func(ctx context.Context) error {
			row := sharedPool.Q(ctx).QueryRow(ctx,
				`SELECT count(*) FROM stock_policies WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3`,
				fxA.Principal.OrganisationID, fxA.WarehouseID, fxA.VariantID)
			return row.Scan(&count)
		})
		if count != 0 {
			t.Fatalf("RLS FAILED: org B could see org A's stock_policies row (count=%d)", count)
		}
	})

	t.Run("purchase_document_lines", func(t *testing.T) {
		documents := purchasespg.NewDocumentRepo(sharedPool)
		lines := purchasespg.NewDocumentLineRepo(sharedPool)
		var lineID uuid.UUID
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			docID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			doc := &purchasesdomain.Document{ID: docID, OrganisationID: fxA.Principal.OrganisationID, BranchID: fxA.BranchID,
				WarehouseID: fxA.WarehouseID, SupplierPartyID: fxA.SupplierPartyID, DocumentType: purchasesdomain.DocPurchaseOrder,
				DocumentNumber: "SWEEP-" + uuid.NewString()[:8], Status: purchasesdomain.StatusDraft, DocumentDate: time.Now(),
				CurrencyCode: "INR", CreatedBy: fxA.Principal.UserID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := documents.Create(ctx, doc); err != nil {
				return err
			}
			lineIDGen, err := uuid.NewV7()
			if err != nil {
				return err
			}
			unitPrice := money.MustNew(mustDecimal(t, "1"), "INR")
			line := &purchasesdomain.DocumentLine{ID: lineIDGen, OrganisationID: fxA.Principal.OrganisationID, PurchaseDocumentID: docID,
				LineNumber: 1, ProductVariantID: fxA.VariantID, UnitID: fxA.PCS, Quantity: mustDecimal(t, "1"),
				UnitPrice: unitPrice, LineTotal: unitPrice, CreatedAt: time.Now()}
			if err := lines.Create(ctx, line); err != nil {
				return err
			}
			lineID = line.ID
			return nil
		})
		assertInvisibleToOtherOrg(t, ctx, "purchase_document_lines", lineID, fxB.Principal.OrganisationID)
	})

	t.Run("purchase_document_counters", func(t *testing.T) {
		// No surrogate id column to look up by (composite PK org+type) —
		// prove RLS the same way TestRLS_UnsetScopeSeesNothing does: an
		// org-B-scoped read of the exact (org A, PURCHASE_ORDER) counter
		// row must see nothing, even though NextNumber(orgA, ...) was
		// just called and the row demonstrably exists.
		mustRunScoped(t, ctx, fxA.Principal.OrganisationID, func(ctx context.Context) error {
			_, err := counters.NextNumber(ctx, fxA.Principal.OrganisationID, purchasesdomain.DocPurchaseOrder)
			return err
		})
		var count int
		mustRunScoped(t, ctx, fxB.Principal.OrganisationID, func(ctx context.Context) error {
			row := sharedPool.Q(ctx).QueryRow(ctx,
				`SELECT count(*) FROM purchase_document_counters WHERE organisation_id = $1 AND document_type = $2`,
				fxA.Principal.OrganisationID, string(purchasesdomain.DocPurchaseOrder))
			return row.Scan(&count)
		})
		if count != 0 {
			t.Fatalf("RLS FAILED: org B could see org A's purchase_document_counters row (count=%d)", count)
		}
	})
}

// mustRunScoped runs fn inside sharedPool.RunScoped and fails the test on
// error, saving the same boilerplate at every call site above.
func mustRunScoped(t *testing.T, ctx context.Context, orgID uuid.UUID, fn func(context.Context) error) {
	t.Helper()
	if err := sharedPool.RunScoped(ctx, orgID, fn); err != nil {
		t.Fatalf("RunScoped(%s): %v", orgID, err)
	}
}

// assertInvisibleToOtherOrg queries `SELECT count(*) FROM <table> WHERE
// id = $1` scoped to otherOrgID and fails the test if the row is
// visible — proving RLS, not an application-level WHERE organisation_id
// filter, is what's actually blocking the read (the query here carries
// no organisation_id predicate of its own).
func assertInvisibleToOtherOrg(t *testing.T, ctx context.Context, table string, id, otherOrgID uuid.UUID) {
	t.Helper()
	var count int
	mustRunScoped(t, ctx, otherOrgID, func(ctx context.Context) error {
		row := sharedPool.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE id = $1", id)
		return row.Scan(&count)
	})
	if count != 0 {
		t.Fatalf("RLS FAILED: a transaction scoped to a different organisation could see %s row %s", table, id)
	}
}
