package app

import (
	"github.com/shopspring/decimal"
	"rechvix/internal/modules/sales/domain"
	"rechvix/internal/platform/money"
	"testing"
)

func TestBillingPriceCurrencyConversion(t *testing.T) {
	doc := &domain.Document{CurrencyCode: "USD", BaseCurrencyCode: "INR", ExchangeRate: decimal.RequireFromString("83.5")}
	price := money.MustNew(decimal.RequireFromString("835"), "INR")
	got, err := BillingPriceInDocumentCurrency(price, doc)
	if err != nil || got.StringFixed(0) != "10.00" || got.Currency() != "USD" {
		t.Fatalf("conversion: %v %v", got, err)
	}
	unrelated := money.MustNew(decimal.NewFromInt(10), "EUR")
	if _, err := BillingPriceInDocumentCurrency(unrelated, doc); err == nil {
		t.Fatal("must reject unknown cross-rate")
	}
	doc.ExchangeRate = decimal.Zero
	if _, err := BillingPriceInDocumentCurrency(price, doc); err == nil {
		t.Fatal("must reject zero rate")
	}
	doc.CurrencyCode, doc.ExchangeRate = "JPY", decimal.RequireFromString("0.6")
	got, err = BillingPriceInDocumentCurrency(price, doc)
	if err != nil || got.StringFixed(0) != "1392" {
		t.Fatalf("JPY rounding: %v %v", got, err)
	}
}
