package domain

import (
	"math"
	"testing"
)

func TestPrepaymentDueIn(t *testing.T) {
	start := Month{2026, 11}
	r := Resolved{1: Month{2030, 6}}
	cases := []struct {
		name string
		p    Prepayment
		now  Month
		want bool
	}{
		{"once on its month", Prepayment{Frequency: PrepayOnce, Start: Month{2028, 3}}, Month{2028, 3}, true},
		{"once other month", Prepayment{Frequency: PrepayOnce, Start: Month{2028, 3}}, Month{2028, 4}, false},
		{"once in the past", Prepayment{Frequency: PrepayOnce, Start: Month{2020, 1}}, Month{2020, 1}, false},
		{"once at milestone", Prepayment{Frequency: PrepayOnce, StartMS: ptr(int64(1))}, Month{2030, 6}, true},
		{"monthly open", Prepayment{Frequency: PrepayMonthly}, Month{2040, 2}, true},
		{"monthly ended", Prepayment{Frequency: PrepayMonthly, End: Month{2030, 1}}, Month{2030, 2}, false},
		{"monthly until milestone", Prepayment{Frequency: PrepayMonthly, EndMS: ptr(int64(1))}, Month{2030, 6}, false},
		{"yearly anchored to start", Prepayment{Frequency: PrepayYearly, Start: Month{2027, 7}}, Month{2031, 7}, true},
		{"yearly wrong month", Prepayment{Frequency: PrepayYearly, Start: Month{2027, 7}}, Month{2031, 8}, false},
		{"yearly anchored to plan", Prepayment{Frequency: PrepayYearly}, Month{2029, 11}, true},
	}
	for _, c := range cases {
		if got := c.p.DueIn(r, c.now, start); got != c.want {
			t.Errorf("%s: DueIn = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrepaymentValidate(t *testing.T) {
	ok := Prepayment{DebtID: 1, Amount: 100, Frequency: PrepayOnce, Mode: ReduceTerm, Start: Month{2028, 1}}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Errorf("valid prepayment has errors: %v", errs)
	}
	bad := Prepayment{Frequency: PrepayOnce, Mode: "x", FeeRate: 0.2}
	errs := bad.Validate()
	for _, f := range []string{"debt", "amount", "mode", "fee", "start"} {
		if _, ok := errs[f]; !ok {
			t.Errorf("expected error on %s, got %v", f, errs)
		}
	}
}

func TestAnnuity(t *testing.T) {
	// 150.000 € at 3 % over 300 months ≈ 711,32 €.
	if got := Annuity(150000, 0.03, 300); math.Abs(got-711.32) > 0.01 {
		t.Errorf("Annuity = %.2f", got)
	}
	if got := Annuity(1200, 0, 12); got != 100 {
		t.Errorf("zero-rate Annuity = %v", got)
	}
}
