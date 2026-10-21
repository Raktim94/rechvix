package geo

import (
	"testing"

	"rechvix/internal/platform/money"
)

func TestCountryTableIsConsistent(t *testing.T) {
	if len(Countries) != 250 {
		t.Fatalf("want 249 ISO 3166-1 countries + XK, got %d", len(Countries))
	}
	seen := map[string]bool{}
	for _, c := range Countries {
		if len(c.Code) != 2 || c.Name == "" {
			t.Errorf("bad entry %+v", c)
		}
		if seen[c.Code] {
			t.Errorf("duplicate code %s", c.Code)
		}
		seen[c.Code] = true
		if c.DefaultCurrency != "" {
			if _, ok := money.MinorDigits(c.DefaultCurrency); !ok {
				t.Errorf("%s default currency %s is not registered in money", c.Code, c.DefaultCurrency)
			}
		}
	}
	for _, legacy := range []string{"YU", "SU", "AN", "UK", "EU", "UN", "FX"} {
		if seen[legacy] {
			t.Errorf("legacy/reserved code %s must not be listed", legacy)
		}
	}
}

func TestKnownCountries(t *testing.T) {
	for code, cur := range map[string]string{"ES": "EUR", "US": "USD", "GB": "GBP", "DE": "EUR", "BG": "EUR", "HR": "EUR", "IN": "INR", "JP": "JPY", "CH": "CHF", "ZW": "ZWG"} {
		c, ok := Lookup(code)
		if !ok || c.DefaultCurrency != cur {
			t.Errorf("%s: got %+v ok=%v, want currency %s", code, c, ok, cur)
		}
	}
	if _, ok := Lookup(" es "); !ok {
		t.Error("Lookup must be case/space-insensitive")
	}
}

func TestEUMembership(t *testing.T) {
	if n := len(EUMembers()); n != 27 {
		t.Fatalf("EU has 27 members, got %d", n)
	}
	for _, c := range []string{"ES", "DE", "BG", "HR", "IE"} {
		if !IsEUMember(c) {
			t.Errorf("%s should be EU", c)
		}
	}
	for _, c := range []string{"GB", "US", "CH", "NO", "IS", "UA"} {
		if IsEUMember(c) {
			t.Errorf("%s must not be EU", c)
		}
	}
}
