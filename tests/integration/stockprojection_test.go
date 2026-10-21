//go:build integration

package integration

import (
	"context"
	"testing"

	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/modules/stockprojection"
	"rechvix/internal/platform/permissions"
)

func TestStockProjection_OrdersCommitStockUntilFulfilled(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	svc := stockprojection.NewService(sharedPool, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool))

	order, err := salesSvc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocSalesOrder, CurrencyCode: "EUR", BaseCurrencyCode: "EUR", ExchangeRate: mustDecimal(t, "1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := salesSvc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{DocumentID: order.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, "30"), UnitPrice: mustDecimal(t, "10")}); err != nil {
		t.Fatal(err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, order.ID); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Get(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.OnHand.Equal(mustDecimal(t, "100")) || !p.CommittedOrders.Equal(mustDecimal(t, "30")) || !p.Projected.Equal(mustDecimal(t, "70")) {
		t.Fatalf("an open order commits 30 of 100: %+v", p)
	}
	inv, err := salesSvc.ConvertDocument(ctx, fx.Principal, order.ID, salesdomain.DocTaxInvoice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, inv.ID); err != nil {
		t.Fatal(err)
	}
	p, _ = svc.Get(ctx, fx.Principal, fx.WarehouseID, fx.VariantID)
	if !p.OnHand.Equal(mustDecimal(t, "70")) || !p.CommittedOrders.IsZero() || !p.Projected.Equal(mustDecimal(t, "70")) {
		t.Fatalf("fulfilling the order moves the commitment into stock movement: %+v", p)
	}
}
