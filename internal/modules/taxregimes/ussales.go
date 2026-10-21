package taxregimes

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	taxdomain "rechvix/internal/modules/taxation/domain"
)

// USSalesTaxEngine computes US state-level sales tax.
//
// Scope and limits (deliberate):
//   - Destination-based, state base rate only. County, city and district
//     taxes are not included; the real combined rate is usually higher.
//   - Tax is charged only where the seller is registered to collect
//     (TaxCalculationInput.CollectionRegions). Elsewhere the line is
//     "NOT_COLLECTED"; the engine does not decide whether the seller has
//     nexus, that is a legal determination.
//   - Product taxability varies by state. Line category TAXABLE (or blank) is
//     taxed; EXEMPT is not. No per-state product rules are modelled.
//   - A customer holding an exemption/resale certificate is not charged.
//
// Treat output as an estimate to be confirmed by a sales-tax service or
// advisor before filing.
type USSalesTaxEngine struct{}

func NewUSSalesTaxEngine() *USSalesTaxEngine { return &USSalesTaxEngine{} }

func (USSalesTaxEngine) Calculate(_ context.Context, in taxdomain.TaxCalculationInput) (taxdomain.TaxCalculationResult, error) {
	if len(in.Lines) == 0 {
		return taxdomain.TaxCalculationResult{}, taxdomain.ErrNoLines
	}
	if in.DocumentDate.Before(RatesEffectiveFrom) {
		return taxdomain.TaxCalculationResult{}, ErrRatesNotVerifiedForDate
	}
	cust := strings.ToUpper(strings.TrimSpace(in.CustomerCountry))
	export := cust != "" && cust != "US"

	var state string
	var rate decimal.Decimal
	if !export {
		region := strings.ToUpper(strings.TrimSpace(in.SupplyRegion))
		if !strings.HasPrefix(region, "US-") {
			return taxdomain.TaxCalculationResult{}, fmt.Errorf("%w: use a US state code such as US-CA", ErrRegionRequired)
		}
		state = strings.TrimPrefix(region, "US-")
		raw, ok := usStateBaseRates[state]
		if !ok {
			return taxdomain.TaxCalculationResult{}, fmt.Errorf("%w: unknown US state %q", ErrRegionRequired, state)
		}
		rate = decimal.RequireFromString(raw)
	}
	collect := !export && !in.CustomerTaxExempt &&
		slices.ContainsFunc(in.CollectionRegions, func(r string) bool { return strings.EqualFold(r, "US-"+state) })

	lines := make([]taxdomain.TaxLineResult, 0, len(in.Lines))
	for _, line := range in.Lines {
		code := strings.ToUpper(strings.TrimSpace(line.HSNSACCode))
		if code != "" && code != "TAXABLE" && code != "EXEMPT" {
			return taxdomain.TaxCalculationResult{}, fmt.Errorf("%w: %q (use TAXABLE or EXEMPT)", ErrTaxCodeInvalid, code)
		}
		lr := lineRate{Classification: "TAXABLE_STATE_BASE_RATE_ONLY"}
		switch {
		case export:
			lr.Classification = "EXPORT"
		case code == "EXEMPT":
			lr.Classification = "EXEMPT"
		case in.CustomerTaxExempt:
			lr.Classification = "EXEMPT_CERTIFICATE"
		case !collect:
			lr.Classification = "NOT_COLLECTED"
		case rate.IsPositive():
			lr.Components = []componentRate{{Type: "STATE_SALES_TAX", Rate: rate}}
		default:
			lr.Classification = "STATE_HAS_NO_SALES_TAX"
		}
		if code == "EXEMPT" {
			lr.Components = nil
		}
		res, err := computeLine(line, lr, "US_SALES_TAX_"+state)
		if err != nil {
			return taxdomain.TaxCalculationResult{}, err
		}
		lines = append(lines, res)
	}
	return totals(in, lines)
}
