//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	purchasesapp "rechvix/internal/modules/purchases/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
	"rechvix/internal/platform/money"
)

func TestPurchases_AddLineDuringFinalization(t *testing.T) {
	for _, tc := range []struct {
		name       string
		editFirst  bool
		wantLines  int
		wantTotal  string
		wantStock  string
		wantLedger string
	}{
		{"edit first", true, 2, "1600", "108", "-1600.00"},
		{"finalize first", false, 1, "1000", "105", "-1000.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, bills, books, stock := newTestAccountingServices(t)
			fx := setupAccountingFixture(t, ctx, books) // 100 units in opening stock
			doc, err := bills.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
				BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierID,
				DocumentType: purchasesdomain.DocPurchaseInvoice, CurrencyCode: "INR",
			})
			if err != nil {
				t.Fatal(err)
			}
			line := purchasesapp.AddLineParams{
				DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
				Quantity: mustDecimal(t, "5"), UnitPrice: mustDecimal(t, "200"),
			}
			if _, err := bills.AddLine(ctx, fx.Principal, line); err != nil {
				t.Fatal(err)
			}
			line.Quantity = mustDecimal(t, "3")
			var finalized *purchasesdomain.Document
			add := func() error {
				_, err := bills.AddLine(ctx, fx.Principal, line)
				return err
			}
			finalize := func() error {
				var err error
				finalized, err = bills.FinalizeDocument(ctx, fx.Principal, doc.ID)
				return err
			}
			var editErr, postErr error
			if tc.editFirst {
				editErr, postErr = queuePurchaseEditAndPosting(t, ctx, fx.Principal.OrganisationID, doc.ID, add, finalize)
			} else {
				postErr, editErr = queuePurchaseEditAndPosting(t, ctx, fx.Principal.OrganisationID, doc.ID, finalize, add)
			}
			if postErr != nil {
				t.Fatal(postErr)
			}
			if tc.editFirst && editErr != nil {
				t.Fatal(editErr)
			}
			if !tc.editFirst && !errors.Is(editErr, purchasesdomain.ErrDocumentNotDraft) {
				t.Errorf("late edit returned %v, want ErrDocumentNotDraft", editErr)
			}
			saved, lines, err := bills.GetDocument(ctx, fx.Principal, doc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != tc.wantLines || saved.Status != purchasesdomain.StatusFinalized ||
				saved.LinesTotal.String() != tc.wantTotal || finalized.LinesTotal.String() != tc.wantTotal {
				t.Errorf("status=%s lines=%d saved total=%s finalize response total=%s; want FINALIZED, %d lines, total %s",
					saved.Status, len(lines), saved.LinesTotal, finalized.LinesTotal, tc.wantLines, tc.wantTotal)
			}
			checkBalances := func(wantStock, wantLedger string) {
				t.Helper()
				bal, err := stock.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
				if err != nil {
					t.Fatal(err)
				}
				entries, err := books.GetPartyLedger(ctx, fx.Principal, fx.SupplierID, time.Now())
				if err != nil || len(entries) == 0 {
					t.Fatalf("supplier ledger: %d entries, error %v", len(entries), err)
				}
				ledger := entries[len(entries)-1].RunningBalance.StringFixed(money.RoundHalfUp)
				if bal.QuantityOnHand.String() != wantStock || ledger != wantLedger {
					t.Errorf("stock=%s supplier balance=%s; want %s and %s", bal.QuantityOnHand, ledger, wantStock, wantLedger)
				}
			}
			checkBalances(tc.wantStock, tc.wantLedger)
			if _, err := bills.CancelDocument(ctx, fx.Principal, doc.ID); err != nil {
				t.Fatal(err)
			}
			checkBalances("100", "0.00")
		})
	}
}

// Queue the two real service calls in a known order at the database. Release the
// invoice only after both are waiting, so the test also covers stale draft reads.
func queuePurchaseEditAndPosting(t *testing.T, ctx context.Context, orgID, docID uuid.UUID, first, second func() error) (error, error) {
	t.Helper()
	results := [2]chan error{make(chan error, 1), make(chan error, 1)}
	err := sharedPool.RunScoped(ctx, orgID, func(lockCtx context.Context) error {
		q := sharedPool.Q(lockCtx)
		if _, err := q.Exec(lockCtx, "SELECT id FROM purchase_documents WHERE organisation_id=$1 AND id=$2 FOR UPDATE", orgID, docID); err != nil {
			return err
		}
		for i, operation := range []func() error{first, second} {
			go func() { results[i] <- operation() }()
			for {
				var waiting int
				if err := q.QueryRow(lockCtx, `WITH RECURSIVE waiters(pid) AS (
					SELECT pg_backend_pid()
					UNION
					SELECT l.pid FROM pg_locks l JOIN waiters w ON w.pid = ANY(pg_blocking_pids(l.pid))
					WHERE NOT l.granted
				) SELECT count(*) - 1 FROM waiters`).Scan(&waiting); err != nil {
					return err
				}
				if waiting == i+1 {
					break
				}
				select {
				case err := <-results[i]:
					return fmt.Errorf("operation %d finished before it queued: %v", i+1, err)
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return <-results[0], <-results[1]
}
