//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	"rechvix/internal/platform/money"
)

// A phone on a flaky connection retries "record this payment". The same client_ref must return the receipt that already exists,
// never book the money twice; reusing a client_ref for a different payment is an error rather than a silent no-op.
func TestAccounting_Receipt_ClientRef_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)
	inv := finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "1", "5000")

	p := accountingapp.RecordReceiptParams{
		PartyID: fx.CustomerID, SalesDocumentID: &inv.ID, Amount: mustDecimal(t, "2000"),
		Method: accountingdomain.MethodUPI, ReceivedAt: time.Now(), ClientRef: "mobile:pay-1",
	}
	first, err := accountingSvc.RecordReceipt(ctx, fx.Principal, p)
	if err != nil {
		t.Fatalf("first RecordReceipt: %v", err)
	}
	again, err := accountingSvc.RecordReceipt(ctx, fx.Principal, p)
	if err != nil {
		t.Fatalf("retry RecordReceipt: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("retry created a second receipt (%s vs %s); want the original returned", again.ID, first.ID)
	}
	entries, err := accountingSvc.GetPartyLedger(ctx, fx.Principal, fx.CustomerID, time.Now())
	if err != nil {
		t.Fatalf("GetPartyLedger: %v", err)
	}
	if len(entries) != 2 { // the invoice, then ONE receipt
		t.Fatalf("ledger has %d entries after a retried receipt, want 2 (invoice + one receipt)", len(entries))
	}
	if got := entries[len(entries)-1].RunningBalance.StringFixed(money.RoundHalfUp); got != "3000.00" {
		t.Fatalf("balance after one ₹2,000 receipt on a ₹5,000 invoice = %s, want 3000.00", got)
	}

	other := p
	other.Amount = mustDecimal(t, "999")
	if _, err := accountingSvc.RecordReceipt(ctx, fx.Principal, other); err == nil {
		t.Fatalf("reusing client_ref for a different amount succeeded; want an error")
	}
}

// A receipt entered by mistake is reversed, never deleted: the customer's balance and the invoice's paid amount go back, a
// reversing journal is posted, and the receipt row records who/when/why. Reversing twice is refused.
func TestAccounting_ReverseReceipt_RestoresBalance_KeepsHistory(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, accountingSvc, _ := newTestAccountingServices(t)
	fx := setupAccountingFixture(t, ctx, accountingSvc)
	inv := finalizeSimpleTaxInvoice(t, ctx, salesSvc, fx, "1", "5000")

	rec, err := accountingSvc.RecordReceipt(ctx, fx.Principal, accountingapp.RecordReceiptParams{
		PartyID: fx.CustomerID, SalesDocumentID: &inv.ID, Amount: mustDecimal(t, "2000"), Method: accountingdomain.MethodCash, ReceivedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordReceipt: %v", err)
	}

	if _, err := accountingSvc.ReverseReceipt(ctx, fx.Principal, rec.ID, "   "); err == nil {
		t.Fatalf("reversal without a reason succeeded; want an error")
	}
	rev, err := accountingSvc.ReverseReceipt(ctx, fx.Principal, rec.ID, "Typed the wrong amount")
	if err != nil {
		t.Fatalf("ReverseReceipt: %v", err)
	}
	if rev.ReversedAt == nil || rev.ReversedBy == nil || rev.ReversalJournalID == nil || rev.ReversalReason != "Typed the wrong amount" {
		t.Fatalf("reversal audit fields not recorded: %+v", rev)
	}

	entries, err := accountingSvc.GetPartyLedger(ctx, fx.Principal, fx.CustomerID, time.Now())
	if err != nil {
		t.Fatalf("GetPartyLedger: %v", err)
	}
	if len(entries) != 3 { // invoice, receipt, reversal: history kept, nothing erased
		t.Fatalf("ledger has %d entries, want 3 (invoice, receipt, reversal)", len(entries))
	}
	if got := entries[len(entries)-1].RunningBalance.StringFixed(money.RoundHalfUp); got != "5000.00" {
		t.Fatalf("balance after reversing the receipt = %s, want 5000.00 (the full invoice owed again)", got)
	}

	receipts, err := accountingSvc.ListReceiptsForSalesDocument(ctx, fx.Principal, inv.ID)
	if err != nil {
		t.Fatalf("ListReceiptsForSalesDocument: %v", err)
	}
	if len(receipts) != 1 || receipts[0].ReversedAt == nil {
		t.Fatalf("receipt must still be listed, marked reversed: %+v", receipts)
	}

	doc, _, err := salesSvc.GetDocument(ctx, fx.Principal, inv.ID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if !doc.PaidAmount.IsZero() {
		t.Fatalf("invoice PaidAmount = %s after reversal, want 0 (reversed receipts must not count as paid)", doc.PaidAmount)
	}

	if _, err := accountingSvc.ReverseReceipt(ctx, fx.Principal, rec.ID, "again"); !errors.Is(err, accountingdomain.ErrReceiptAlreadyReversed) {
		t.Fatalf("second reversal error = %v, want ErrReceiptAlreadyReversed", err)
	}
}
