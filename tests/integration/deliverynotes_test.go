//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
)

func onHand(t *testing.T, ctx context.Context, fx accountingFixture) string {
	t.Helper()
	var q string
	if err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT quantity_on_hand::text FROM stock_balances WHERE warehouse_id = $1 AND product_variant_id = $2`, fx.WarehouseID, fx.VariantID).Scan(&q)
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

func finalizedChallan(t *testing.T, ctx context.Context, svc *salesapp.Service, fx accountingFixture, qty string) *salesdomain.Document {
	t.Helper()
	d, err := svc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocDeliveryChallan, CurrencyCode: "EUR", BaseCurrencyCode: "EUR", ExchangeRate: mustDecimal(t, "1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{DocumentID: d.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, qty), UnitPrice: mustDecimal(t, "10")}); err != nil {
		t.Fatal(err)
	}
	out, err := svc.FinalizeDocument(ctx, fx.Principal, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDeliveryNotes_SeveralNotesOneInvoiceWithoutDoubleStockDeduction(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	if got := onHand(t, ctx, fx); got != "100.000000" {
		t.Fatalf("opening stock: %s", got)
	}
	a := finalizedChallan(t, ctx, salesSvc, fx, "5")
	b := finalizedChallan(t, ctx, salesSvc, fx, "7")
	if got := onHand(t, ctx, fx); got != "88.000000" {
		t.Fatalf("two delivery notes take 12 units out: %s", got)
	}
	inv, err := salesSvc.ConvertDeliveryNotes(ctx, fx.Principal, []uuid.UUID{a.ID, b.ID, a.ID})
	if err != nil {
		t.Fatalf("bill notes: %v", err)
	}
	_, lines, err := salesSvc.GetDocument(ctx, fx.Principal, inv.ID)
	if err != nil || len(lines) != 2 {
		t.Fatalf("one invoice line per delivery-note line expected: %d %v", len(lines), err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, inv.ID); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if got := onHand(t, ctx, fx); got != "88.000000" {
		t.Fatalf("the invoice must not deduct the goods again, stock is %s", got)
	}
	// Billing the same note twice is refused.
	if _, err := salesSvc.ConvertDeliveryNotes(ctx, fx.Principal, []uuid.UUID{a.ID}); !errors.Is(err, salesdomain.ErrDeliveryNoteAlreadyBilled) {
		t.Fatalf("already billed note must be refused, got %v", err)
	}
	if _, err := salesSvc.ConvertDocument(ctx, fx.Principal, b.ID, salesdomain.DocTaxInvoice, nil); !errors.Is(err, salesdomain.ErrDeliveryNoteAlreadyBilled) {
		t.Fatalf("single conversion of a billed note must be refused too, got %v", err)
	}
	// Cancelling the invoice leaves stock alone (the notes still delivered the goods) and frees the notes.
	if _, err := salesSvc.CancelDocument(ctx, fx.Principal, inv.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := onHand(t, ctx, fx); got != "88.000000" {
		t.Fatalf("cancelling the invoice must not restock goods that stay delivered: %s", got)
	}
	if _, err := salesSvc.ConvertDeliveryNotes(ctx, fx.Principal, []uuid.UUID{a.ID, b.ID}); err != nil {
		t.Fatalf("after the cancellation the notes can be billed again: %v", err)
	}
}

func TestDeliveryNotes_SingleNoteConversionDoesNotDoubleDeduct(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	n := finalizedChallan(t, ctx, salesSvc, fx, "4")
	inv, err := salesSvc.ConvertDocument(ctx, fx.Principal, n.ID, salesdomain.DocTaxInvoice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, inv.ID); err != nil {
		t.Fatal(err)
	}
	if got := onHand(t, ctx, fx); got != "96.000000" {
		t.Fatalf("a challan billed once must leave 96 units, got %s", got)
	}
}

func TestDeliveryNotes_RefusesMixedOrNonChallans(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	inv := finalizedSale(t, ctx, salesSvc, fx, "1", "10")
	if _, err := salesSvc.ConvertDeliveryNotes(ctx, fx.Principal, []uuid.UUID{inv.ID}); !errors.Is(err, salesdomain.ErrMixedDeliveryNotes) {
		t.Fatalf("an invoice is not a delivery note: %v", err)
	}
	other := spainFixture(t, ctx, acct)
	otherNote := finalizedChallan(t, ctx, salesSvc, other, "1")
	mine := finalizedChallan(t, ctx, salesSvc, fx, "1")
	if _, err := salesSvc.ConvertDeliveryNotes(ctx, fx.Principal, []uuid.UUID{mine.ID, otherNote.ID}); err == nil {
		t.Fatal("notes from another organisation must never be combined")
	}
	if _, err := salesSvc.ConvertDeliveryNotes(ctx, fx.Principal, nil); !errors.Is(err, salesdomain.ErrMixedDeliveryNotes) {
		t.Fatalf("an empty selection must be refused: %v", err)
	}
}
