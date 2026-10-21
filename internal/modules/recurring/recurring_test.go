package recurring

import (
	"testing"
	"time"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func TestNextClampsAndDoesNotDrift(t *testing.T) {
	cases := []struct {
		from time.Time
		f    Frequency
		n    int
		want time.Time
	}{
		{d(2026, 1, 31), Monthly, 1, d(2026, 2, 28)},
		{d(2028, 1, 31), Monthly, 1, d(2028, 2, 29)},
		{d(2026, 2, 28), Monthly, 1, d(2026, 3, 28)},
		{d(2026, 11, 30), Monthly, 3, d(2027, 2, 28)},
		{d(2026, 12, 15), Monthly, 1, d(2027, 1, 15)},
		{d(2026, 1, 31), Quarterly, 1, d(2026, 4, 30)},
		{d(2028, 2, 29), Yearly, 1, d(2029, 2, 28)},
		{d(2026, 10, 21), Weekly, 2, d(2026, 11, 4)},
	}
	for _, c := range cases {
		if got := Next(c.from, c.f, c.n); !got.Equal(c.want) {
			t.Errorf("Next(%s,%s,%d) = %s, want %s", c.from.Format("2006-01-02"), c.f, c.n, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
	// Stepping from the scheduled date (not the clamped one) keeps the 31st when the month allows it.
	a := Next(d(2026, 1, 31), Monthly, 1) // 28 Feb
	b := Next(d(2026, 1, 31), Monthly, 2) // 31 Mar
	if !a.Equal(d(2026, 2, 28)) || !b.Equal(d(2026, 3, 31)) {
		t.Fatalf("got %s and %s", a, b)
	}
}

func TestNthKeepsTheAnchorDay(t *testing.T) {
	a := d(2026, 1, 31)
	want := []time.Time{d(2026, 1, 31), d(2026, 2, 28), d(2026, 3, 31), d(2026, 4, 30), d(2026, 5, 31)}
	for k, w := range want {
		if got := Nth(a, Monthly, 1, k); !got.Equal(w) {
			t.Errorf("run %d: %s, want %s", k, got.Format("2006-01-02"), w.Format("2006-01-02"))
		}
	}
	if got := Nth(d(2026, 10, 1), Weekly, 2, 3); !got.Equal(d(2026, 11, 12)) {
		t.Errorf("weekly: %s", got)
	}
}
