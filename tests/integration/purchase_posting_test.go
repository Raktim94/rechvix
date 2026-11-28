//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	purchasesapp "rechvix/internal/modules/purchases/app"
	purchasesdomain "rechvix/internal/modules/purchases/domain"
	"rechvix/internal/platform/money"
)

func TestPurchases_OverlappingInvoicePostings(t *testing.T) {
	for _, tc := range []struct {
		action       string
		repeatErr    error
		wantStock    string
		wantJournals int
		wantBalance  string // supplier ledger is debit minus credit
	}{
		{"finalize", purchasesdomain.ErrDocumentNotDraft, "105", 1, "-1000.00"},
		{"cancel", purchasesdomain.ErrDocumentNotFinalized, "100", 2, "0.00"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, purchases, accounting, inventory := newTestAccountingServices(t)
			fx := setupAccountingFixture(t, ctx, accounting)
			doc, err := purchases.CreateDocument(ctx, fx.Principal, purchasesapp.CreateDocumentParams{
				BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, SupplierPartyID: fx.SupplierID,
				DocumentType: purchasesdomain.DocPurchaseInvoice, CurrencyCode: "INR",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := purchases.AddLine(ctx, fx.Principal, purchasesapp.AddLineParams{
				DocumentID: doc.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS,
				Quantity: mustDecimal(t, "5"), UnitPrice: mustDecimal(t, "200"),
			}); err != nil {
				t.Fatal(err)
			}
			post := purchases.FinalizeDocument
			if tc.action == "cancel" {
				if _, err := post(ctx, fx.Principal, doc.ID); err != nil {
					t.Fatal(err)
				}
				post = purchases.CancelDocument
			}
			results := overlapPurchaseRequests(ctx, t, fx.Principal.OrganisationID, doc.ID, func() error {
				_, err := post(ctx, fx.Principal, doc.ID)
				return err
			})
			successes := 0
			for _, err := range results {
				switch {
				case err == nil:
					successes++
				case !errors.Is(err, tc.repeatErr):
					t.Errorf("unexpected posting error: %v", err)
				}
			}
			if successes != 1 {
				t.Errorf("%d requests succeeded, want one", successes)
			}
			stock, err := inventory.GetBalance(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
			if err != nil {
				t.Fatal(err)
			}
			if !stock.QuantityOnHand.Equal(mustDecimal(t, tc.wantStock)) {
				t.Errorf("stock = %s, want %s", stock.QuantityOnHand, tc.wantStock)
			}
			entries, err := accounting.GetPartyLedger(ctx, fx.Principal, fx.SupplierID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != tc.wantJournals {
				t.Errorf("supplier ledger entries = %d, want %d", len(entries), tc.wantJournals)
			}
			if len(entries) == 0 {
				t.Fatal("supplier ledger is empty")
			}
			if got := entries[len(entries)-1].RunningBalance.StringFixed(money.RoundHalfUp); got != tc.wantBalance {
				t.Errorf("supplier balance = %s, want %s", got, tc.wantBalance)
			}
		})
	}
}

// Hold the invoice row until both real requests are waiting on a database lock.
// An ordinary SELECT can still read the row. This exercises overlapping
// transactions even on a slow runner where goroutines might otherwise run in sequence.
func overlapPurchaseRequests(ctx context.Context, t *testing.T, orgID, documentID uuid.UUID, post func() error) [2]error {
	t.Helper()
	results := make(chan error, 2)
	err := sharedPool.RunScoped(ctx, orgID, func(lockCtx context.Context) error {
		if _, err := sharedPool.Q(lockCtx).Exec(lockCtx, `SELECT id FROM purchase_documents WHERE organisation_id = $1 AND id = $2 FOR NO KEY UPDATE`, orgID, documentID); err != nil {
			return err
		}
		for range 2 {
			go func() { results <- post() }()
		}
		for {
			var waiting int
			// The second request may queue behind the first instead of directly behind us.
			if err := sharedPool.Q(lockCtx).QueryRow(lockCtx, `WITH RECURSIVE waiters(pid) AS (
				SELECT pg_backend_pid()
				UNION
				SELECT l.pid FROM pg_locks l JOIN waiters w ON w.pid = ANY(pg_blocking_pids(l.pid))
				WHERE NOT l.granted
			) SELECT count(*) - 1 FROM waiters`).Scan(&waiting); err != nil {
				return err
			}
			if waiting >= 2 {
				return nil
			}
			select {
			case <-results:
				return errors.New("purchase request finished before both requests were waiting")
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return [2]error{<-results, <-results}
}
