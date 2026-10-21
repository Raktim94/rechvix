package numbering

import (
	"testing"
	"time"
)

func TestFinancialYearFor(t *testing.T) {
	cases := []struct {
		date time.Time
		want string
	}{
		{time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC), "2025-26"},
		{time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC), "2025-26"},
		{time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), "2026-27"},
		{time.Date(2026, time.June, 15, 0, 0, 0, 0, time.UTC), "2026-27"},
		{time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC), "2026-27"},
		{time.Date(2099, time.December, 31, 0, 0, 0, 0, time.UTC), "2099-00"},
	}
	for _, c := range cases {
		if got := FinancialYearFor(c.date); got != c.want {
			t.Errorf("FinancialYearFor(%s) = %q, want %q", c.date.Format("2006-01-02"), got, c.want)
		}
	}
}

func TestSeriesPeriodFor(t *testing.T) {
	jan := time.Date(2027, time.January, 15, 0, 0, 0, 0, time.UTC)
	jun := time.Date(2026, time.June, 15, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		country string
		date    time.Time
		want    string
	}{
		{"IN", jan, "2026-27"}, // India keeps April-March
		{"", jan, "2026-27"},   // legacy callers
		{"in", jun, "2026-27"},
		{"ES", jan, "2027"}, // everyone else: calendar year
		{"ES", jun, "2026"},
		{"GB", time.Date(2027, time.March, 31, 0, 0, 0, 0, time.UTC), "2027"}, // not the UK tax year
		{"US", jun, "2026"},
		{"DE", jan, "2027"},
	}
	for _, c := range cases {
		if got := SeriesPeriodFor(c.country, c.date); got != c.want {
			t.Errorf("SeriesPeriodFor(%q, %s) = %q, want %q", c.country, c.date.Format("2006-01-02"), got, c.want)
		}
	}
}
