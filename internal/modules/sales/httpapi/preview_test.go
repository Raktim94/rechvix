package httpapi

import (
	"testing"

	"rechvix/internal/modules/sales/printing"
)

func TestNewPreview_IndiaFoldsGSTIntoOneRate(t *testing.T) {
	d := &printing.InvoiceData{
		Seller: printing.SellerInfo{LegalName: "Seller", GSTIN: "27AAAAA0000A1Z5"},
		BillTo: printing.PartyInfo{Name: "Buyer", GSTIN: "29BBBBB0000B1Z5"},
		Lines: []printing.LineItem{
			{SNo: 1, Description: "Intra", CGSTRate: "9", CGSTValue: "9.00", SGSTRate: "9", SGSTValue: "9.00", LineTotal: "118.00"},
			{SNo: 2, Description: "Inter", IGSTRate: "18", IGSTValue: "18.00", LineTotal: "118.00"},
		},
		TotalCGST: "9.00", TotalSGST: "9.00", TotalIGST: "18.00", TotalCess: "0.00",
	}
	p := newPreview(d)
	if p.Lines[0].TaxRate != "18" || p.Lines[0].Tax != "18" {
		t.Errorf("CGST+SGST line = %s%% / %s, want 18 / 18", p.Lines[0].TaxRate, p.Lines[0].Tax)
	}
	if p.Lines[1].TaxRate != "18" || p.Lines[1].Tax != "18.00" {
		t.Errorf("IGST line = %s%% / %s, want 18 / 18.00", p.Lines[1].TaxRate, p.Lines[1].Tax)
	}
	if len(p.Taxes) != 3 || p.Seller.TaxLabel != "GSTIN" || p.Seller.TaxID != "27AAAAA0000A1Z5" {
		t.Errorf("taxes=%v seller=%+v; want CGST, SGST, IGST (no zero cess) and a GSTIN label", p.Taxes, p.Seller)
	}
}

func TestNewPreview_OutsideIndiaHasNoGSTLabels(t *testing.T) {
	d := &printing.InvoiceData{
		GenericTax: true,
		Seller:     printing.SellerInfo{LegalName: "Vendedor", GSTIN: "", TaxID: "ESB123", TaxIDLabel: "NIF"},
		BillTo:     printing.PartyInfo{Name: "Cliente", TaxID: "FR99", TaxIDLabel: "NIF"},
		Lines:      []printing.LineItem{{SNo: 1, TaxRate: "21%", TaxValue: "21.00", TaxableValue: "100.00", LineTotal: "121.00"}},
		TaxTotals:  []printing.TaxTotal{{Label: "IVA", Rate: "21", Amount: "21.00"}},
		TotalCGST:  "9.00",
	}
	p := newPreview(d)
	if p.Seller.TaxLabel != "NIF" || p.Seller.TaxID != "ESB123" || p.BillTo.TaxID != "FR99" {
		t.Errorf("seller=%+v billTo=%+v; want NIF registration numbers", p.Seller, p.BillTo)
	}
	if len(p.Taxes) != 1 || p.Taxes[0].Label != "IVA" {
		t.Errorf("taxes = %v, want only IVA (never CGST/SGST outside India)", p.Taxes)
	}
}
