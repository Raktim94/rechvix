package taxregimes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/geo"
	"rechvix/internal/platform/money"
)

var docDate = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

func line(ref, code, amount, ccy string, mode taxdomain.PricingMode) taxdomain.TaxableLine {
	return taxdomain.TaxableLine{LineRef: ref, HSNSACCode: code, Amount: money.MustNew(decimal.RequireFromString(amount), ccy), PricingMode: mode}
}

func in(supplier, customer, ccy string, lines ...taxdomain.TaxableLine) taxdomain.TaxCalculationInput {
	return taxdomain.TaxCalculationInput{SupplierCountry: supplier, CustomerCountry: customer, Lines: lines, DocumentDate: docDate, CurrencyCode: ccy}
}

func calc(t *testing.T, e taxdomain.TaxEngine, i taxdomain.TaxCalculationInput) taxdomain.TaxCalculationResult {
	t.Helper()
	r, err := e.Calculate(context.Background(), i)
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	return r
}

func s(m money.Money) string { return m.StringFixed(money.RoundHalfUp) }

func TestSpainIVA(t *testing.T) {
	e := NewVATEngine()
	r := calc(t, e, in("ES", "", "EUR", line("1", "STANDARD", "100.00", "EUR", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "21.00" || s(r.GrandTotal) != "121.00" || r.Lines[0].Components[0].Type != "IVA" {
		t.Fatalf("21%% IVA on 100: tax=%s grand=%s comp=%+v", s(r.TotalTaxAmount), s(r.GrandTotal), r.Lines[0].Components)
	}
	if r.Lines[0].Regime != "ES_IVA" || r.Lines[0].RatePackVersion != RatePackVersion {
		t.Errorf("snapshot metadata missing: %+v", r.Lines[0])
	}

	// Inclusive: 121.00 gross -> 100.00 taxable + 21.00 tax.
	r = calc(t, e, in("ES", "ES", "EUR", line("1", "STANDARD", "121.00", "EUR", taxdomain.PricingInclusive)))
	if s(r.TotalTaxableAmount) != "100.00" || s(r.TotalTaxAmount) != "21.00" {
		t.Fatalf("inclusive 121: taxable=%s tax=%s", s(r.TotalTaxableAmount), s(r.TotalTaxAmount))
	}

	// Inclusive 100.00 at 21%: tax = round(100 - 100/1.21) = 17.36, taxable = 82.64, reconciles to 100.00.
	r = calc(t, e, in("ES", "", "EUR", line("1", "STANDARD", "100.00", "EUR", taxdomain.PricingInclusive)))
	if s(r.TotalTaxAmount) != "17.36" || s(r.TotalTaxableAmount) != "82.64" || s(r.GrandTotal) != "100.00" {
		t.Fatalf("inclusive 100: tax=%s taxable=%s grand=%s", s(r.TotalTaxAmount), s(r.TotalTaxableAmount), s(r.GrandTotal))
	}

	// Reduced 10% and super-reduced 4%.
	r = calc(t, e, in("ES", "", "EUR",
		line("1", "RATE:10", "200.00", "EUR", taxdomain.PricingExclusive),
		line("2", "RATE:4", "50.00", "EUR", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "22.00" { // 20.00 + 2.00
		t.Fatalf("10%% of 200 + 4%% of 50 = 22.00, got %s", s(r.TotalTaxAmount))
	}
}

func TestSpainEquivalenceSurcharge(t *testing.T) {
	i := in("ES", "ES", "EUR", line("1", "RATE:10", "100.00", "EUR", taxdomain.PricingExclusive))
	i.EquivalenceSurcharge = true
	r := calc(t, NewVATEngine(), i)
	// IVA 10.00 + recargo 1.4% = 1.40
	if s(r.TotalTaxAmount) != "11.40" || len(r.Lines[0].Components) != 2 || r.Lines[0].Components[1].Type != "RE" {
		t.Fatalf("10%% + RE 1.4%% on 100 = 11.40, got %s %+v", s(r.TotalTaxAmount), r.Lines[0].Components)
	}
	i = in("ES", "ES", "EUR", line("1", "STANDARD", "100.00", "EUR", taxdomain.PricingExclusive))
	i.EquivalenceSurcharge = true
	r = calc(t, NewVATEngine(), i)
	if s(r.TotalTaxAmount) != "26.20" { // 21 + 5.2
		t.Fatalf("21%% + RE 5.2%% on 100 = 26.20, got %s", s(r.TotalTaxAmount))
	}
}

func TestSpanishTerritoriesRefused(t *testing.T) {
	for _, region := range []string{"ES-CN", "ES-CE", "ES-ML"} {
		i := in("ES", "ES", "EUR", line("1", "STANDARD", "100", "EUR", taxdomain.PricingExclusive))
		i.SupplyRegion = region
		if _, err := NewVATEngine().Calculate(context.Background(), i); !errors.Is(err, ErrRegionNotSupported) {
			t.Errorf("%s: want ErrRegionNotSupported, got %v", region, err)
		}
	}
}

func TestUKVAT(t *testing.T) {
	e := NewVATEngine()
	r := calc(t, e, in("GB", "GB", "GBP",
		line("1", "STANDARD", "100.00", "GBP", taxdomain.PricingExclusive),
		line("2", "RATE:5", "100.00", "GBP", taxdomain.PricingExclusive),
		line("3", "ZERO", "100.00", "GBP", taxdomain.PricingExclusive),
		line("4", "EXEMPT", "100.00", "GBP", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "25.00" || s(r.GrandTotal) != "425.00" {
		t.Fatalf("20+5+0+0 on 4x100: tax=%s grand=%s", s(r.TotalTaxAmount), s(r.GrandTotal))
	}
	want := []string{"STANDARD", "REDUCED", "ZERO_RATED", "EXEMPT"}
	for i, w := range want {
		if r.Lines[i].Classification != w {
			t.Errorf("line %d classification = %s, want %s", i+1, r.Lines[i].Classification, w)
		}
	}
	// Northern Ireland goods are refused; GB->EU is a zero-rated export of goods.
	ni := in("GB", "GB", "GBP", line("1", "STANDARD", "100", "GBP", taxdomain.PricingExclusive))
	ni.SupplyRegion = "GB-NIR"
	if _, err := e.Calculate(context.Background(), ni); !errors.Is(err, ErrRegionNotSupported) {
		t.Errorf("GB-NIR: want ErrRegionNotSupported, got %v", err)
	}
	r = calc(t, e, in("GB", "DE", "GBP", line("1", "STANDARD", "100", "GBP", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "0.00" || r.Lines[0].Classification != "EXPORT" {
		t.Errorf("GB->DE: %s %s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
}

func TestEUCrossBorder(t *testing.T) {
	e := NewVATEngine()
	b2b := in("DE", "FR", "EUR", line("1", "STANDARD", "1000.00", "EUR", taxdomain.PricingExclusive))
	b2b.CustomerTaxRegistered = true
	r := calc(t, e, b2b)
	if s(r.TotalTaxAmount) != "0.00" || r.Lines[0].Classification != "INTRA_EU_B2B" {
		t.Fatalf("DE->FR registered: tax=%s class=%s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
	b2c := in("DE", "FR", "EUR", line("1", "STANDARD", "1000.00", "EUR", taxdomain.PricingExclusive))
	if _, err := e.Calculate(context.Background(), b2c); !errors.Is(err, ErrCrossBorderB2CUnsupported) {
		t.Fatalf("DE->FR consumer: want ErrCrossBorderB2CUnsupported, got %v", err)
	}
	r = calc(t, e, in("DE", "US", "EUR", line("1", "STANDARD", "1000.00", "EUR", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "0.00" || r.Lines[0].Classification != "EXPORT" {
		t.Fatalf("DE->US: %s %s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
	// Domestic reverse charge.
	rc := in("ES", "ES", "EUR", line("1", "STANDARD", "500", "EUR", taxdomain.PricingExclusive))
	rc.ReverseCharge = true
	r = calc(t, e, rc)
	if s(r.TotalTaxAmount) != "0.00" || r.Lines[0].Classification != "REVERSE_CHARGE" {
		t.Fatalf("ES reverse charge: %s %s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
	// Exempt supplies stay exempt even when the sale is an export.
	r = calc(t, e, in("DE", "US", "EUR", line("1", "EXEMPT", "10", "EUR", taxdomain.PricingExclusive)))
	if r.Lines[0].Classification != "EXEMPT" {
		t.Errorf("exempt export classification = %s", r.Lines[0].Classification)
	}
}

func TestRateCodeValidation(t *testing.T) {
	e := NewVATEngine()
	cases := map[string]struct {
		country, code string
		want          error
	}{
		"unverified rate":    {"DE", "RATE:11", ErrTaxCodeInvalid},
		"blank code":         {"DE", "", ErrTaxCodeRequired},
		"garbage":            {"DE", "NORMAL", ErrTaxCodeInvalid},
		"ambiguous reduced":  {"ES", "REDUCED", ErrTaxCodeInvalid}, // ES has 4 and 10
		"denmark no reduced": {"DK", "REDUCED", ErrTaxCodeInvalid},
		"bad number":         {"DE", "RATE:abc", ErrTaxCodeInvalid},
	}
	for name, c := range cases {
		_, err := e.Calculate(context.Background(), in(c.country, "", "EUR", line("1", c.code, "100", "EUR", taxdomain.PricingExclusive)))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: want %v, got %v", name, c.want, err)
		}
	}
	// REDUCED is allowed where unambiguous: Germany has only 7%.
	r := calc(t, e, in("DE", "", "EUR", line("1", "REDUCED", "100", "EUR", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "7.00" {
		t.Errorf("DE reduced = %s, want 7.00", s(r.TotalTaxAmount))
	}
	// Finnish 25.5% on 33.33 = 8.49915 -> 8.50 (half-up).
	r = calc(t, e, in("FI", "", "EUR", line("1", "STANDARD", "33.33", "EUR", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "8.50" {
		t.Errorf("FI 25.5%% of 33.33 = %s, want 8.50", s(r.TotalTaxAmount))
	}
	// Zero-decimal currency: 19% of 999 = 189.81 -> 190.
	r = calc(t, e, in("DE", "", "JPY", line("1", "STANDARD", "999", "JPY", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "190" {
		t.Errorf("JPY tax = %s, want 190", s(r.TotalTaxAmount))
	}
}

func TestOldDocumentsRefused(t *testing.T) {
	i := in("DE", "", "EUR", line("1", "STANDARD", "100", "EUR", taxdomain.PricingExclusive))
	i.DocumentDate = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := NewVATEngine().Calculate(context.Background(), i); !errors.Is(err, ErrRatesNotVerifiedForDate) {
		t.Fatalf("2025 document: want ErrRatesNotVerifiedForDate, got %v", err)
	}
}

func TestEveryEUMemberHasSaneRates(t *testing.T) {
	for _, c := range geo.EUMembers() {
		p, ok := vatTable[c]
		if !ok {
			t.Errorf("EU member %s has no VAT table", c)
			continue
		}
		// EU law: standard rate >= 15%; the highest in the EU is 27%.
		if p.Standard.LessThan(decimal.NewFromInt(15)) || p.Standard.GreaterThan(decimal.NewFromInt(27)) {
			t.Errorf("%s standard rate %s outside 15-27%%", c, p.Standard)
		}
		for i, r := range p.Reduced {
			if r.GreaterThanOrEqual(p.Standard) {
				t.Errorf("%s reduced rate %s is not below standard %s", c, r, p.Standard)
			}
			if i > 0 && !r.GreaterThan(p.Reduced[i-1]) {
				t.Errorf("%s reduced rates not ascending: %v", c, p.Reduced)
			}
		}
	}
	// Spot-check well-known standard rates.
	for c, want := range map[string]string{"ES": "21", "DE": "19", "FR": "20", "IT": "22", "HU": "27", "LU": "17", "FI": "25.5", "GB": "20"} {
		if got := vatTable[c].Standard; !got.Equal(decimal.RequireFromString(want)) {
			t.Errorf("%s standard = %s, want %s", c, got, want)
		}
	}
}

func usIn(region string, lines ...taxdomain.TaxableLine) taxdomain.TaxCalculationInput {
	i := in("US", "US", "USD", lines...)
	i.SupplyRegion = region
	i.CollectionRegions = []string{"US-CA", "US-OR", "US-TX"}
	return i
}

func TestUSSalesTax(t *testing.T) {
	e := NewUSSalesTaxEngine()
	r := calc(t, e, usIn("US-CA", line("1", "TAXABLE", "100.00", "USD", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "7.25" || r.Lines[0].Classification != "TAXABLE_STATE_BASE_RATE_ONLY" {
		t.Fatalf("CA 7.25%% of 100: %s %s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
	r = calc(t, e, usIn("US-TX", line("1", "", "19.99", "USD", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "1.25" { // 19.99 * 6.25% = 1.249375
		t.Fatalf("TX 6.25%% of 19.99 = 1.25, got %s", s(r.TotalTaxAmount))
	}
	// Collect-state without sales tax.
	r = calc(t, e, usIn("US-OR", line("1", "TAXABLE", "100", "USD", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "0.00" || r.Lines[0].Classification != "STATE_HAS_NO_SALES_TAX" {
		t.Fatalf("OR: %s %s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
	// Not registered to collect in NY.
	r = calc(t, e, usIn("US-NY", line("1", "TAXABLE", "100", "USD", taxdomain.PricingExclusive)))
	if s(r.TotalTaxAmount) != "0.00" || r.Lines[0].Classification != "NOT_COLLECTED" {
		t.Fatalf("NY not registered: %s %s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
	// Exemption certificate and exempt product.
	cert := usIn("US-CA", line("1", "TAXABLE", "100", "USD", taxdomain.PricingExclusive))
	cert.CustomerTaxExempt = true
	if r = calc(t, e, cert); r.Lines[0].Classification != "EXEMPT_CERTIFICATE" || s(r.TotalTaxAmount) != "0.00" {
		t.Fatalf("certificate: %s %s", s(r.TotalTaxAmount), r.Lines[0].Classification)
	}
	if r = calc(t, e, usIn("US-CA", line("1", "EXEMPT", "100", "USD", taxdomain.PricingExclusive))); s(r.TotalTaxAmount) != "0.00" {
		t.Fatalf("exempt product taxed: %s", s(r.TotalTaxAmount))
	}
	// Export and missing/invalid region.
	ex := usIn("", line("1", "TAXABLE", "100", "USD", taxdomain.PricingExclusive))
	ex.CustomerCountry = "CA"
	if r = calc(t, e, ex); r.Lines[0].Classification != "EXPORT" {
		t.Fatalf("export: %s", r.Lines[0].Classification)
	}
	for _, region := range []string{"", "CA", "US-ZZ"} {
		if _, err := e.Calculate(context.Background(), usIn(region, line("1", "TAXABLE", "100", "USD", taxdomain.PricingExclusive))); !errors.Is(err, ErrRegionRequired) {
			t.Errorf("region %q: want ErrRegionRequired, got %v", region, err)
		}
	}
	if len(usStateBaseRates) != 51 {
		t.Errorf("want 50 states + DC, got %d", len(usStateBaseRates))
	}
}

type stubEngine struct{ called *bool }

func (s stubEngine) Calculate(context.Context, taxdomain.TaxCalculationInput) (taxdomain.TaxCalculationResult, error) {
	*s.called = true
	return taxdomain.TaxCalculationResult{}, nil
}

func TestDispatcherRouting(t *testing.T) {
	var india bool
	d := NewDispatcher(stubEngine{&india})
	line1 := line("1", "STANDARD", "100", "EUR", taxdomain.PricingExclusive)
	for _, c := range []string{"", "IN", " in "} {
		india = false
		if _, err := d.Calculate(context.Background(), in(c, "", "EUR", line1)); err != nil || !india {
			t.Errorf("supplier %q should reach the India engine (err=%v called=%v)", c, err, india)
		}
	}
	india = false
	if _, err := d.Calculate(context.Background(), in("FR", "", "EUR", line1)); err != nil || india {
		t.Errorf("FR must use VAT, not India (err=%v india=%v)", err, india)
	}
	if _, err := d.Calculate(context.Background(), in("ZZ", "", "EUR", line1)); !errors.Is(err, ErrRegimeNotSupported) {
		t.Errorf("unknown country: want ErrRegimeNotSupported, got %v", err)
	}
	if _, err := d.Calculate(context.Background(), in("JP", "", "JPY", line1)); !errors.Is(err, ErrRegimeNotSupported) {
		t.Errorf("JP has no pack: want ErrRegimeNotSupported, got %v", err)
	}
}
