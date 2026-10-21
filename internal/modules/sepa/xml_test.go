package sepa

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestIBANChecksum(t *testing.T) {
	good := []string{"ES91 2100 0418 4502 0005 1332", "DE89370400440532013000", "FR1420041010050500013M02606", "gb29 nwbk 6016 1331 9268 19"}
	for _, g := range good {
		if _, err := NormalizeIBAN(g); err != nil {
			t.Errorf("%q should be valid: %v", g, err)
		}
	}
	for _, b := range []string{"ES91 2100 0418 4502 0005 1333", "DE00", "", "1234567890", "ES91210004184502000513320"} {
		if _, err := NormalizeIBAN(b); err == nil {
			t.Errorf("%q must be rejected", b)
		}
	}
}

func TestBICAndCreditorID(t *testing.T) {
	if b, err := NormalizeBIC("caixesbb"); err != nil || b != "CAIXESBB" {
		t.Fatalf("bic: %q %v", b, err)
	}
	if _, err := NormalizeBIC("XX"); err == nil {
		t.Fatal("short BIC rejected")
	}
	if c, err := ValidateCreditorID("es12 zzz 12345678"); err != nil || c != "ES12ZZZ12345678" {
		t.Fatalf("creditor id: %q %v", c, err)
	}
	if _, err := ValidateCreditorID("12345678"); err == nil {
		t.Fatal("a creditor id must start with a country code")
	}
}

func TestCleanKeepsSEPACharacterSet(t *testing.T) {
	if got := Clean("Señor Müller & Hijos, S.L. — Factura #12 ñ", 70); got != "Senor Muller + Hijos, S.L. - Factura 12 n" {
		t.Fatalf("got %q", got)
	}
	if got := Clean(strings.Repeat("a", 200), 35); len(got) != 35 {
		t.Fatalf("length %d", len(got))
	}
}

func sampleItems() []DebitItem {
	d := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return []DebitItem{
		{EndToEndID: "INV-1", Amount: decimal.RequireFromString("121.00"), MandateRef: "MND-0001", MandateDate: d, DebtorName: "Cliente Uno SL", DebtorIBAN: "ES9121000418450200051332", Remittance: "Factura FAC/26-27/000001", SequenceType: "FRST"},
		{EndToEndID: "INV-2", Amount: decimal.RequireFromString("60.50"), MandateRef: "MND-0002", MandateDate: d, DebtorName: "Müller & Söhne GmbH", DebtorIBAN: "DE89370400440532013000", DebtorBIC: "COBADEFFXXX", Remittance: "Factura FAC/26-27/000002", SequenceType: "RCUR"},
		{EndToEndID: "INV-3", Amount: decimal.RequireFromString("10.00"), MandateRef: "MND-0003", MandateDate: d, DebtorName: "Cliente Tres", DebtorIBAN: "FR1420041010050500013M02606", Remittance: "Cuota", SequenceType: "RCUR"},
	}
}

func TestBuildFileTotalsAndGrouping(t *testing.T) {
	cred := Creditor{Name: "Empresa Demo SL", IBAN: "ES9121000418450200051332", CreditorID: "ES12ZZZ12345678"}
	xml, total, err := BuildFile(cred, "RX-0001", time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), sampleItems())
	if err != nil {
		t.Fatal(err)
	}
	if !total.Equal(decimal.RequireFromString("191.50")) {
		t.Fatalf("control sum %s", total)
	}
	if strings.Count(xml, "<PmtInf>") != 2 || !strings.Contains(xml, "<SeqTp>FRST</SeqTp>") || !strings.Contains(xml, "<SeqTp>RCUR</SeqTp>") || !strings.Contains(xml, "<NbOfTxs>3</NbOfTxs>") {
		t.Fatalf("one payment block per sequence type: %s", xml)
	}
	if strings.Contains(xml, "ü") || !strings.Contains(xml, "Muller + Sohne GmbH") {
		t.Fatal("debtor names must be reduced to the SEPA character set")
	}
	if _, _, err := BuildFile(cred, "x", time.Now(), time.Now(), nil); err == nil {
		t.Fatal("an empty batch is refused")
	}
	bad := sampleItems()
	bad[0].SequenceType = ""
	if _, _, err := BuildFile(cred, "x", time.Now(), time.Now(), bad); err == nil {
		t.Fatal("a missing sequence type is refused")
	}
}

// The generated file must satisfy the ISO 20022 schema. Needs a JDK on PATH (present on CI runners); skipped otherwise.
func TestFileValidatesAgainstISOSchema(t *testing.T) {
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java not available")
	}
	for name, cred := range map[string]Creditor{
		"with BIC":    {Name: "Empresa Demo SL", IBAN: "ES9121000418450200051332", BIC: "CAIXESBBXXX", CreditorID: "ES12ZZZ12345678"},
		"without BIC": {Name: "Empresa Demo SL", IBAN: "ES9121000418450200051332", CreditorID: "ES12ZZZ12345678"},
	} {
		xml, _, err := BuildFile(cred, "RX-0001", time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), sampleItems())
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "dd.xml")
		if err := os.WriteFile(file, []byte(xml), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(java, "testdata/Validate.java", "testdata/pain.008.001.02.xsd", file).CombinedOutput(); err != nil {
			t.Fatalf("%s: ISO schema validation failed: %s", name, out)
		}
	}
}
