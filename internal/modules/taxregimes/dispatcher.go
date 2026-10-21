package taxregimes

import (
	"context"
	"fmt"
	"strings"

	taxdomain "rechvix/internal/modules/taxation/domain"
)

// Dispatcher routes a calculation to the engine for the supplier's country.
// An empty SupplierCountry or "IN" goes to the India GST engine, which keeps
// every existing caller's behaviour unchanged.
type Dispatcher struct {
	India taxdomain.TaxEngine
	VAT   taxdomain.TaxEngine
	US    taxdomain.TaxEngine
}

// NewDispatcher wires the VAT and US engines around an existing India engine.
func NewDispatcher(india taxdomain.TaxEngine) *Dispatcher {
	return &Dispatcher{India: india, VAT: NewVATEngine(), US: NewUSSalesTaxEngine()}
}

func (d *Dispatcher) Calculate(ctx context.Context, in taxdomain.TaxCalculationInput) (taxdomain.TaxCalculationResult, error) {
	country := strings.ToUpper(strings.TrimSpace(in.SupplierCountry))
	switch {
	case country == "" || country == "IN":
		return d.India.Calculate(ctx, in)
	case country == "US":
		return d.US.Calculate(ctx, in)
	}
	if _, ok := vatTable[country]; ok {
		return d.VAT.Calculate(ctx, in)
	}
	return taxdomain.TaxCalculationResult{}, fmt.Errorf("%w: %q", ErrRegimeNotSupported, country)
}

// Supported lists the supplier countries with a tax pack, for UI and docs.
func Supported() []string {
	out := []string{"IN", "US"}
	for c := range vatTable {
		out = append(out, c)
	}
	return out
}

// IsVATCountry reports whether country is served by the VAT engine (Spain, the
// United Kingdom and the EU member states).
func IsVATCountry(country string) bool {
	_, ok := vatTable[strings.ToUpper(strings.TrimSpace(country))]
	return ok
}
