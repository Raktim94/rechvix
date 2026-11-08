package httpapi

import (
	"testing"

	"rechvix/internal/modules/sales/printing"
)

func TestPreviewCarriesPaymentsAndKeepsTheInvoiceTotal(t *testing.T) {
	d := &printing.InvoiceData{
		GrandTotal: "10000.00", ShowPayments: true, AmountPaid: "5000.00", BalanceDue: "5000.00", PaymentStatus: "PARTIAL",
		Payments: []printing.PaymentLine{{Date: "21-Oct-2026", Method: "CASH", Amount: "3000.00"}, {Date: "25-Oct-2026", Method: "UPI", Reference: "R1", Amount: "2000.00"}},
	}
	p := newPreview(d)
	if p.GrandTotal != "10000.00" {
		t.Fatalf("grand total = %s, want the full invoice amount", p.GrandTotal)
	}
	if p.Payment == nil || len(p.Payment.Payments) != 2 || p.Payment.Paid != "5000.00" || p.Payment.BalanceDue != "5000.00" || p.Payment.Status != "PARTIAL" {
		t.Fatalf("payment = %+v", p.Payment)
	}
	if newPreview(&printing.InvoiceData{GrandTotal: "1.00"}).Payment != nil {
		t.Fatal("a document with no receivable must not carry a payment block")
	}
}
