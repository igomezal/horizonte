package history

import (
	"math"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

// compound returns the total change of xs over the 12 months of year.
func compound(t *testing.T, s *Series, xs []float64, year int) float64 {
	t.Helper()
	i := s.Index(domain.Month{Year: year, Month: 1})
	if i < 0 || i+12 > s.Len() {
		t.Fatalf("year %d outside the data", year)
	}
	g := 1.0
	for _, x := range xs[i : i+12] {
		g *= 1 + x
	}
	return g - 1
}

func TestEmbeddedDatasetMatchesKnownHistory(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Start != (domain.Month{Year: 1871, Month: 1}) || s.Len() < 1800 {
		t.Fatalf("range %s–%s (%d months)", s.Start, s.End(), s.Len())
	}
	if s.Source == "" || s.Generated == "" {
		t.Error("dataset should record its source and generation date")
	}

	// Well-known years (Shiller's data uses monthly average prices, so the
	// figures differ a little from calendar-year index returns).
	checks := []struct {
		name     string
		xs       []float64
		year     int
		min, max float64
	}{
		{"1931 crash", s.Stock, 1931, -0.55, -0.30},
		{"1974 bear market", s.Stock, 1974, -0.40, -0.10},
		{"2008 financial crisis", s.Stock, 2008, -0.50, -0.25},
		{"1995 boom", s.Stock, 1995, 0.20, 0.45},
		{"1980 inflation", s.Inflation, 1980, 0.10, 0.16},
		{"2009 deflation scare", s.Inflation, 2009, -0.01, 0.04},
		{"1982 bond rally", s.Bond, 1982, 0.15, 0.50},
	}
	for _, c := range checks {
		if got := compound(t, s, c.xs, c.year); got < c.min || got > c.max {
			t.Errorf("%s: %.1f %%, want between %.0f %% and %.0f %%", c.name, got*100, c.min*100, c.max*100)
		}
	}

	// Long-run real stock return ≈ 6–7 % a year.
	real := 0.0
	for i := range s.Stock {
		real += math.Log((1 + s.Stock[i]) / (1 + s.Inflation[i]))
	}
	if annual := math.Exp(real*12/float64(s.Len())) - 1; annual < 0.055 || annual > 0.08 {
		t.Errorf("long-run real stock return = %.2f %% a year", annual*100)
	}
}

func TestParseRejectsGaps(t *testing.T) {
	csv := "month,stock,bond,inflation\n2000-01,0,0,0\n2000-03,0,0,0\n"
	if _, err := Parse([]byte(csv)); err == nil {
		t.Error("a missing month should be rejected")
	}
}
