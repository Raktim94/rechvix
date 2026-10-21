package taxregimes

import (
	"sort"
	"strings"
)

// Category is a selectable tax category for a line, for UI pickers.
type Category struct {
	Code string `json:"code"`           // value to store in the line's tax code
	Rate string `json:"rate,omitempty"` // percent, empty when not rate-based
	Kind string `json:"kind"`           // STANDARD | REDUCED | ZERO | EXEMPT | TAXABLE
}

// Categories lists the tax categories valid for a supplier country, or nil
// when the country has no pack or uses the India HSN/SAC scheme.
func Categories(country string) []Category {
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "US" {
		return []Category{{Code: "TAXABLE", Kind: "TAXABLE"}, {Code: "EXEMPT", Kind: "EXEMPT"}}
	}
	p, ok := vatTable[country]
	if !ok {
		return nil
	}
	out := []Category{{Code: "STANDARD", Rate: p.Standard.String(), Kind: "STANDARD"}}
	for _, r := range p.Reduced {
		out = append(out, Category{Code: "RATE:" + r.String(), Rate: r.String(), Kind: "REDUCED"})
	}
	return append(out, Category{Code: "ZERO", Rate: "0", Kind: "ZERO"}, Category{Code: "EXEMPT", Kind: "EXEMPT"})
}

// USStateRates returns the verified state base rates by ISO 3166-2 code ("US-CA").
func USStateRates() map[string]string {
	out := make(map[string]string, len(usStateBaseRates))
	for k, v := range usStateBaseRates {
		out["US-"+k] = v
	}
	return out
}

// SupportedCountries returns the sorted supplier countries that have a pack
// other than India's (which is configured through the HSN/SAC rate master).
func SupportedCountries() []string {
	out := []string{"US"}
	for c := range vatTable {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
