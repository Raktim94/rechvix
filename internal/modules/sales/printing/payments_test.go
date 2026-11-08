package printing

import (
	"strings"
	"testing"
)

func partPaid() InvoiceData {
	d := sampleData()
	d.GrandTotal = "10000.00"
	d.ShowPayments = true
	d.Payments = []PaymentLine{
		{Date: "21-Oct-2026", Method: "CASH", Amount: "3000.00"},
		{Date: "25-Oct-2026", Method: "UPI", Reference: "UTR998877", Amount: "2000.00"},
	}
	d.AmountPaid, d.BalanceDue, d.PaymentStatus = "5000.00", "5000.00", "PARTIAL"
	return d
}

// The invoice total must stay on the page next to what was paid and what is left, in every theme and template.
func TestPartPaidInvoiceShowsTotalPaymentsPaidAndDue(t *testing.T) {
	for _, tpl := range []Template{TemplateA4GSTInvoice, TemplateCompactInvoice, TemplateThermal80mm, TemplateThermal58mm} {
		for _, th := range AllThemes {
			pdf, err := RenderPDF(tpl, th, partPaid())
			if err != nil {
				t.Fatalf("%s/%s: %v", tpl, th, err)
			}
			text := pdfText(t, pdf)
			for _, want := range []string{"10000.00", "3000.00", "2000.00", "UTR998877", "Total paid", "5000.00", "Balance due", "PARTIALLY PAID"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s/%s: PDF is missing %q\n%s", tpl, th, want, text)
				}
			}
		}
	}
}

func TestHTMLInvoiceShowsPaymentSummary(t *testing.T) {
	b, err := RenderHTML(TemplateA4GSTInvoice, partPaid())
	if err != nil {
		t.Fatal(err)
	}
	h := string(b)
	for _, want := range []string{"10000.00", "3000.00", "UTR998877", "Total paid:", "Balance due:", "PARTIALLY PAID"} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
}

func TestInvoiceWithoutPaymentInfoPrintsNoPaymentBlock(t *testing.T) {
	pdf, err := RenderPDF(TemplateA4GSTInvoice, ThemeClassic, sampleData())
	if err != nil {
		t.Fatal(err)
	}
	if text := pdfText(t, pdf); strings.Contains(text, "Balance due") {
		t.Error("a quotation/credit note must not print a balance due")
	}
}

func TestUPIQRAsksForTheBalanceDueNotTheTotal(t *testing.T) {
	d := partPaid()
	d.GenericTax = false
	d.Seller.UPIID = "shop@bank"
	if uri := upiPayURI(d); !strings.Contains(uri, "am=5000.00") {
		t.Errorf("UPI link should request the 5000.00 still due, got %s", uri)
	}
}
