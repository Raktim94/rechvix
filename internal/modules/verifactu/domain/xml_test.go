package domain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func sampleSystem() System {
	return System{
		ProducerName: "NODEDR INFOTECH PRIVATE LIMITED", ProducerOther: &IDOther{CountryCode: "IN", IDType: "04", ID: "U72900XX2020PTC00"},
		SystemName: "Rechvix", SystemID: "RX", Version: "0.15.0", InstallationNumber: "RX-0001",
		OnlyVeriFactu: true,
	}
}

func dp(s string) *decimal.Decimal { d := decimal.RequireFromString(s); return &d }

func sampleAlta() Alta {
	return Alta{
		ID:          InvoiceID{IssuerNIF: "89890001K", Number: "FAC/26-27/000001", IssueDate: day("21-10-2026")},
		IssuerName:  "Empresa Demo SL",
		InvoiceType: "F1",
		Description: "Venta de mercancias",
		Recipients:  []Party{{Name: "Cliente Demo SA", NIF: "A12345674"}},
		Breakdown: []Breakdown{
			{Tax: "01", Regime: "01", Qualification: "S1", Rate: dp("21"), Base: decimal.RequireFromString("100.00"), Quota: dp("21.00")},
			{Tax: "01", Regime: "01", Qualification: "S1", Rate: dp("10"), Base: decimal.RequireFromString("50.00"), Quota: dp("5.00")},
		},
		TaxTotal: decimal.RequireFromString("26.00"), InvoiceTotal: decimal.RequireFromString("176.00"),
		System:      sampleSystem(),
		GeneratedAt: ts("2026-10-21T10:30:00+02:00"),
	}
}

func TestAltaValidate(t *testing.T) {
	a := sampleAlta()
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := a
	bad.TaxTotal = decimal.RequireFromString("25.00")
	if bad.Validate() == nil {
		t.Fatal("a tax total that disagrees with the breakdown must be rejected")
	}
	noNIF := a
	noNIF.ID.IssuerNIF = ""
	if noNIF.Validate() == nil {
		t.Fatal("a missing issuer NIF must be rejected")
	}
	noRecipient := a
	noRecipient.Recipients = nil
	if noRecipient.Validate() == nil {
		t.Fatal("an F1 without a recipient must be rejected")
	}
}

func TestChainLinksHashes(t *testing.T) {
	a := sampleAlta()
	first := a.Hash()
	if len(first) != 64 {
		t.Fatalf("hash length %d", len(first))
	}
	b := sampleAlta()
	b.ID.Number = "FAC/26-27/000002"
	b.Previous = &ChainRef{IssuerNIF: a.ID.IssuerNIF, Number: a.ID.Number, IssueDate: a.ID.IssueDate, Hash: first}
	if b.Hash() == first {
		t.Fatal("hashes must differ")
	}
	if !strings.Contains(AltaString(b.hashInput()), "&Huella="+first+"&") {
		t.Fatal("the next record's hash input must embed the previous fingerprint")
	}
	if !strings.Contains(b.XML(), "<sf:RegistroAnterior>") || !strings.Contains(a.XML(), "<sf:PrimerRegistro>S</sf:PrimerRegistro>") {
		t.Fatal("chain reference elements missing")
	}
}

// Generated records must satisfy AEAT's own schema. Needs a JDK on PATH (present on CI runners); skipped otherwise.
func TestRecordsValidateAgainstAEATSchema(t *testing.T) {
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java not available")
	}
	prev := sampleAlta()
	second := sampleAlta()
	second.ID.Number = "FAC/26-27/000002"
	second.Previous = &ChainRef{IssuerNIF: prev.ID.IssuerNIF, Number: prev.ID.Number, IssueDate: prev.ID.IssueDate, Hash: prev.Hash()}

	simplified := sampleAlta()
	simplified.ID.Number = "TCK/26-27/000003"
	simplified.InvoiceType = "F2"
	simplified.Recipients = nil
	simplified.Previous = &ChainRef{IssuerNIF: second.ID.IssuerNIF, Number: second.ID.Number, IssueDate: second.ID.IssueDate, Hash: second.Hash()}

	credit := sampleAlta()
	credit.ID.Number = "ABN/26-27/000001"
	credit.InvoiceType = "R1"
	credit.RectType = "I"
	credit.Rectified = []InvoiceID{prev.ID}
	credit.Breakdown = []Breakdown{{Tax: "01", Regime: "01", Qualification: "S1", Rate: dp("21"), Base: decimal.RequireFromString("-100.00"), Quota: dp("-21.00")}}
	credit.TaxTotal, credit.InvoiceTotal = decimal.RequireFromString("-21.00"), decimal.RequireFromString("-121.00")
	credit.Previous = &ChainRef{IssuerNIF: simplified.ID.IssuerNIF, Number: simplified.ID.Number, IssueDate: simplified.ID.IssueDate, Hash: simplified.Hash()}

	exempt := sampleAlta()
	exempt.ID.Number = "FAC/26-27/000004"
	exempt.Breakdown = []Breakdown{{Tax: "01", Regime: "01", Exempt: "E5", Base: decimal.RequireFromString("100.00")}}
	exempt.TaxTotal, exempt.InvoiceTotal = decimal.Zero, decimal.RequireFromString("100.00")
	exempt.Recipients = []Party{{Name: "Kunde GmbH", IDOther: &IDOther{CountryCode: "DE", IDType: "02", ID: "DE123456789"}}}
	exempt.Previous = &ChainRef{IssuerNIF: credit.ID.IssuerNIF, Number: credit.ID.Number, IssueDate: credit.ID.IssueDate, Hash: credit.Hash()}

	surcharge := sampleAlta()
	surcharge.ID.Number = "FAC/26-27/000005"
	surcharge.Breakdown = []Breakdown{{Tax: "01", Regime: "18", Qualification: "S1", Rate: dp("21"), Base: decimal.RequireFromString("100.00"), Quota: dp("21.00"), SurchargeRate: dp("5.2"), SurchargeQuota: dp("5.20")}}
	surcharge.TaxTotal, surcharge.InvoiceTotal = decimal.RequireFromString("26.20"), decimal.RequireFromString("126.20")
	surcharge.Previous = &ChainRef{IssuerNIF: exempt.ID.IssuerNIF, Number: exempt.ID.Number, IssueDate: exempt.ID.IssueDate, Hash: exempt.Hash()}

	cancel := Cancel{ID: prev.ID, Previous: &ChainRef{IssuerNIF: surcharge.ID.IssuerNIF, Number: surcharge.ID.Number, IssueDate: surcharge.ID.IssueDate, Hash: surcharge.Hash()}, System: sampleSystem(), GeneratedAt: ts("2026-10-21T10:35:00+02:00")}

	records := []string{prev.XML(), second.XML(), simplified.XML(), credit.XML(), exempt.XML(), surcharge.XML(), cancel.XML()}
	for _, a := range []Alta{prev, second, simplified, credit, exempt, surcharge} {
		if err := a.Validate(); err != nil {
			t.Fatalf("%s: %v", a.ID.Number, err)
		}
	}
	doc := `<?xml version="1.0" encoding="UTF-8"?>` + Envelope("Empresa Demo SL", "89890001K", records)
	dir := t.TempDir()
	file := filepath.Join(dir, "request.xml")
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(java, "testdata/Validate.java", "testdata/xsd/SuministroLR.xsd", file).CombinedOutput()
	if err != nil {
		t.Fatalf("AEAT schema validation failed: %s", out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
