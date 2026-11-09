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
	// 44 years by 64 (528 months, third column): one year early costs 5,00 %
	// (art. 208.2), not the 8,50 % a proportional share of 17 % would give.
	early := Estimate(Input{AgeMonths: 480, YearsContributed: 20, MonthlyBase: 2500, ContributeUntilAge: 64, RetireAge: 64})
	if early.EarlyNotAllowed || early.RetireAge != 64 {
		t.Fatalf("early = %+v", early)
	}
	near(t, "early adjustment", early.Adjustment, -0.05)

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

// boeColumns holds art. 208.2 of the LGSS as printed in the BOE (consolidated
// text of 21 October 2026, BOE-A-2015-11724), copied one column at a time from
// 24 months down to 1, in percent. The values were checked against the 2033
// column of the tables in disposición transitoria trigésima cuarta, which
// repeats them with a label on every row.
var boeColumns = [4][24]float64{
	// fewer than 38 years and 6 months
	{21.00, 17.60, 14.67, 12.57, 11.00, 9.78, 8.80, 8.00, 7.33, 6.77, 6.29, 5.87,
		5.50, 5.18, 4.89, 4.63, 4.40, 4.19, 4.00, 3.83, 3.67, 3.52, 3.38, 3.26},
	// 38 years 6 months or more, fewer than 41 years 6 months
	{19.00, 16.50, 14.00, 12.00, 10.50, 9.33, 8.40, 7.64, 7.00, 6.46, 6.00, 5.60,
		5.25, 4.94, 4.67, 4.42, 4.20, 4.00, 3.82, 3.65, 3.50, 3.36, 3.23, 3.11},
	// 41 years 6 months or more, fewer than 44 years 6 months
	{17.00, 15.00, 13.33, 11.43, 10.00, 8.89, 8.00, 7.27, 6.67, 6.15, 5.71, 5.33,
		5.00, 4.71, 4.44, 4.21, 4.00, 3.81, 3.64, 3.48, 3.33, 3.20, 3.08, 2.96},
	// 44 years 6 months or more
	{13.00, 12.00, 11.00, 10.00, 9.20, 8.40, 7.60, 6.91, 6.33, 5.85, 5.43, 5.07,
		4.75, 4.47, 4.22, 4.00, 3.80, 3.62, 3.45, 3.30, 3.17, 3.04, 2.92, 2.81},
}

func TestEarlyReductionMatchesTheBOETable(t *testing.T) {
	// For each column: the months just inside it and, at its limits, the last
	// month of the column on the left and the first month of the column on the
	// right. A period equal to a limit belongs to the column on its right.
	inside := [4][]int{
		{420, 461}, // 35 years is the minimum to retire early
		{462, 497},
		{498, 533},
		{534, 600},
	}
	for col, months := range inside {
		for _, mc := range months {
			for ahead := 1; ahead <= 24; ahead++ {
				want := boeColumns[col][24-ahead] / 100
				if got := earlyReduction(mc, ahead); math.Abs(got-want) > 1e-9 {
					t.Errorf("%d months contributed, %d months early: %.4f, want %.4f", mc, ahead, got, want)
				}
			}
		}
	}
}

func TestEarlyReductionLimitsBelongToTheHigherColumn(t *testing.T) {
	for _, c := range []struct {
		months int
		col    int
	}{{461, 0}, {462, 1}, {497, 1}, {498, 2}, {533, 2}, {534, 3}} {
		got := earlyReduction(c.months, 24)
		want := boeColumns[c.col][0] / 100
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%d months, 24 early: %.4f, want %.4f", c.months, got, want)
		}
	}
}

func TestEarlyReductionIsNotProportional(t *testing.T) {
	// 23 months in the first column is 17,60 %; a share of 21 % would be 20,13 %.
	near(t, "23 months", earlyReduction(440, 23), 0.1760)
	near(t, "1 month", earlyReduction(440, 1), 0.0326)
	if earlyReduction(440, 0) != 0 || earlyReduction(440, -3) != 0 {
		t.Error("no advance should not reduce")
	}
	if earlyReduction(440, 30) != earlyReduction(440, 24) {
		t.Error("more than 24 months is not allowed and counts as 24")
	}
}

func TestEstimateEarlyByMonths(t *testing.T) {
	// 40 years old, 10 contributed, working until 67: 35 years at 66 would be
	// short of 38 years and 6 months at 65, so the ordinary age is 67.
	in := Input{AgeMonths: 480, YearsContributed: 10, MonthlyBase: 2500, ContributeUntilAge: 67}

	// 23 months early: 65 years and 1 month, 421 months contributed.
	in.RetireMonths = 67*12 - 23
	r := Estimate(in)
	if r.EarlyNotAllowed || r.OrdinaryAge != 67 || r.RetireMonths != 781 || r.RetireAge != 65 || r.MonthsAtRetire != 421 {
		t.Fatalf("23 months: %+v", r)
	}
	near(t, "23 months adjustment", r.Adjustment, -0.1760)
	br := 300 * 2500.0 / 350
	pct := 0.5 + 0.0019*(421-180)
	near(t, "23 months monthly", r.Monthly, br*pct*(1-0.1760))

	// 1 month early: 66 years and 11 months, 443 months contributed.
	in.RetireMonths = 67*12 - 1
	r = Estimate(in)
	if r.EarlyNotAllowed || r.RetireMonths != 803 || r.MonthsAtRetire != 443 {
		t.Fatalf("1 month: %+v", r)
	}
	near(t, "1 month adjustment", r.Adjustment, -0.0326)
	pct = 0.5 + 0.0019*248 + 0.0018*(443-180-248)
	near(t, "1 month monthly", r.Monthly, br*pct*(1-0.0326))

	// More than 24 months early is not allowed: computed at the ordinary age.
	in.RetireMonths = 67*12 - 25
	if r = Estimate(in); !r.EarlyNotAllowed || r.RetireMonths != 67*12 {
		t.Errorf("25 months: %+v", r)
	}
}

func TestEarlyColumnFollowsTheMonthsContributed(t *testing.T) {
	// Ordinary age 65 (43 years by then), pension from 64 years and 1 month:
	// 11 months early with 516 months contributed is the third column.
	in := Input{AgeMonths: 480, YearsContributed: 18, MonthlyBase: 2500, ContributeUntilAge: 67, RetireMonths: 65*12 - 11}
	r := Estimate(in)
	if r.EarlyNotAllowed || r.OrdinaryAge != 65 || r.MonthsAtRetire != 216+(769-480) {
		t.Fatalf("result = %+v", r)
	}
	near(t, "adjustment", r.Adjustment, -0.0471)
}
