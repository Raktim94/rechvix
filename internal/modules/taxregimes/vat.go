package taxregimes

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/geo"
)

// VATEngine implements VAT for Spain (IVA), the United Kingdom and the EU
// member states.
//
// Line tax categories (carried in TaxableLine.HSNSACCode):
//
//	STANDARD   the country's standard rate
//	RATE:<n>   a verified reduced rate, e.g. RATE:10 (REDUCED is accepted when
//	           the country has exactly one reduced rate)
//	ZERO       zero-rated supply
//	EXEMPT     exempt supply
//
// Cross-border treatment (derived from SupplierCountry and CustomerCountry):
//
//	EU -> other EU member, customer VAT-registered   0%, INTRA_EU_B2B (reverse charge)
//	EU -> other EU member, consumer                  refused (OSS not implemented)
//	EU/GB -> country outside the supplier's area     0%, EXPORT
//
// VAT numbers are not validated here (VIES is the caller's job) and the
// zero-rating of goods needs transport evidence the engine cannot see.
type VATEngine struct{}

func NewVATEngine() *VATEngine { return &VATEngine{} }

func (VATEngine) Calculate(_ context.Context, in taxdomain.TaxCalculationInput) (taxdomain.TaxCalculationResult, error) {
	if len(in.Lines) == 0 {
		return taxdomain.TaxCalculationResult{}, taxdomain.ErrNoLines
	}
	if in.DocumentDate.Before(RatesEffectiveFrom) {
		return taxdomain.TaxCalculationResult{}, ErrRatesNotVerifiedForDate
	}
	sup := strings.ToUpper(strings.TrimSpace(in.SupplierCountry))
	pack, ok := vatTable[sup]
	if !ok {
		return taxdomain.TaxCalculationResult{}, fmt.Errorf("%w: %q", ErrRegimeNotSupported, sup)
	}
	if why, bad := unsupportedRegions[strings.ToUpper(strings.TrimSpace(in.SupplyRegion))]; bad {
		return taxdomain.TaxCalculationResult{}, fmt.Errorf("%w: %s", ErrRegionNotSupported, why)
	}
	cust := strings.ToUpper(strings.TrimSpace(in.CustomerCountry))
	if cust == "" {
		cust = sup
	}
	scope, err := vatScope(sup, cust, in)
	if err != nil {
		return taxdomain.TaxCalculationResult{}, err
	}

	regime := "VAT"
	vatName := "VAT"
	if sup == "ES" {
		regime, vatName = "ES_IVA", "IVA"
	} else if sup == "GB" {
		regime = "GB_VAT"
	} else {
		regime = "EU_VAT_" + sup
	}

	lines := make([]taxdomain.TaxLineResult, 0, len(in.Lines))
	for _, line := range in.Lines {
		lr, err := resolveVATLine(line, pack, scope, vatName, sup, in)
		if err != nil {
			return taxdomain.TaxCalculationResult{}, err
		}
		res, err := computeLine(line, lr, regime)
		if err != nil {
			return taxdomain.TaxCalculationResult{}, err
		}
		lines = append(lines, res)
	}
	return totals(in, lines)
}

type scope int

const (
	scopeDomestic scope = iota
	scopeReverseCharge
	scopeIntraEUB2B
	scopeExport
)

func vatScope(sup, cust string, in taxdomain.TaxCalculationInput) (scope, error) {
	if sup == cust {
		if in.ReverseCharge {
			return scopeReverseCharge, nil
		}
		return scopeDomestic, nil
	}
	if sup != "GB" && geo.IsEUMember(sup) && geo.IsEUMember(cust) {
		if !in.CustomerTaxRegistered {
			return 0, ErrCrossBorderB2CUnsupported
		}
		return scopeIntraEUB2B, nil
	}
	if _, known := geo.Lookup(cust); !known {
		return 0, fmt.Errorf("taxregimes: unknown customer country %q", cust)
	}
	return scopeExport, nil
}

func resolveVATLine(line taxdomain.TaxableLine, pack vatRates, sc scope, vatName, sup string, in taxdomain.TaxCalculationInput) (lineRate, error) {
	code := strings.ToUpper(strings.TrimSpace(line.HSNSACCode))
	if code == "" {
		return lineRate{}, fmt.Errorf("%w (line %s): use STANDARD, RATE:<n>, ZERO or EXEMPT", ErrTaxCodeRequired, line.LineRef)
	}
	if code == "EXEMPT" {
		return lineRate{Classification: "EXEMPT"}, nil
	}
	switch sc {
	case scopeExport:
		return lineRate{Classification: "EXPORT"}, nil
	case scopeIntraEUB2B:
		return lineRate{Classification: "INTRA_EU_B2B"}, nil
	case scopeReverseCharge:
		return lineRate{Classification: "REVERSE_CHARGE"}, nil
	}
	if code == "ZERO" {
		return lineRate{Classification: "ZERO_RATED"}, nil
	}

	rate, class, err := pickRate(code, pack)
	if err != nil {
		return lineRate{}, fmt.Errorf("line %s: %w", line.LineRef, err)
	}
	comps := []componentRate{{Type: vatName, Rate: rate}}
	if sup == "ES" && in.EquivalenceSurcharge {
		re, ok := spainEquivalenceSurcharge[rate.String()]
		if !ok {
			return lineRate{}, fmt.Errorf("line %s: %w: no equivalence surcharge defined for %s%%", line.LineRef, ErrTaxCodeInvalid, rate)
		}
		comps = append(comps, componentRate{Type: "RE", Rate: re})
	}
	return lineRate{Classification: class, Components: comps}, nil
}

func pickRate(code string, pack vatRates) (decimal.Decimal, string, error) {
	switch {
	case code == "STANDARD":
		return pack.Standard, "STANDARD", nil
	case code == "REDUCED":
		if len(pack.Reduced) != 1 {
			return decimal.Zero, "", fmt.Errorf("%w: REDUCED is ambiguous here, use RATE:<n> (verified: %v)", ErrTaxCodeInvalid, pack.Reduced)
		}
		return pack.Reduced[0], "REDUCED", nil
	case strings.HasPrefix(code, "RATE:"):
		want, err := decimal.NewFromString(strings.TrimPrefix(code, "RATE:"))
		if err != nil {
			return decimal.Zero, "", fmt.Errorf("%w: %q", ErrTaxCodeInvalid, code)
		}
		if want.Equal(pack.Standard) {
			return pack.Standard, "STANDARD", nil
		}
		for _, r := range pack.Reduced {
			if want.Equal(r) {
				return r, "REDUCED", nil
			}
		}
		return decimal.Zero, "", fmt.Errorf("%w: %s%% (verified: standard %s, reduced %v)", ErrTaxCodeInvalid, want, pack.Standard, pack.Reduced)
	}
	return decimal.Zero, "", fmt.Errorf("%w: %q", ErrTaxCodeInvalid, code)
}
