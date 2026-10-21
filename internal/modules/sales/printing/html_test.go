package printing

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHindiBrowserPrintEscapesInputAndPreservesFigures(t *testing.T) {
	d := sampleData()
	d.Language = "hi"
	d.GenericTax = false
	d.DocumentTypeLabel = "TAX INVOICE"
	d.Seller.LegalName = "शक्ति उद्योग"
	d.BillTo.Name = "लक्ष्मी इंजीनियरिंग <script>alert(1)</script>"
	d.Lines[0].Description = "औद्योगिक क्षत्र परीक्षण"
	d.TotalCGST, d.TotalSGST = "22.00", "22.00"
	d.Currency = "INR"
	if !NeedsBrowserPrint(d) {
		t.Fatal("Hindi must use browser shaping")
	}
	body, err := RenderHTML(TemplateA4GSTInvoice, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"कर बीजक", "शक्ति उद्योग", "लक्ष्मी इंजीनियरिंग", "औद्योगिक क्षत्र परीक्षण", "294.00", "22.00", "data:font/ttf;base64,", "&lt;script&gt;"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("missing %q", want)
		}
	}
	if bytes.Contains(body, []byte("<script>")) || bytes.Contains(body, []byte("ZgotmplZ")) {
		t.Fatal("unsafe or rejected template content")
	}
	csp := HTMLContentSecurityPolicy(body)
	if !strings.Contains(csp, "style-src 'sha256-") || strings.Contains(csp, "unsafe-inline") {
		t.Fatal(csp)
	}
	if _, err := RenderPDF(TemplateA4GSTInvoice, ThemeClassic, d); err == nil {
		t.Fatal("must not generate broken Devanagari PDF")
	}
	if dir := os.Getenv("PRINT_FIXTURE_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "hindi-invoice.html"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	d.Language = "en"
	if !NeedsBrowserPrint(d) {
		t.Fatal("Hindi content with English labels also needs shaping")
	}
}
