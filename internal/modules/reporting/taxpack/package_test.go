package taxpack

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"github.com/xuri/excelize/v2"
	"io"
	"rechvix/internal/modules/reporting/domain"
	"strings"
	"testing"
	"time"
)

func source(country string) domain.AccountantData {
	currency := map[string]string{"ES": "EUR", "GB": "GBP", "IN": "INR", "US": "USD"}[country]
	taxName := map[string]string{"ES": "IVA", "GB": "VAT", "IN": "IGST", "US": "STATE_SALES_TAX"}[country]
	return domain.AccountantData{Company: domain.AccountantCompany{ID: "company", Name: "Example Industries", Country: country, Currency: currency, TaxID: "29ABCDE1234F1Z5", State: "29"}, SnapshotAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), Documents: []domain.AccountantDocument{{ID: "sale", Direction: "OUTPUT", Type: "TAX_INVOICE", Number: "INV/26-27/000001", Date: "2026-10-01", Status: "FINALIZED", Party: "=EVIL()", Currency: currency, Rate: "1", TaxDocumentID: "tax", Taxable: "100", Tax: "20", Total: "120", JournalCount: 1, Debit: "120", Credit: "120", LedgerTax: "20"}}, Lines: []domain.AccountantLine{{DocumentID: "sale", Ref: "1", HSN: "8419", Description: "A product", Unit: "PCS", Quantity: "1", Classification: "TAXABLE", State: "27", StateName: "Maharashtra", Region: "US-CA", SupplyType: "B2C", Taxable: "100", Tax: "20", Total: "120", Components: []domain.AccountantComponent{{Name: taxName, Rate: "20", Amount: "20"}}}}}
}
func build(t *testing.T, d domain.AccountantData) *Package {
	t.Helper()
	p, err := Build(d, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func workbook(t *testing.T, p *Package) *excelize.File {
	t.Helper()
	b, err := p.Workbook()
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
func TestUKUnknownBoxesAndCAFinalFormulas(t *testing.T) {
	p := build(t, source("GB"))
	f := workbook(t, p)
	if v, _ := f.GetCellValue("Return workpaper", "C3"); v != "" {
		t.Fatal("unknown box2 must stay blank")
	}
	if v, _ := f.CalcCellValue("Return workpaper", "E4"); v != "" {
		t.Fatalf("box3 must stay unresolved: %q", v)
	}
	_ = f.SetCellInt("Return workpaper", "D3", 0)
	if v, err := f.CalcCellValue("Return workpaper", "E4"); err != nil || v != "20.00" {
		t.Fatalf("box3=%s %v", v, err)
	}
	_ = f.SetCellInt("Return workpaper", "D5", 30) // verified additional input tax
	if v, err := f.CalcCellValue("Return workpaper", "E6"); err != nil || v != "10.00" {
		t.Fatalf("box5 must be absolute 10: %s %v", v, err)
	}
	if v, err := f.CalcCellValue("Return workpaper", "I2"); err != nil || v != "RECLAIM" {
		t.Fatalf("direction %s %v", v, err)
	}
}
func TestNoCSVFormulaExecutionAndNoAssumedZero(t *testing.T) {
	p := build(t, source("GB"))
	b, err := p.ZIP()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range z.File {
		if file.Name == "Documents.csv" {
			r, _ := file.Open()
			raw, _ := io.ReadAll(r)
			_ = r.Close()
			if !bytes.Contains(raw, []byte("'=EVIL()")) {
				t.Fatal("formula-like text was not escaped")
			}
		}
	}
	f := workbook(t, p)
	if formula, _ := f.GetCellFormula("Documents", "H2"); formula != "" {
		t.Fatal("untrusted party name became formula")
	}
	if v, _ := f.GetCellValue("Documents", "H2"); v != "=EVIL()" {
		t.Fatal("literal party name lost")
	}
}
func TestGSTHeadersAndAllOrNothingImportFiles(t *testing.T) {
	p := build(t, source("IN"))
	if p.Status == "BLOCKED_ITEMS_PRESENT" {
		t.Fatalf("unexpected block %+v", p.Checks)
	}
	b, err := p.ZIP()
	if err != nil {
		t.Fatal(err)
	}
	z, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	found := false
	for _, file := range z.File {
		if file.Name == "gstr1/b2cs.csv" {
			found = true
			r, _ := file.Open()
			rows, err := csv.NewReader(r).ReadAll()
			_ = r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(rows[0], ",") != gstHeaders["b2cs.csv"] || len(rows) != 2 {
				t.Fatalf("wrong official header/row count: %+v", rows)
			}
		}
	}
	if !found {
		t.Fatal("missing GST utility CSV")
	}
	d := source("IN")
	d.Documents[0].Number = "INV/2026-27/000001"
	p = build(t, d)
	b, _ = p.ZIP()
	z, _ = zip.NewReader(bytes.NewReader(b), int64(len(b)))
	for _, file := range z.File {
		if strings.HasPrefix(file.Name, "gstr1/") {
			t.Fatal("a blocked pack must not contain partial native imports")
		}
	}
}
func TestCorruptSnapshotIsBlockedAndInvalidDecimalFails(t *testing.T) {
	d := source("ES")
	d.Lines[0].Tax = "999"
	p := build(t, d)
	found := false
	for _, c := range p.Checks {
		if c.Code == "SNAPSHOT_MISMATCH" {
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot mismatch missed")
	}
	d.Lines[0].Tax = "invalid"
	if _, err := Build(d, time.Now(), time.Now()); err == nil {
		t.Fatal("invalid decimal was accepted")
	}
}
