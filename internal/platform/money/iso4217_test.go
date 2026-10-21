package money

import "testing"

func TestISO4217Coverage(t *testing.T) {
	if len(ISO4217) < 150 {
		t.Fatalf("expected the full ISO 4217 list, got %d currencies", len(ISO4217))
	}
	// Precision that existing code relied on must be unchanged by the generated table.
	for code, want := range map[string]int32{"INR": 2, "USD": 2, "EUR": 2, "GBP": 2, "JPY": 0, "KWD": 3, "BHD": 3, "OMR": 3} {
		if got, ok := MinorDigits(code); !ok || got != want {
			t.Errorf("%s: minor digits = %d ok=%v, want %d", code, got, ok, want)
		}
	}
	// Spot checks on units that are not 2: zero-decimal and three/four-decimal currencies.
	for code, want := range map[string]int32{"KRW": 0, "CLP": 0, "ISK": 0, "UGX": 0, "TND": 3, "JOD": 3, "LYD": 3, "CLF": 4, "UYW": 4} {
		if got, ok := MinorDigits(code); !ok || got != want {
			t.Errorf("%s: minor digits = %d ok=%v, want %d", code, got, ok, want)
		}
	}
	if _, ok := MinorDigits("XAU"); ok {
		t.Error("precious-metal codes are not transactional currencies")
	}
}
