package es

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
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

// calendar returns the CalendarMonth that makes a pension starting at
// retireMonths (for someone ageMonths old today) fall in January of year.
func calendar(year, ageMonths, retireMonths int) int {
	return year*12 - (retireMonths - ageMonths)
}

// TestCapTableMatchesTheBOE reads testdata/lgss_dt34a.txt, the text of
// disposición transitoria trigésima cuarta as extracted from the BOE
// consolidated text of 21 October 2026, and compares every cell of the four
// tables (4 columns x 24 months x 10 years) with capTable.
func TestCapTableMatchesTheBOE(t *testing.T) {
	raw, err := os.ReadFile("testdata/lgss_dt34a.txt")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`^\s*(\d+)\s+((?:\d+,\d+\s*){10})$`)
	table, month, cells := -1, 0, 0
	for _, line := range strings.Split(string(raw), "\n") {
		m := row.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		months, _ := strconv.Atoi(m[1])
		if months == 24 {
			table++
			month = 24
		}
		if table < 0 || months != month {
			t.Fatalf("unexpected row %q", line)
		}
		for i, f := range strings.Fields(m[2]) {
			want, _ := strconv.ParseFloat(strings.Replace(f, ",", ".", 1), 64)
			if got := capTable[table][earlyMaxMonths-months][i]; got != want {
				t.Errorf("table %d, %d months, %d: %.2f, BOE %.2f", table, months, capFirstYear+i, got, want)
			}
			cells++
		}
		month--
	}
	if table != 3 || month != 0 || cells != 4*24*10 {
		t.Errorf("read %d tables, next month %d, %d cells", table+1, month, cells)
	}
}

func TestCapTableLastYearIsArticle208(t *testing.T) {
	for col := range earlyColumns {
		for row := range earlyMaxMonths {
			if capTable[col][row][capYears-1] != earlyTable[row][col] {
				t.Errorf("column %d, %d months: DT 34a 2033 %.2f, art. 208 %.2f", col, earlyMaxMonths-row,
					capTable[col][row][capYears-1], earlyTable[row][col])
			}
		}
	}
}

func TestCapReductionByYear(t *testing.T) {
	// Fewer than 38y6m, 24 months: 5,70 in 2024 up to 21,00 in 2033 (DT 34a).
	for year, want := range map[int]float64{2024: 0.057, 2027: 0.108, 2030: 0.159, 2032: 0.193, 2033: 0.21, 2040: 0.21, 0: 0.21, 2020: 0.057} {
		near(t, "24 months "+strconv.Itoa(year), capReduction(year, 440, 24), want)
	}
	// Copied from the BOE: 38y6m or more, 12 months, 2027: 3,30; 41y6m or
	// more, 1 month, 2025: 0,99; 44y6m or more, 23 months, 2026: 6,40.
	near(t, "12 months 2027, second column", capReduction(2027, 462, 12), 0.0330)
	near(t, "1 month 2025, third column", capReduction(2025, 498, 1), 0.0099)
	near(t, "23 months 2026, fourth column", capReduction(2026, 534, 23), 0.0640)
	// A period just under a limit stays in the column on its left.
	near(t, "12 months 2027, first column", capReduction(2027, 461, 12), 0.0340)
}

// Hand calculation for the cap cases: 50 years old, 27 contributed, base at the
// maximum, working until 63 and retiring then: 40 years (480 months), second
// column, 24 months early. The base reguladora is 300/350 x 5.101,20 =
// 4.372,46 at 100 %, above the maximum pension of 3.359,60, so the reduction
// applies to 3.359,60.
func TestEarlyRetirementAboveTheMaximumPension(t *testing.T) {
	in := Input{AgeMonths: 600, YearsContributed: 27, MonthlyBase: 5101.20, ContributeUntilAge: 63, RetireAge: 63}
	retire := 63 * 12

	in.CalendarMonth = calendar(2027, in.AgeMonths, retire)
	r := Estimate(in)
	if !r.Entitled || r.EarlyNotAllowed || !r.Capped || r.OrdinaryAge != 65 {
		t.Fatalf("2027: %+v", r)
	}
	near(t, "2027 adjustment", r.Adjustment, -0.10) // DT 34a, second table, 24 months, 2027
	near(t, "2027 monthly", r.Monthly, MaxPension*0.90)
	near(t, "2027 monthly literal", r.Monthly, 3023.64)

	in.CalendarMonth = calendar(2032, in.AgeMonths, retire)
	near(t, "2032 monthly", Estimate(in).Monthly, MaxPension*(1-0.175)) // DT 34a, second table, 24 months, 2032

	for _, cal := range []int{calendar(2033, in.AgeMonths, retire), calendar(2041, in.AgeMonths, retire), 0} {
		in.CalendarMonth = cal
		r = Estimate(in)
		near(t, "2033 on adjustment", r.Adjustment, -0.19) // art. 208.2, second column, 24 months
		near(t, "2033 on monthly", r.Monthly, 2721.28)
	}

	// 12 months early, fewer than 38y6m (first column): 3,40 % in 2027, 5,50 %
	// from 2033. 20 years contributed at 50, until 66, retire at 66 years and
	// 0 months: 36 years.
	in = Input{AgeMonths: 600, YearsContributed: 20, MonthlyBase: 5101.20, ContributeUntilAge: 66, RetireMonths: 67*12 - 12}
	in.CalendarMonth = calendar(2027, in.AgeMonths, in.RetireMonths)
	r = Estimate(in)
	if !r.Capped || r.OrdinaryAge != 67 || r.MonthsAtRetire != 240+(792-600) {
		t.Fatalf("12 months: %+v", r)
	}
	near(t, "12 months 2027", r.Monthly, 3245.37)
	in.CalendarMonth = calendar(2033, in.AgeMonths, in.RetireMonths)
	near(t, "12 months 2033", Estimate(in).Monthly, 3174.82)
}

// A pension from the percentage just below or above the maximum pension
// decides which paragraph of art. 210.3 applies: reduce the pension, or reduce
// the maximum.
func TestCapLimitIsStrictlyAbove(t *testing.T) {
	// 300/350 x base is the base reguladora; 40 years contributed is 100 %.
	atMax := MaxPension * 350 / 300 // the base at which the pension equals the maximum
	in := Input{AgeMonths: 600, YearsContributed: 27, ContributeUntilAge: 63, RetireAge: 63}
	in.CalendarMonth = calendar(2027, in.AgeMonths, 63*12)

	in.MonthlyBase = atMax - 0.01 // below: the pension is reduced with the art. 208.2 value
	below := Estimate(in)
	if below.Capped {
		t.Fatalf("below the maximum: %+v", below)
	}
	near(t, "below adjustment", below.Adjustment, -0.19)
	near(t, "below monthly", below.Monthly, (atMax-0.01)*300/350*0.81)

	in.MonthlyBase = atMax + 0.01 // above: the maximum is reduced with the 2027 value
	above := Estimate(in)
	if !above.Capped {
		t.Fatalf("above the maximum: %+v", above)
	}
	near(t, "above adjustment", above.Adjustment, -0.10)
	near(t, "above monthly", above.Monthly, MaxPension*0.90)
}

// Art. 205.1.b: 15 years, 2 of them in the 15 before the pension starts.
func TestTwoOfTheLastFifteenYears(t *testing.T) {
	// 50 years old, 13 years contributed, stops at 52: nothing in the 15 years
	// before 67 (months 624 to 804).
	r := Estimate(Input{AgeMonths: 600, YearsContributed: 13, MonthlyBase: 3000, ContributeUntilAge: 52})
	if r.Entitled || r.NotEntitledWhy != NotEntitledRecent || r.Monthly != 0 || r.Annual != 0 || r.MonthsAtRetire != 180 {
		t.Errorf("13 years, until 52: %+v", r)
	}
	r = Estimate(Input{AgeMonths: 600, YearsContributed: 25, MonthlyBase: 3000, ContributeUntilAge: 50})
	if r.Entitled || r.NotEntitledWhy != NotEntitledRecent || r.Monthly != 0 {
		t.Errorf("25 years, until 50: %+v", r)
	}
	// Fewer than 15 years keeps its own reason.
	r = Estimate(Input{AgeMonths: 720, YearsContributed: 5, MonthlyBase: 2000, ContributeUntilAge: 60})
	if r.Entitled || r.NotEntitledWhy != NotEntitledYears {
		t.Errorf("5 years: %+v", r)
	}

	// 20 years contributed at 50 (contributions from month 360), until 54: they
	// run to month 648. A pension at 67 (month 804) looks at months 624 to 804:
	// exactly 24 contributed months, enough. One month later the window is
	// 625 to 805: 23 months, not enough. One month earlier: 25.
	in := Input{AgeMonths: 600, YearsContributed: 20, MonthlyBase: 3000, ContributeUntilAge: 54}
	for _, c := range []struct {
		retire int
		want   bool
	}{{67*12 - 1, true}, {67 * 12, true}, {67*12 + 1, false}} {
		in.RetireMonths = c.retire
		got := Estimate(in)
		if got.Entitled != c.want {
			t.Errorf("start at month %d: entitled = %v, want %v (%+v)", c.retire, got.Entitled, c.want, got)
		}
		if !c.want && (got.NotEntitledWhy != NotEntitledRecent || got.Monthly != 0) {
			t.Errorf("start at month %d: %+v", c.retire, got)
		}
	}

	// Contributing until a later age fixes it: this is what a convenio
	// especial does for the estimate.
	r = Estimate(Input{AgeMonths: 600, YearsContributed: 13, MonthlyBase: 3000, ContributeUntilAge: 67})
	if !r.Entitled {
		t.Errorf("13 years, until 67: %+v", r)
	}
}

// Art. 208.2, last paragraphs: the legal age is the one the person would have
// had by contributing from the start of the pension up to it.
func TestLegalAgeAssumesContributingUpToIt(t *testing.T) {
	// 62 years old, 36 contributed, until 63, claims at 63: 37 years at 63, and
	// 39 by 65: legal age 65, 24 months early, 21,00 % (fewer than 38y6m
	// contributed at the start). Base reguladora 300/350 x 3.000 = 2.571,43.
	in := Input{AgeMonths: 744, YearsContributed: 36, MonthlyBase: 3000, ContributeUntilAge: 63, RetireAge: 63}
	r := Estimate(in)
	if r.EarlyNotAllowed || r.OrdinaryAge != 65 || r.RetireAge != 63 || r.MonthsAtRetire != 444 {
		t.Fatalf("result = %+v", r)
	}
	near(t, "adjustment", r.Adjustment, -0.21)
	near(t, "monthly", r.Monthly, 300*3000.0/350*0.79)
	near(t, "monthly literal", r.Monthly, 2031.43)

	// Exact limit: claims at 64 (768) with 449 or 450 months. 450 + 12 = 462
	// at month 780 gives legal age 65 (12 months early); 449 + 13 = 462 at
	// month 781, 65y1m (13 months early, 5,87 %). With 420 months 462 comes at
	// month 810, past 67: legal age 67, 36 months early, refused.
	in = Input{AgeMonths: 64 * 12, YearsContributed: 450 / 12.0, MonthlyBase: 3000, ContributeUntilAge: 64, RetireAge: 64}
	r = Estimate(in)
	if r.EarlyNotAllowed || r.OrdinaryAge != 65 || r.MonthsAtRetire != 450 {
		t.Errorf("450 months: %+v", r)
	}
	in.YearsContributed = 449 / 12.0
	r = Estimate(in)
	if r.EarlyNotAllowed || r.OrdinaryMonths != 781 || r.MonthsAtRetire != 449 {
		t.Errorf("449 months: %+v", r)
	}
	near(t, "449 months adjustment", r.Adjustment, -0.0587)
	in.YearsContributed = 420 / 12.0
	r = Estimate(in)
	if !r.EarlyNotAllowed || r.EarlyWhy != EarlyTooEarly || r.OrdinaryAge != 67 || r.RetireAge != 67 {
		t.Errorf("420 months: %+v", r)
	}

	// The assumption only applies to early retirement before 65: a person who
	// claims at 66 is not helped by contributions after the claim.
	r = Estimate(Input{AgeMonths: 744, YearsContributed: 36, MonthlyBase: 3000, ContributeUntilAge: 63, RetireAge: 66})
	if r.OrdinaryAge != 67 || r.EarlyNotAllowed {
		t.Fatalf("66: %+v", r)
	}
	near(t, "66 adjustment", r.Adjustment, -0.055) // 12 months, first column
}

// Art. 208.1.c: the early pension must exceed the minimum pension.
func TestEarlyPensionMustExceedTheMinimum(t *testing.T) {
	// 50 years old, 20 contributed at the minimum base, until 65, claims at 65
	// (24 months before 67) with 420 months: 95,60 % of 300/350 x 1.424,40
	// less 21,00 % is 922,08, under 936,20.
	in := Input{AgeMonths: 600, YearsContributed: 20, MonthlyBase: MinBase, ContributeUntilAge: 65, RetireAge: 65}
	r := Estimate(in)
	if !r.EarlyNotAllowed || r.EarlyWhy != EarlyBelowMinimum || r.RetireAge != 67 || r.OrdinaryAge != 67 {
		t.Fatalf("result = %+v", r)
	}
	// It is computed at the ordinary age instead, with 2 years of gap filled.
	near(t, "monthly at 67", r.Monthly, (276*MinBase+24*MinBase)/350*(0.5+0.0019*240))
	if r.Adjustment != 0 {
		t.Errorf("adjustment = %v", r.Adjustment)
	}

	// A higher base gives a pension above the minimum after the 21,00 %, so the
	// early retirement is allowed.
	in.MonthlyBase = 2000
	r = Estimate(in)
	if r.EarlyNotAllowed || r.RetireAge != 65 {
		t.Fatalf("base 2000: %+v", r)
	}
	near(t, "base 2000 monthly", r.Monthly, 300*2000.0/350*(0.5+0.0019*240)*0.79)
	if r.Monthly <= MinPension {
		t.Errorf("monthly %.2f not above the minimum", r.Monthly)
	}
}

// Contribution periods count complete months (art. 205.1.a, 208.2).
func TestContributedYearsCountCompleteMonths(t *testing.T) {
	for _, c := range []struct {
		years float64
		want  int
	}{{38.5, 462}, {38.46, 461}, {38.4999, 461}, {462.0 / 12, 462}, {0.1 + 0.2, 3}, {12.5, 150}, {15, 180}, {14.99, 179}} {
		if got := completeMonths(c.years); got != c.want {
			t.Errorf("%v years = %d months, want %d", c.years, got, c.want)
		}
	}
	// 38,46 years at 65 (23,46 now, 15 more) is 461 months: legal age 67, not 65.
	in := Input{AgeMonths: 600, YearsContributed: 23.46, MonthlyBase: 3000, ContributeUntilAge: 65}
	if r := Estimate(in); r.OrdinaryAge != 67 || r.RetireAge != 67 {
		t.Errorf("38,46 years: %+v", r)
	}
	in.YearsContributed = 23.5
	if r := Estimate(in); r.OrdinaryAge != 65 || r.MonthsAtRetire != 462 {
		t.Errorf("38,5 years: %+v", r)
	}
}

// Art. 205.1.a: 67, or 65 when 38 years and 6 months are accredited. The
// article does not ask for them on the 65th birthday: someone who reaches
// them between 65 and 67 has the legal age at that point. Base 3.000, 64 years
// old (768 months), 37 years contributed (444 months), working until 66: the
// 462 months are reached at month 786 (65 years and 6 months).
func TestLegalAgeIsReachedBetween65And67(t *testing.T) {
	in := Input{AgeMonths: 768, YearsContributed: 37, MonthlyBase: 3000, ContributeUntilAge: 66}
	full := 300 * 3000.0 / 350 // 2.571,43: all 300 months contributed, 100 %

	for _, c := range []struct {
		name    string
		retire  int
		legal   int
		months  int // contributed at the claim
		reduced float64
	}{
		{"66 years (39 contributed)", 66 * 12, 786, 468, 0},
		{"65y7m", 65*12 + 7, 786, 463, 0},
		{"65y6m (38y6m exactly)", 65*12 + 6, 786, 462, 0},
		{"65y5m (38y5m)", 65*12 + 5, 786, 461, 0.0326}, // fiction: 462 at 65y6m, 1 month early, first column
	} {
		in.RetireMonths = c.retire
		r := Estimate(in)
		if r.EarlyNotAllowed || r.OrdinaryMonths != c.legal || r.MonthsAtRetire != c.months || r.RetireMonths != c.retire {
			t.Errorf("%s: %+v", c.name, r)
			continue
		}
		near(t, c.name+" adjustment", r.Adjustment, -c.reduced)
		near(t, c.name+" monthly", r.Monthly, full*(1-c.reduced))
	}
	in.RetireMonths = 66 * 12
	near(t, "66 years literal", Estimate(in).Monthly, 2571.43)
	in.RetireMonths = 65*12 + 6
	near(t, "65y6m literal", Estimate(in).Monthly, 2571.43)
	in.RetireMonths = 65*12 + 5
	near(t, "65y5m literal", Estimate(in).Monthly, 2487.60)

	// With no age given the pension starts when the right arises.
	in.RetireMonths = 0
	r := Estimate(in)
	if r.RetireMonths != 786 || r.OrdinaryAge != 65 || r.OrdinaryMonths != 786 || r.EarlyNotAllowed {
		t.Errorf("no claim: %+v", r)
	}
	near(t, "no claim", r.Monthly, 2571.43)

	// A claim before 65 still uses art. 208.2: contributing up to 65 is
	// assumed (D3). 62y, 36 contributed, until 63, claim at 63.
	r = Estimate(Input{AgeMonths: 744, YearsContributed: 36, MonthlyBase: 3000, ContributeUntilAge: 63, RetireAge: 63})
	if r.OrdinaryMonths != 780 || r.RetireAge != 63 {
		t.Errorf("before 65: %+v", r)
	}
}

// Art. 210.2.a: 4 % for each complete year contributed between reaching the
// legal age and the claim.
func TestLateBonusStartsAtTheLegalAge(t *testing.T) {
	// Legal age at month 786 (as above), working until 68, claim at 68 (816):
	// 30 months, 2 complete years, 8 %.
	in := Input{AgeMonths: 768, YearsContributed: 37, MonthlyBase: 3000, ContributeUntilAge: 68, RetireAge: 68}
	r := Estimate(in)
	near(t, "bonus", r.Adjustment, 0.08)
	near(t, "monthly", r.Monthly, 300*3000.0/350*1.08)
	// A claim at 67 (804): 18 months, 1 complete year, 4 %.
	in.RetireAge = 67
	near(t, "67", Estimate(in).Adjustment, 0.04)
	// A claim at 66 years 11 months (803): 17 months, 1 year.
	in.RetireAge, in.RetireMonths = 0, 66*12+11
	near(t, "66y11m", Estimate(in).Adjustment, 0.04)
	// 67y4m (808) is 22 months after month 786: 1 year; 67y6m (810) is 24: 2 years.
	in.RetireMonths = 67*12 + 4
	near(t, "67y4m", Estimate(in).Adjustment, 0.04)
	in.RetireMonths = 67*12 + 6
	near(t, "67y6m", Estimate(in).Adjustment, 0.08)

	// No bonus for time without contributions: working until 66, claiming at
	// 68. The 24 months without contributions fill the base reguladora with the
	// minimum base instead.
	in = Input{AgeMonths: 768, YearsContributed: 37, MonthlyBase: 3000, ContributeUntilAge: 66, RetireAge: 68}
	r = Estimate(in)
	if r.Adjustment != 0 || r.MonthsAtRetire != 468 {
		t.Fatalf("no contributions: %+v", r)
	}
	near(t, "no contributions", r.Monthly, (276*3000+24*MinBase)/350)
}

// Art. 208.2 (last paragraphs) also finds a legal age between 65 and 67, for a
// claim before 65 and for one from 65. Base 3.000, 64 years old (768 months).
func TestLegalAgeFictionBetween65And67(t *testing.T) {
	full := 300 * 3000.0 / 350
	for _, c := range []struct {
		name   string
		years  float64 // contributed now
		until  int
		retire int
		legal  int
		months int
		ahead  int
		red    float64
		pct    float64 // percentage for the months contributed
		want   float64
	}{
		// 440 months, claim at 64: 462 at month 790 (65y10m), 22 months, 14,67 %.
		// 440 months give 50 % + 248 x 0,19 % + 12 x 0,18 % = 99,28 %, so the
		// pension is 2.571,43 x 0,9928 x 0,8533, not 2.194,20 (which takes 100 %).
		{"claim 64, 440 months", 440 / 12.0, 70, 64 * 12, 790, 440, 22, 0.1467, 0.5 + 0.0019*248 + 0.0018*12, 2178.40},
		// 444 months, until 66, claim at 65y5m: 461 months, 462 at 65y6m.
		{"claim 65y5m", 444 / 12.0, 66, 65*12 + 5, 786, 461, 1, 0.0326, 1, 2487.60},
		// 444 months, until 70, claim at 65: 456 months, 462 at 65y6m, 6 months, 4,00 %.
		{"claim 65y0m", 444 / 12.0, 70, 65 * 12, 786, 456, 6, 0.04, 1, 2468.57},
	} {
		in := Input{AgeMonths: 768, YearsContributed: c.years, MonthlyBase: 3000, ContributeUntilAge: c.until, RetireMonths: c.retire}
		r := Estimate(in)
		if r.EarlyNotAllowed || !r.Entitled || r.OrdinaryMonths != c.legal || r.MonthsAtRetire != c.months || r.RetireMonths != c.retire {
			t.Errorf("%s: %+v", c.name, r)
			continue
		}
		near(t, c.name+" adjustment", r.Adjustment, -c.red)
		near(t, c.name+" monthly", r.Monthly, full*c.pct*(1-c.red))
		near(t, c.name+" monthly literal", r.Monthly, c.want)
		if got := r.OrdinaryMonths - r.RetireMonths; got != c.ahead {
			t.Errorf("%s: %d months early, want %d", c.name, got, c.ahead)
		}
	}
}

// Art. 210.2.a: the extra percentage is added to the percentage for the months
// contributed and the sum is applied to the base reguladora.
func TestLateBonusIsAddedToThePercentage(t *testing.T) {
	// 60 years old, working until 72 and claiming then. 10 years contributed:
	// 264 months at 72 (legal age 67 at month 804, 60 months later), 5 complete
	// years, 20 %. The base reguladora is 264 x 3.000 / 350 (nothing before the
	// working life). 65,96 % + 20 % = 85,96 %.
	in := Input{AgeMonths: 720, YearsContributed: 10, MonthlyBase: 3000, ContributeUntilAge: 72, RetireAge: 72}
	r := Estimate(in)
	near(t, "adjustment", r.Adjustment, 0.20)
	near(t, "percent", r.Percent, 0.6596)
	near(t, "monthly", r.Monthly, 264*3000.0/350*0.8596)
	near(t, "monthly literal", r.Monthly, 1945.15)
	// 20 years contributed: 88,76 % + 20 % = 108,76 %, over 100 % as art. 210.1.b allows.
	in.YearsContributed = 20
	r = Estimate(in)
	near(t, "108,76 %", r.Monthly, 300*3000.0/350*1.0876)
	near(t, "108,76 % literal", r.Monthly, 2796.69)
}

// Art. 210.2.a: after the second complete year of delay a further period of
// more than 6 and less than 12 months adds 2 %.
func TestLateBonusExtraTwoPercent(t *testing.T) {
	// Legal age at month 786 (as in TestLegalAgeIsReachedBetween65And67), working
	// until 70; the delay in months decides.
	in := Input{AgeMonths: 768, YearsContributed: 37, MonthlyBase: 3000, ContributeUntilAge: 70}
	for _, c := range []struct {
		delay int
		want  float64
	}{
		{11, 0}, {12, 0.04}, {18, 0.04}, {19, 0.04}, // 7 months of the second year: not yet a second complete year
		{23, 0.04}, {24, 0.08}, {30, 0.08}, // 6 months: not more than 6
		{31, 0.10}, {35, 0.10}, {36, 0.12}, {43, 0.14}, {48, 0.16},
	} {
		in.RetireMonths = 786 + c.delay
		near(t, strconv.Itoa(c.delay)+" months", Estimate(in).Adjustment, c.want)
	}
}

// Art. 210.2: the bonus needs the 15 years at the legal age.
func TestLateBonusNeedsFifteenYearsAtTheLegalAge(t *testing.T) {
	// 60 years old, 5 contributed, working until 72: 144 months at 67, 204 at 72.
	r := Estimate(Input{AgeMonths: 720, YearsContributed: 5, MonthlyBase: 3000, ContributeUntilAge: 72, RetireAge: 72})
	if !r.Entitled || r.Adjustment != 0 || r.MonthsAtRetire != 204 {
		t.Fatalf("%+v", r)
	}
	near(t, "monthly", r.Monthly, 204*3000.0/350*(0.5+0.0019*24))
	near(t, "monthly literal", r.Monthly, 954.02)
	// 8 years: 180 months at 67 exactly: the bonus applies.
	r = Estimate(Input{AgeMonths: 720, YearsContributed: 8, MonthlyBase: 3000, ContributeUntilAge: 72, RetireAge: 72})
	if r.Adjustment != 0.20 {
		t.Errorf("180 months at 67: adjustment %v", r.Adjustment)
	}
	r = Estimate(Input{AgeMonths: 720, YearsContributed: 8 - 1/12.0, MonthlyBase: 3000, ContributeUntilAge: 72, RetireAge: 72})
	if r.Adjustment != 0 {
		t.Errorf("179 months at 67: adjustment %v", r.Adjustment)
	}
}

// The cap note depends on whether the reduction came from the DT 34a table.
func TestCapPhasedOnlyBefore2033(t *testing.T) {
	in := Input{AgeMonths: 600, YearsContributed: 27, MonthlyBase: 5101.20, ContributeUntilAge: 63, RetireAge: 63}
	in.CalendarMonth = calendar(2032, in.AgeMonths, 63*12)
	if r := Estimate(in); !r.Capped || !r.CapPhased {
		t.Errorf("2032: %+v", r)
	}
	for _, y := range []int{2033, 2039} {
		in.CalendarMonth = calendar(y, in.AgeMonths, 63*12)
		if r := Estimate(in); !r.Capped || r.CapPhased {
			t.Errorf("%d: %+v", y, r)
		}
	}
}
