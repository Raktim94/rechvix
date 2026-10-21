package printing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFitsWindows1252(t *testing.T) {
	for s, want := range map[string]bool{
		"Muñoz García €": true,  // Spanish letters and the euro sign
		"Çağrı":          false, // ğ and ı are not in Windows-1252
		"Straße Müller":  true,
		"Łódź":           false, // Polish
		"Αθήνα":          false, // Greek
		"Москва":         false, // Cyrillic
		"plain ascii":    true,
	} {
		if got := fitsWindows1252(s); got != want {
			t.Errorf("fitsWindows1252(%q) = %v, want %v", s, got, want)
		}
	}
	if got := toWindows1252("€ñ"); got != "\x80\xf1" {
		t.Errorf("toWindows1252 = %q, want %q (0x80 is the euro sign, 0xF1 is ñ)", got, "\x80\xf1")
	}
}

func sampleData() InvoiceData {
	return InvoiceData{
		Seller:            SellerInfo{LegalName: "Taller Demo S.L.", TaxID: "B12345678", TaxIDLabel: "Tax ID", AddressLines: []string{"Calle Mayor 1, Madrid"}},
		BillTo:            PartyInfo{Name: "José Muñoz Peña", TaxID: "ESB87654321", TaxIDLabel: "Tax ID"},
		DocumentTypeLabel: "INVOICE",
		DocumentNumber:    "INV/2026/000007",
		IssueDate:         time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		GenericTax:        true,
		Currency:          "EUR",
		Lines: []LineItem{
			{SNo: 1, Description: "Compresor industrial", HSNSAC: "STANDARD", Quantity: "2", Rate: "100.00", TaxableValue: "200.00", TaxRate: "21%", TaxValue: "42.00", LineTotal: "242.00"},
			{SNo: 2, Description: "Manual", HSNSAC: "RATE:4", Quantity: "1", Rate: "50.00", TaxableValue: "50.00", TaxRate: "4%", TaxValue: "2.00", LineTotal: "52.00"},
		},
		SubtotalTaxable: "250.00",
		TaxTotals:       []TaxTotal{{"IVA", "21", "42.00"}, {"IVA", "4", "2.00"}},
		TaxNotes:        []string{"Reverse charge: the customer accounts for the VAT."},
		GrandTotal:      "294.00",
	}
}

func TestConversionDoesNotMutateInputAndKeepsBinaryData(t *testing.T) {
	in := sampleData()
	in.Seller.LogoPNG = []byte{1, 2, 3} // opaque binary, never converted
	out := forBuiltInFonts(in)
	if in.BillTo.Name != "José Muñoz Peña" || in.Lines[0].Description != "Compresor industrial" {
		t.Fatal("input data was modified")
	}
	if out.BillTo.Name == in.BillTo.Name {
		t.Fatal("expected the name to be converted for the built-in fonts")
	}
	if string(out.Seller.LogoPNG) != string(in.Seller.LogoPNG) {
		t.Fatal("binary logo data must pass through untouched")
	}
	if !out.IssueDate.Equal(in.IssueDate) {
		t.Fatal("time values must pass through untouched")
	}
	if len(out.TaxTotals) != 2 || out.TaxTotals[0].Label != "IVA" {
		t.Fatalf("nested slices lost: %+v", out.TaxTotals)
	}
}

func TestNeedsUnicodeOnlyWhenWindows1252CannotRepresentText(t *testing.T) {
	d := sampleData()
	if needsUnicode(d) {
		t.Error("Spanish text fits the built-in fonts")
	}
	d.BillTo.Name = "Zażółć Gęślą Jaźń"
	if !needsUnicode(d) {
		t.Error("Polish text needs the Unicode font")
	}
}

func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext not installed")
	}
	f := filepath.Join(t.TempDir(), "x.pdf")
	if err := os.WriteFile(f, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-layout", f, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	return string(out)
}

func TestRenderedSpanishInvoiceItemisesTaxAndKeepsAccents(t *testing.T) {
	for _, th := range AllThemes {
		pdf, err := RenderPDF(TemplateA4GSTInvoice, th, sampleData())
		if err != nil {
			t.Fatalf("%s: %v", th, err)
		}
		text := pdfText(t, pdf)
		for _, want := range []string{
			"INVOICE", "Taller Demo S.L.", "Tax ID: B12345678", "Tax ID: ESB87654321",
			"José Muñoz Peña", // accents survive the built-in-font path
			"IVA 21%:", "42.00", "IVA 4%:", "2.00",
			"Grand Total (EUR):", "294.00",
			"Reverse charge",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("theme %s: PDF text is missing %q\n%s", th, want, text)
			}
		}
		for _, bad := range []string{"CGST", "SGST", "IGST", "GSTIN"} {
			if strings.Contains(text, bad) {
				t.Errorf("theme %s: a non-GST invoice must not mention %q", th, bad)
			}
		}
	}
}

func TestRenderedPolishInvoiceUsesUnicodeFont(t *testing.T) {
	d := sampleData()
	d.BillTo.Name = "Zażółć Gęślą Jaźń sp. z o.o."
	pdf, err := RenderPDF(TemplateA4GSTInvoice, ThemeElegant, d)
	if err != nil {
		t.Fatal(err)
	}
	if text := pdfText(t, pdf); !strings.Contains(text, "Zażółć Gęślą Jaźń") {
		t.Errorf("Polish text lost in the PDF:\n%s", text)
	}
}

func TestIndianInvoiceKeepsGSTLayout(t *testing.T) {
	d := InvoiceData{
		Seller:            SellerInfo{LegalName: "Acme Traders", GSTIN: "27AAAAA0000A1Z5"},
		BillTo:            PartyInfo{Name: "Customer", GSTIN: "29BBBBB1111B1Z6"},
		DocumentTypeLabel: "TAX INVOICE", DocumentNumber: "INV/2026-27/000001", IssueDate: time.Now(),
		Lines:           []LineItem{{SNo: 1, Description: "Widget", HSNSAC: "8419", Quantity: "1", Rate: "100.00", TaxableValue: "100.00", CGSTRate: "9", CGSTValue: "9.00", SGSTRate: "9", SGSTValue: "9.00", LineTotal: "118.00"}},
		SubtotalTaxable: "100.00", TotalCGST: "9.00", TotalSGST: "9.00", GrandTotal: "118.00",
	}
	pdf, err := RenderPDF(TemplateA4GSTInvoice, ThemeClassic, d)
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, pdf)
	for _, want := range []string{"GSTIN: 27AAAAA0000A1Z5", "CGST", "SGST", "IGST", "TAX INVOICE", "Grand Total:"} {
		if !strings.Contains(text, want) {
			t.Errorf("India layout lost %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "Tax category") {
		t.Error("India must keep the HSN/GST columns")
	}
}
