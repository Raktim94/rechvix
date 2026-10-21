// Package geo holds reference data for countries: ISO 3166-1 codes, names and
// default currencies (generated, see scripts/gengeo) plus tax-relevant
// groupings such as EU membership.
package geo

import "strings"

var byCode = func() map[string]Country {
	m := make(map[string]Country, len(Countries))
	for _, c := range Countries {
		m[c.Code] = c
	}
	return m
}()

// Lookup returns the country for an ISO 3166-1 alpha-2 code (case-insensitive).
func Lookup(code string) (Country, bool) {
	c, ok := byCode[strings.ToUpper(strings.TrimSpace(code))]
	return c, ok
}

// euMembers are the 27 EU member states (verified against the member-state
// VAT tables of taxfoundation.org and eurofiscalis.com, Oct 2026). The EU VAT
// area differs from this list (it adds e.g. Monaco/Isle of Man for some
// purposes and excludes certain territories such as the Canary Islands);
// the tax packs handle those cases explicitly.
var euMembers = map[string]bool{
	"AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true, "DK": true, "EE": true, "FI": true,
	"FR": true, "DE": true, "GR": true, "HU": true, "IE": true, "IT": true, "LV": true, "LT": true, "LU": true,
	"MT": true, "NL": true, "PL": true, "PT": true, "RO": true, "SK": true, "SI": true, "ES": true, "SE": true,
}

// IsEUMember reports whether code is one of the 27 EU member states.
func IsEUMember(code string) bool { return euMembers[strings.ToUpper(strings.TrimSpace(code))] }

// EUMembers returns the member-state codes (unordered copy).
func EUMembers() []string {
	out := make([]string, 0, len(euMembers))
	for c := range euMembers {
		out = append(out, c)
	}
	return out
}
