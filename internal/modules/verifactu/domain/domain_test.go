package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func ts(s string) time.Time {
	t, err := time.Parse(TimestampLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func day(s string) time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

// The three cases below are the worked examples in AEAT's hash specification v0.1.2, section 6.
func TestOfficialHashVectors(t *testing.T) {
	d := func(s string) decimal.Decimal { return decimal.RequireFromString(s) }

	first := AltaInput{
		ID:          InvoiceID{IssuerNIF: "89890001K", Number: "12345678/G33", IssueDate: day("01-01-2024")},
		InvoiceType: "F1", TaxTotal: d("12.35"), InvoiceTotal: d("123.45"),
		GeneratedAt: ts("2024-01-01T19:20:30+01:00"),
	}
	wantText1 := "IDEmisorFactura=89890001K&NumSerieFactura=12345678/G33&FechaExpedicionFactura=01-01-2024&TipoFactura=F1&CuotaTotal=12.35&ImporteTotal=123.45&Huella=&FechaHoraHusoGenRegistro=2024-01-01T19:20:30+01:00"
	if got := AltaString(first); got != wantText1 {
		t.Fatalf("case 1 text\n got %s\nwant %s", got, wantText1)
	}
	h1 := AltaHash(first)
	if want := "3C464DAF61ACB827C65FDA19F352A4E3BDC2C640E9E9FC4CC058073F38F12F60"; h1 != want {
		t.Fatalf("case 1 hash got %s want %s", h1, want)
	}

	second := AltaInput{
		ID:          InvoiceID{IssuerNIF: "89890001K", Number: "12345679/G34", IssueDate: day("01-01-2024")},
		InvoiceType: "F1", TaxTotal: d("12.35"), InvoiceTotal: d("123.45"),
		PreviousHash: h1, GeneratedAt: ts("2024-01-01T19:20:35+01:00"),
	}
	h2 := AltaHash(second)
	if want := "F7B94CFD8924EDFF273501B01EE5153E4CE8F259766F88CF6ACB8935802A2B97"; h2 != want {
		t.Fatalf("case 2 hash got %s want %s", h2, want)
	}

	cancel := CancelInput{
		ID:           InvoiceID{IssuerNIF: "89890001K", Number: "12345679/G34", IssueDate: day("01-01-2024")},
		PreviousHash: h2, GeneratedAt: ts("2024-01-01T19:20:40+01:00"),
	}
	wantText3 := "IDEmisorFacturaAnulada=89890001K&NumSerieFacturaAnulada=12345679/G34&FechaExpedicionFacturaAnulada=01-01-2024&Huella=F7B94CFD8924EDFF273501B01EE5153E4CE8F259766F88CF6ACB8935802A2B97&FechaHoraHusoGenRegistro=2024-01-01T19:20:40+01:00"
	if got := CancelString(cancel); got != wantText3 {
		t.Fatalf("case 3 text\n got %s\nwant %s", got, wantText3)
	}
	if want, got := "177547C0D57AC74748561D054A9CEC14B4C4EA23D1BEFD6F2E69E3A388F90C68", CancelHash(cancel); got != want {
		t.Fatalf("case 3 hash got %s want %s", got, want)
	}
}

// AEAT's QR document section 4 shows "&" inside the series being encoded as %26.
func TestQRURLEncoding(t *testing.T) {
	id := InvoiceID{IssuerNIF: "89890001K", Number: "12345678&G33", IssueDate: day("01-01-2024")}
	got, err := QRURL(EnvTest, id, decimal.RequireFromString("241.4"))
	if err != nil {
		t.Fatal(err)
	}
	// AEAT writes importe=241.4; two decimals (241.40) are equally valid numerically and we always emit two.
	want := "https://prewww2.aeat.es/wlpl/TIKE-CONT/ValidarQR?nif=89890001K&numserie=12345678%26G33&fecha=01-01-2024&importe=241.40"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	prod, _ := QRURL(EnvProduction, id, decimal.RequireFromString("241.4"))
	if !strings.HasPrefix(prod, "https://www2.agenciatributaria.gob.es/wlpl/TIKE-CONT/ValidarQR?") {
		t.Fatalf("production base wrong: %s", prod)
	}
}

func TestInvoiceNumberRules(t *testing.T) {
	if ValidateInvoiceNumber("") == nil || ValidateInvoiceNumber("é1") == nil {
		t.Fatal("empty or non-ASCII numbers must be rejected")
	}
	long := make([]byte, 61)
	for i := range long {
		long[i] = 'A'
	}
	if ValidateInvoiceNumber(string(long)) == nil {
		t.Fatal("61 characters must be rejected")
	}
	if err := ValidateInvoiceNumber("INV/26-27/000001"); err != nil {
		t.Fatal(err)
	}
}

func TestBuildBreakdownGroupsByRateAndSignsCredits(t *testing.T) {
	d := decimal.RequireFromString
	lines := []TaxLine{
		{Classification: "STANDARD", Taxable: d("100.00"), Components: []TaxComponent{{Type: "IVA", Rate: d("21"), Amount: d("21.00")}}},
		{Classification: "STANDARD", Taxable: d("50.00"), Components: []TaxComponent{{Type: "IVA", Rate: d("21"), Amount: d("10.50")}}},
		{Classification: "REDUCED", Taxable: d("40.00"), Components: []TaxComponent{{Type: "IVA", Rate: d("10"), Amount: d("4.00")}}},
		{Classification: "EXEMPT", Taxable: d("10.00")},
	}
	b, tax, err := BuildBreakdown(lines, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 3 {
		t.Fatalf("want 3 groups, got %d", len(b))
	}
	if !tax.Equal(d("35.50")) {
		t.Fatalf("tax total %s", tax)
	}
	if !b[0].Base.Equal(d("150.00")) && !b[1].Base.Equal(d("150.00")) && !b[2].Base.Equal(d("150.00")) {
		t.Fatal("the two 21% lines must merge into one 150.00 base")
	}
	neg, negTax, err := BuildBreakdown(lines, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !negTax.Equal(d("-35.50")) || neg[0].Base.Sign() > 0 && neg[1].Base.Sign() > 0 {
		t.Fatalf("credit amounts must be negative: %s", negTax)
	}
}

func TestBuildBreakdownSurchargeAndSpecialClasses(t *testing.T) {
	d := decimal.RequireFromString
	b, tax, err := BuildBreakdown([]TaxLine{
		{Classification: "STANDARD", Taxable: d("100.00"), Components: []TaxComponent{{Type: "IVA", Rate: d("21"), Amount: d("21.00")}, {Type: "RE", Rate: d("5.2"), Amount: d("5.20")}}},
		{Classification: "EXPORT", Taxable: d("30.00")},
		{Classification: "REVERSE_CHARGE", Taxable: d("20.00")},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !tax.Equal(d("26.20")) || len(b) != 3 {
		t.Fatalf("tax %s groups %d", tax, len(b))
	}
	var sawSurcharge, sawE2, sawS2 bool
	for _, x := range b {
		sawSurcharge = sawSurcharge || (x.Regime == "18" && x.SurchargeQuota != nil)
		sawE2 = sawE2 || x.Exempt == "E2"
		sawS2 = sawS2 || x.Qualification == "S2"
	}
	if !sawSurcharge || !sawE2 || !sawS2 {
		t.Fatalf("missing special groups: %+v", b)
	}
	if _, _, err := BuildBreakdown([]TaxLine{{Classification: "STANDARD", Taxable: d("1")}}, 1); err == nil {
		t.Fatal("a taxable line without an IVA component must be rejected")
	}
}
