package taxregimes

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/money"
)

var (
	// ErrRegimeNotSupported means no engine exists for the supplier's country.
	ErrRegimeNotSupported = &taxdomain.TaxCaseError{Kind: taxdomain.ErrUnsupportedTaxCase, Msg: "Tax for this supplier country is not set up"}
	// ErrRatesNotVerifiedForDate means the document predates the verified rate tables.
	ErrRatesNotVerifiedForDate = &taxdomain.TaxCaseError{Kind: taxdomain.ErrUnsupportedTaxCase, Msg: "rates are only verified for documents dated on or after 1 January 2026"}
	// ErrTaxCodeRequired means a line has no tax category.
	ErrTaxCodeRequired = &taxdomain.TaxCaseError{Kind: taxdomain.ErrInvalidTaxInput, Msg: "an item has no tax category; set one on the product"}
	// ErrTaxCodeInvalid means the line's tax category is not in the verified rate table.
	ErrTaxCodeInvalid = &taxdomain.TaxCaseError{Kind: taxdomain.ErrInvalidTaxInput, Msg: "an item's tax category is not a verified rate for this country"}
	// ErrRegionNotSupported means the supply region follows tax rules this pack does not implement.
	ErrRegionNotSupported = &taxdomain.TaxCaseError{Kind: taxdomain.ErrUnsupportedTaxCase, Msg: "tax rules for this region are not implemented"}
	// ErrCrossBorderB2CUnsupported means an EU cross-border sale to a consumer needs OSS destination-rate VAT.
	ErrCrossBorderB2CUnsupported = &taxdomain.TaxCaseError{Kind: taxdomain.ErrUnsupportedTaxCase, Msg: "EU sales to consumers in another member state (OSS destination VAT) are not implemented"}
	// ErrRegionRequired means the destination state/region is required but missing.
	ErrRegionRequired = &taxdomain.TaxCaseError{Kind: taxdomain.ErrInvalidTaxInput, Msg: "the destination state is required"}
	// ErrCrossBorderPurchaseUnsupported means a purchase from a supplier in another country (reverse charge or import VAT) is not implemented.
	ErrCrossBorderPurchaseUnsupported = &taxdomain.TaxCaseError{Kind: taxdomain.ErrUnsupportedTaxCase, Msg: "purchases from a supplier in another country (reverse charge, import VAT) are not implemented"}
)

var hundred = decimal.NewFromInt(100)

// lineRate is the resolved tax treatment of one line.
type lineRate struct {
	Classification string
	Components     []componentRate
}

type componentRate struct {
	Type string
	Rate decimal.Decimal // percent
}

// computeLine applies the shared arithmetic: taxable value at full precision,
// each component rounded half-up once to the currency's minor unit, and for
// inclusive prices taxable = gross - rounded tax so the line reconciles to the
// entered gross exactly. This mirrors gstindia.Engine.
func computeLine(line taxdomain.TaxableLine, lr lineRate, regime string) (taxdomain.TaxLineResult, error) {
	currency := line.Amount.Currency()
	gross := line.Amount.Decimal()

	combined := decimal.Zero
	for _, c := range lr.Components {
		combined = combined.Add(c.Rate)
	}
	taxableUnrounded := gross
	if line.PricingMode == taxdomain.PricingInclusive {
		taxableUnrounded = gross.Div(decimal.NewFromInt(1).Add(combined.Div(hundred)))
	}

	lineTax, err := money.Zero(currency)
	if err != nil {
		return taxdomain.TaxLineResult{}, err
	}
	comps := make([]taxdomain.TaxComponent, 0, len(lr.Components))
	for _, c := range lr.Components {
		amt, err := money.New(taxableUnrounded.Mul(c.Rate).Div(hundred), currency)
		if err != nil {
			return taxdomain.TaxLineResult{}, err
		}
		amt = amt.Round(money.RoundHalfUp)
		comps = append(comps, taxdomain.TaxComponent{Type: c.Type, Rate: c.Rate, Amount: amt})
		if lineTax, err = lineTax.Add(amt); err != nil {
			return taxdomain.TaxLineResult{}, err
		}
	}

	var taxable, grossOut money.Money
	if line.PricingMode == taxdomain.PricingInclusive {
		grossOut = line.Amount.Round(money.RoundHalfUp)
		if taxable, err = grossOut.Sub(lineTax); err != nil {
			return taxdomain.TaxLineResult{}, err
		}
	} else {
		if taxable, err = money.New(taxableUnrounded, currency); err != nil {
			return taxdomain.TaxLineResult{}, err
		}
		taxable = taxable.Round(money.RoundHalfUp)
		if grossOut, err = taxable.Add(lineTax); err != nil {
			return taxdomain.TaxLineResult{}, err
		}
	}
	return taxdomain.TaxLineResult{
		LineRef: line.LineRef, HSNSACCode: line.HSNSACCode, PricingMode: line.PricingMode,
		GrossAmount: grossOut, TaxableAmount: taxable, TotalTax: lineTax,
		Classification: lr.Classification, Components: comps,
		RateMasterID: uuid.Nil, Regime: regime, RatePackVersion: RatePackVersion,
	}, nil
}

// totals folds line results into a document result.
func totals(in taxdomain.TaxCalculationInput, lines []taxdomain.TaxLineResult) (taxdomain.TaxCalculationResult, error) {
	taxable, err := money.Zero(in.CurrencyCode)
	if err != nil {
		return taxdomain.TaxCalculationResult{}, err
	}
	tax := taxable
	for _, l := range lines {
		if taxable, err = taxable.Add(l.TaxableAmount); err != nil {
			return taxdomain.TaxCalculationResult{}, err
		}
		if tax, err = tax.Add(l.TotalTax); err != nil {
			return taxdomain.TaxCalculationResult{}, err
		}
	}
	grand, err := taxable.Add(tax)
	if err != nil {
		return taxdomain.TaxCalculationResult{}, fmt.Errorf("taxregimes: summing totals: %w", err)
	}
	return taxdomain.TaxCalculationResult{Lines: lines, TotalTaxableAmount: taxable, TotalTaxAmount: tax, GrandTotal: grand}, nil
}
