package es

import (
	"math"
	"testing"
)

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s = %.4f, want %.4f", what, got, want)
	}
}

func TestPercentScale(t *testing.T) {
	near(t, "14 years", Percent(179), 0)
	near(t, "15 years", Percent(180), 0.5)
	near(t, "25 years", Percent(300), 0.5+0.0019*120)
	near(t, "37 years", Percent(444), 1)
	near(t, "45 years", Percent(540), 1)
}

func TestFullCareerIsTwelveTimesTheBase(t *testing.T) {
	// 40 years old with 18 contributed, working until 67: 43 years by 65.
	r := Estimate(Input{AgeMonths: 480, YearsContributed: 18, MonthlyBase: 3000, ContributeUntilAge: 67})
	if !r.Entitled || r.OrdinaryAge != 65 || r.RetireAge != 65 || r.MonthsAtRetire != 516 {
		t.Fatalf("result = %+v", r)
	}
	near(t, "base reguladora", r.BaseReguladora, 300*3000.0/350)
	near(t, "percent", r.Percent, 1)
	if math.Abs(r.Annual-36000) > 0.1 { // 14 × 300/350 × base = 12 × base (monthly rounded to cents)
		t.Errorf("annual = %.2f", r.Annual)
	}
}

func TestBaseAboveMaximumIsCapped(t *testing.T) {
	r := Estimate(Input{AgeMonths: 480, YearsContributed: 18, MonthlyBase: 6000, ContributeUntilAge: 67})
	near(t, "base reguladora", r.BaseReguladora, 300*MaxBase/350)
	if !r.Capped || r.Monthly != MaxPension {
		t.Errorf("capped = %v, monthly %.2f", r.Capped, r.Monthly)
	}
}

func TestFewerThanFifteenYearsGivesNothing(t *testing.T) {
	r := Estimate(Input{AgeMonths: 720, YearsContributed: 5, MonthlyBase: 2000, ContributeUntilAge: 60})
	if r.Entitled || r.Annual != 0 {
		t.Errorf("result = %+v", r)
	}
}

func TestGapsAreFilledWithTheMinimumBase(t *testing.T) {
	// 33 years by 67 (stops at 63): early retirement at 65 is not allowed.
	r := Estimate(Input{AgeMonths: 480, YearsContributed: 10, MonthlyBase: 3000, ContributeUntilAge: 63, RetireAge: 65})
	if !r.EarlyNotAllowed || r.RetireAge != 67 || r.OrdinaryAge != 67 || r.MonthsAtRetire != 396 {
		t.Fatalf("result = %+v", r)
	}
	near(t, "base reguladora", r.BaseReguladora, (252*3000+48*MinBase)/350)
	near(t, "monthly", r.Monthly, (252*3000+48*MinBase)/350*(0.5+0.0019*216))

	self := Estimate(Input{AgeMonths: 480, YearsContributed: 10, MonthlyBase: 3000, ContributeUntilAge: 63, SelfEmployed: true})
	near(t, "self-employed base reguladora", self.BaseReguladora, 252*3000.0/350)

	long := Estimate(Input{AgeMonths: 480, YearsContributed: 10, MonthlyBase: 3000, ContributeUntilAge: 57})
	// 120 gap months: 48 at 100 %, 12 at 80 %, 60 at 50 % of the minimum.
	near(t, "long gap", long.BaseReguladora, (180*3000+(48+12*0.8+60*0.5)*MinBase)/350)
}

func TestEarlyAndLateRetirement(t *testing.T) {
	// 44 years by 64: one year early costs 17 % × 12/24.
	early := Estimate(Input{AgeMonths: 480, YearsContributed: 20, MonthlyBase: 2500, ContributeUntilAge: 64, RetireAge: 64})
	if early.EarlyNotAllowed || early.RetireAge != 64 {
		t.Fatalf("early = %+v", early)
	}
	near(t, "early adjustment", early.Adjustment, -0.085)

	late := Estimate(Input{AgeMonths: 480, YearsContributed: 20, MonthlyBase: 2500, ContributeUntilAge: 68, RetireAge: 68})
	near(t, "late adjustment", late.Adjustment, 0.12)
	near(t, "late monthly", late.Monthly, 2500*300.0/350*1.12)
}

func TestTopUpToMinimum(t *testing.T) {
	in := Input{AgeMonths: 600, YearsContributed: 10, MonthlyBase: MinBase, ContributeUntilAge: 55}
	plain := Estimate(in)
	in.TopUpToMinimum = true
	topped := Estimate(in)
	if !plain.Entitled || plain.Monthly >= MinPension {
		t.Fatalf("plain = %+v", plain)
	}
	want := math.Min(MinPension, plain.Monthly+MaxComplement)
	if !topped.ToppedUp || math.Abs(topped.Monthly-want) > 0.01 {
		t.Errorf("topped = %.2f, want %.2f", topped.Monthly, want)
	}
}
