package es

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestScaleApply(t *testing.T) {
	// 26.056 € on the state scale: 12.450×9,5 % + 7.750×12 % + 5.856×15 %.
	if got := StateGeneral.Apply(26056); !near(got, 1182.75+930+878.40, 0.01) {
		t.Errorf("state scale = %.2f", got)
	}
	if got := Savings.Apply(10000); !near(got, 6000*0.19+4000*0.21, 1e-9) {
		t.Errorf("savings scale = %.2f", got)
	}
	if got := Savings.Apply(400000); !near(got, 6000*0.19+44000*0.21+150000*0.23+100000*0.27+100000*0.30, 1e-6) {
		t.Errorf("savings top bracket = %.2f", got)
	}
	if StateGeneral.Apply(-5) != 0 {
		t.Error("negative base")
	}
}

func TestSalary30000General(t *testing.T) {
	// Worked by hand: SS 1.950; net 28.050 − 2.000 = 26.050 (no art. 20
	// reduction above 19.747,50); combined tax 2 × 2.990,25 = 5.980,50;
	// minimum 5.550 × 19 % = 1.054,50 → 4.926,00 €.
	b := Compute(Income{Age: 36, Salary: 30000}, FindRegion("general"))
	if !near(b.SocialSecurity, 1950, 1e-6) || !near(b.GeneralBase, 26050, 1e-6) {
		t.Fatalf("breakdown = %+v", b)
	}
	if !near(b.Tax, 4926.00, 0.01) {
		t.Errorf("tax = %.2f, want 4926.00", b.Tax)
	}
}

func TestPensionOver65(t *testing.T) {
	// 20.000 € pension at 70: net 18.000; art. 20 reduction
	// 2.364,34 − 1,14 × 326,48 = 1.992,15 → base 16.007,85;
	// tax 12.450×19 % + 3.557,85×24 % = 3.219,38; minimum 6.700×19 % = 1.273 → 1.946,38 €.
	b := Compute(Income{Age: 70, Pension: 20000}, FindRegion("general"))
	if b.SocialSecurity != 0 {
		t.Error("pensions pay no Social Security")
	}
	if !near(b.Tax, 1946.38, 0.02) {
		t.Errorf("tax = %.2f, want 1946.38", b.Tax)
	}
}

func TestLowIncomePaysNothing(t *testing.T) {
	if b := Compute(Income{Age: 30, Salary: 15000}, FindRegion("general")); b.Tax != 0 {
		t.Errorf("15.000 € salary should pay no IRPF, got %.2f", b.Tax)
	}
}

func TestSavingsAndMinimumSpillover(t *testing.T) {
	// Only savings income: the personal minimum applies to the savings base.
	b := Compute(Income{Age: 50, Savings: 10000}, FindRegion("general"))
	want := Savings.Apply(10000) - Savings.Apply(5550)
	if !near(b.SavingsTax, want, 1e-9) || b.GeneralTax != 0 {
		t.Errorf("savings-only = %+v, want %.2f", b, want)
	}
}

func TestPensionContributionsAndRentals(t *testing.T) {
	base := Compute(Income{Age: 40, Salary: 50000}, FindRegion("general"))
	with := Compute(Income{Age: 40, Salary: 50000, PlanContrib: 3000}, FindRegion("general"))
	// Only 1.500 € is deductible.
	if !near(base.GeneralBase-with.GeneralBase, 1500, 1e-6) {
		t.Errorf("contribution reduction = %.2f", base.GeneralBase-with.GeneralBase)
	}
	r := Compute(Income{Age: 40, Rental: 10000}, FindRegion("general"))
	if !near(r.GeneralBase, 5000, 1e-6) {
		t.Errorf("rental base = %.2f, want 5000 (50 %% reduction)", r.GeneralBase)
	}
}

func TestRegionsDiffer(t *testing.T) {
	in := Income{Age: 40, Salary: 60000}
	madrid, gen, cat := Compute(in, FindRegion("madrid")), Compute(in, FindRegion("general")), Compute(in, FindRegion("cataluna"))
	if !(madrid.Tax < gen.Tax && gen.Tax < cat.Tax) {
		t.Errorf("expected Madrid < general < Cataluña: %.0f %.0f %.0f", madrid.Tax, gen.Tax, cat.Tax)
	}
	if FindRegion("nowhere").Key != "general" {
		t.Error("unknown region should fall back to general")
	}
}

// Expected tax is worked by hand from the published scale: the cumulative
// amount at the last threshold passed plus the rest at that bracket's rate.
func TestAddedRegionScales(t *testing.T) {
	cases := []struct {
		key               string
		at30000, at250000 float64
	}{
		{"andalucia", 3542.00, 51660.00},
		{"aragon", 3536.89, 56593.29},
		{"asturias", 3472.35, 56627.62},
		{"baleares", 3510.00, 53640.00},
		{"canarias", 3370.75, 57039.40},
		{"cantabria", 3290.00, 54458.00},
		{"castilla_la_mancha", 3582.75, 51700.75},
		{"castilla_leon", 3422.50, 49786.28},
		{"extremadura", 3375.50, 56395.50},
		{"galicia", 3441.16, 51529.16},
		{"murcia", 3354.15, 51290.15},
		{"rioja", 3150.30, 58241.90},
		{"valencia", 3394.00, 61789.00},
	}
	for _, c := range cases {
		r := FindRegion(c.key)
		if r.Key != c.key {
			t.Errorf("%s: region not found", c.key)
			continue
		}
		// The published cumulative amounts are rounded, so allow a few cents.
		if got := r.Scale.Apply(30000); !near(got, c.at30000, 0.05) {
			t.Errorf("%s: tax on 30.000 = %.2f, want %.2f", c.key, got, c.at30000)
		}
		if got := r.Scale.Apply(250000); !near(got, c.at250000, 0.05) {
			t.Errorf("%s: tax on 250.000 = %.2f, want %.2f", c.key, got, c.at250000)
		}
	}
}

func TestForalRegionsAreNotScales(t *testing.T) {
	for _, r := range Regions {
		if r.Key == "pais_vasco" || r.Key == "navarra" {
			t.Errorf("%s has a foral system and must not be an ordinary scale", r.Key)
		}
	}
}

// Regional personal minimums. The income is 30.000 € of "other" general-base
// income so that no reduction or Social Security is involved and the general
// base is exactly 30.000 €. The State half is the same in every case:
//
//	State scale on 30.000: 12.450×9,5 % + 7.750×12 % + 9.800×15 % = 3.582,75
//	minus the State minimum at 9,5 %: 5.550 → 527,25, 6.700 → 636,50,
//	8.100 → 769,50.
func TestRegionalMinimums(t *testing.T) {
	state := map[int]float64{
		40: 3582.75 - 5550*0.095,
		70: 3582.75 - 6700*0.095,
		80: 3582.75 - 8100*0.095,
	}
	// Regional scale on 30.000, then the regional minimum taken off at the
	// first bracket's rate (every minimum below is under the first bracket).
	madridScale := 13362.22*0.085 + (19004.63-13362.22)*0.107 + (30000-19004.63)*0.128
	asturiasScale := 12450*0.09 + (17707.20-12450)*0.12 + (30000-17707.20)*0.14
	balearesScale := 10000*0.09 + 8000*0.1125 + 12000*0.1425
	cases := []struct {
		region string
		age    int
		want   float64
	}{
		// Madrid: 5.956,65 / +1.234,26 / +1.502,58 at 8,5 %.
		{"madrid", 40, state[40] + madridScale - 5956.65*0.085},
		{"madrid", 70, state[70] + madridScale - (5956.65+1234.26)*0.085},
		{"madrid", 80, state[80] + madridScale - (5956.65+1234.26+1502.58)*0.085},
		// Asturias: 6.105 / +1.265 / +1.540 at 9 %.
		{"asturias", 40, state[40] + asturiasScale - 6105*0.09},
		{"asturias", 70, state[70] + asturiasScale - (6105+1265)*0.09},
		{"asturias", 80, state[80] + asturiasScale - (6105+1265+1540)*0.09},
		// Illes Balears: the State 5.550, plus 1.265 and 1.540 at 9 %.
		{"baleares", 40, state[40] + balearesScale - 5550*0.09},
		{"baleares", 70, state[70] + balearesScale - (5550+1265)*0.09},
		{"baleares", 80, state[80] + balearesScale - (5550+1265+1540)*0.09},
		// The general region: State amounts on both halves, the same scale twice.
		{"general", 40, 2 * state[40]},
		{"general", 70, 2 * state[70]},
		{"general", 80, 2 * state[80]},
	}
	for _, c := range cases {
		b := Compute(Income{Age: c.age, Other: 30000}, FindRegion(c.region))
		if !near(b.Tax, c.want, 0.005) {
			t.Errorf("%s at %d: tax = %.4f, want %.4f", c.region, c.age, b.Tax, c.want)
		}
	}
	// Spot-check one total against a plain number: Madrid under 65.
	if b := Compute(Income{Age: 40, Other: 30000}, FindRegion("madrid")); !near(b.Tax, 5696.11, 0.01) {
		t.Errorf("Madrid under 65 = %.2f, want 5696.11", b.Tax)
	}
}

func TestMinimumAgeLimits(t *testing.T) {
	// Age is at the end of the year and art. 57 says "superior a 65" and
	// "superior a 75": 65 gets no supplement, 66 does; 75 gets one, 76 both.
	m := FindRegion("madrid")
	for _, c := range []struct {
		age  int
		want float64
	}{{65, 5956.65}, {66, 5956.65 + 1234.26}, {75, 5956.65 + 1234.26}, {76, 5956.65 + 1234.26 + 1502.58}} {
		if got := m.PersonalMinimum(c.age); !near(got, c.want, 1e-9) {
			t.Errorf("Madrid at %d = %.2f, want %.2f", c.age, got, c.want)
		}
	}
	// A region with no minimum of its own falls back on the State amounts.
	if got := FindRegion("aragon").PersonalMinimum(80); got != 8100 {
		t.Errorf("Aragón at 80 = %.2f, want the State 8.100", got)
	}
}

func TestRegionalMinimumWithSavings(t *testing.T) {
	// Madrid, 3.000 € general and 20.000 € savings income, under 65.
	// General: the State minimum (5.550) takes the 3.000 general base and
	// 2.550 spills to savings; Madrid's (5.956,65) takes 3.000 and 2.956,65
	// spills. No general tax is left on either half.
	// Savings half scale on 20.000: 6.000×9,5 % + 14.000×10,5 % = 2.040.
	// State half 2.040 − 2.550×9,5 %; regional half 2.040 − 2.956,65×9,5 %.
	b := Compute(Income{Age: 40, Other: 3000, Savings: 20000}, FindRegion("madrid"))
	if b.GeneralTax != 0 {
		t.Errorf("general tax = %.2f, want 0", b.GeneralTax)
	}
	want := 2040 - 2550*0.095 + 2040 - 2956.65*0.095
	if !near(b.SavingsTax, want, 0.005) || !near(b.SavingsTax, 3556.87, 0.01) {
		t.Errorf("savings tax = %.4f, want %.4f", b.SavingsTax, want)
	}
	// Savings only, Illes Balears at 70: the minimums (6.700 State, 6.815
	// regional) cross into the second savings bracket.
	// State half: 6.000×9,5 % + 700×10,5 % = 643,50; regional: 570 + 815×10,5 % = 655,575.
	b = Compute(Income{Age: 70, Savings: 20000}, FindRegion("baleares"))
	want = 2040 - 643.50 + 2040 - 655.575
	if !near(b.SavingsTax, want, 0.005) || b.GeneralTax != 0 {
		t.Errorf("Baleares savings only = %+v, want savings tax %.4f", b, want)
	}
	// With the State minimum on both halves (the old behaviour) the first case
	// owed 4.080 − 2×2.550×9,5 % = 3.595,50, that is 38,63 € more: 406,65 € at 9,5 %.
	old := Compute(Income{Age: 40, Other: 3000, Savings: 20000}, FindRegion("general"))
	if !near(old.SavingsTax, 3595.50, 0.005) || !near(old.SavingsTax-3556.87, 406.65*0.095, 0.01) {
		t.Errorf("general savings tax = %.4f, want 3595.50", old.SavingsTax)
	}
}

func TestWithholdingUsesStateMinimum(t *testing.T) {
	// The withholding algorithm has State amounts only, so Madrid's higher
	// minimum is left out: 3.146,93 − 5.550×8,5 % on the regional half.
	in := Income{Age: 40, Other: 30000}
	w := ComputeForWithholding(in, FindRegion("madrid"), SocialSecurityMaxBase)
	madridScale := 13362.22*0.085 + (19004.63-13362.22)*0.107 + (30000-19004.63)*0.128
	want := (3582.75 - 5550*0.095) + madridScale - 5550*0.085
	if !near(w.Tax, want, 0.005) || !near(w.RegionalMin, 5550, 1e-9) {
		t.Errorf("withholding view = %.4f / %.2f, want %.4f / 5550", w.Tax, w.RegionalMin, want)
	}
	full := Compute(in, FindRegion("madrid"))
	if !near(w.Tax-full.Tax, (5956.65-5550)*0.085, 0.005) {
		t.Errorf("withholding exceeds the tax by %.4f, want %.4f", w.Tax-full.Tax, 406.65*0.085)
	}
	// The general region is the same either way.
	g := FindRegion("general")
	if a, b := Compute(in, g), ComputeForWithholding(in, g, SocialSecurityMaxBase); a.Tax != b.Tax {
		t.Errorf("general differs: %v vs %v", a.Tax, b.Tax)
	}
}

// DA 61 LIRPF, as raised by RDL 5/2026: 590,89 € up to 17.094 € of gross
// employee income, then 590,89 − 0,2 × (income − 17.094), nothing from
// 20.048,45 €, and nothing when other income exceeds 6.500 €.
func TestWorkDeductionAmounts(t *testing.T) {
	for _, c := range []struct {
		gross, other, want float64
	}{
		{10000, 0, 590.89},
		{17094, 0, 590.89},
		{18500, 0, 590.89 - 0.2*(18500-17094)}, // 309,69
		{20000, 0, 590.89 - 0.2*(20000-17094)}, // 9,69
		{20048.45, 0, 0},                       // 0,2 × 2.954,45 = 590,89
		{20048.46, 0, 0},
		{30000, 0, 0},
		{15000, 6500, 590.89},
		{15000, 6500.01, 0},
		{0, 0, 0},
	} {
		if got := WorkDeduction(c.gross, c.other); !near(got, c.want, 1e-9) {
			t.Errorf("WorkDeduction(%.2f, %.2f) = %.4f, want %.4f", c.gross, c.other, got, c.want)
		}
	}
}

func TestWorkDeductionLimitedByTax(t *testing.T) {
	// 17.000 €: SS 1.105; net 17.000 − 1.105 − 2.000 = 13.895; art. 20 takes
	// 7.302 → base 6.593. Tax 2 × (6.593 − 5.550) × 9,5 % = 198,17. The
	// deduction is 590,89 but cannot exceed the tax, so the tax is 0 and the
	// deduction 198,17.
	b := Compute(Income{Age: 40, Salary: 17000}, FindRegion("general"))
	if !near(b.GeneralTax, 198.17, 0.01) || !near(b.WorkDeduction, 198.17, 0.01) || !near(b.Tax, 0, 1e-9) {
		t.Errorf("breakdown = %+v", b)
	}
	// 15.000 €: base 15.000 − 975 − 2.000 − 7.302 = 4.723, under the minimum:
	// no tax, so no deduction.
	if b := Compute(Income{Age: 40, Salary: 15000}, FindRegion("general")); b.Tax != 0 || b.WorkDeduction != 0 {
		t.Errorf("15.000 € = %+v", b)
	}
}

func TestWorkDeductionInsidePhaseOut(t *testing.T) {
	// 18.500 €: SS 1.202,50; net 15.297,50; art. 20 = 7.302 − 1,75 × 445,50 =
	// 6.522,375 → base 8.775,125. Tax 2 × 3.225,125 × 9,5 % = 612,77375.
	// Deduction 590,89 − 0,2 × 1.406 = 309,69, under the tax → 303,08375.
	b := Compute(Income{Age: 40, Salary: 18500}, FindRegion("general"))
	if !near(b.WorkDeduction, 309.69, 1e-6) || !near(b.Tax, 612.77375-309.69, 1e-4) {
		t.Errorf("breakdown = %+v", b)
	}
	// 20.000 €: SS 1.300; net 16.700; art. 20 = 7.302 − 1,75 × 1.848 = 4.068
	// → base 12.632. Per half 12.450 × 9,5 % + 182 × 12 % − 527,25 = 677,34,
	// so 1.354,68; deduction 9,69.
	b = Compute(Income{Age: 40, Salary: 20000}, FindRegion("general"))
	if !near(b.Tax, 1354.68-9.69, 0.005) || !near(b.WorkDeduction, 9.69, 1e-6) {
		t.Errorf("20.000 € = %+v", b)
	}
	// The deduction comes off the combined tax, so a region changes the tax
	// but not the deduction: Madrid, same income. State half 677,34; regional
	// half 12.632 × 8,5 % − 5.956,65 × 8,5 % = 567,40475.
	m := Compute(Income{Age: 40, Salary: 20000}, FindRegion("madrid"))
	want := 677.34 + 567.40475 - 9.69
	if !near(m.Tax, want, 0.005) || !near(m.WorkDeduction, 9.69, 1e-6) {
		t.Errorf("Madrid 20.000 € = %+v, want tax %.4f", m, want)
	}
}

func TestWorkDeductionAtAndAboveTheUpperLimit(t *testing.T) {
	for _, salary := range []float64{20048.45, 20100, 25000} {
		if b := Compute(Income{Age: 40, Salary: salary}, FindRegion("general")); b.WorkDeduction != 0 {
			t.Errorf("salary %.2f: deduction %.4f, want 0", salary, b.WorkDeduction)
		}
	}
	// 25.000 € pays the plain tax: SS 1.625; net 21.375, no art. 20 reduction;
	// 12.450 × 9,5 % + 7.750 × 12 % + 1.175 × 15 % = 2.289,00 less the
	// minimum 527,25 = 1.761,75 per half.
	if b := Compute(Income{Age: 40, Salary: 25000}, FindRegion("general")); !near(b.Tax, 2*1761.75, 0.01) {
		t.Errorf("25.000 € tax = %.2f, want 3523.50", b.Tax)
	}
}

func TestWorkDeductionPensionersAndOtherIncome(t *testing.T) {
	g := FindRegion("general")
	// A pension alone is not pay for services under an employment
	// relationship: no deduction, however low.
	if b := Compute(Income{Age: 70, Pension: 15000}, g); b.WorkDeduction != 0 {
		t.Errorf("pension alone: deduction %.2f", b.WorkDeduction)
	}
	// Someone who worked part of the year and then retired: 17.000 € of pay
	// and a 6.000 € pension (other income, within 6.500 €), as in the AEAT
	// examples. SS 1.105; work 17.000 − 1.105 + 6.000 − 2.000 = 19.895, above
	// 19.747,50 so no art. 20 reduction. Tax per half 1.182,75 + 7.445 × 12 %
	// − 527,25 = 1.548,90, so 3.097,80. Net pay 13.895 of a 19.895 base:
	// limit 3.097,80 × 13.895 / 19.895 = 2.163,5, above 590,89.
	b := Compute(Income{Age: 40, Salary: 17000, Pension: 6000}, g)
	if !near(b.WorkDeduction, 590.89, 1e-9) || !near(b.Tax, 3097.80-590.89, 0.005) {
		t.Errorf("pay plus small pension = %+v", b)
	}
	// With 7.000 € of pension, other income exceeds 6.500 €: no deduction.
	if b := Compute(Income{Age: 40, Salary: 17000, Pension: 7000}, g); b.WorkDeduction != 0 {
		t.Errorf("pension above the limit: deduction %.2f", b.WorkDeduction)
	}
	// Other income above the limit: 18.500 € of pay and 7.000 € of other
	// general income. No art. 20 reduction either: work 15.297,50, base
	// 22.297,50; per half 1.182,75 + 930 + 2.097,50 × 15 % − 527,25 = 1.900,125.
	b = Compute(Income{Age: 40, Salary: 18500, Other: 7000}, g)
	if b.WorkDeduction != 0 || !near(b.Tax, 3800.25, 0.005) {
		t.Errorf("other income above the limit = %+v", b)
	}
	// Exactly 6.500 € of savings still qualifies.
	if b := Compute(Income{Age: 40, Salary: 18500, Savings: 6500}, g); b.WorkDeduction <= 0 {
		t.Errorf("6.500 € of other income should qualify: %+v", b)
	}
}

func TestWithholdingLeavesOutTheWorkDeduction(t *testing.T) {
	in := Income{Age: 40, Salary: 18500}
	w := ComputeForWithholding(in, FindRegion("general"), SocialSecurityMaxBase)
	if w.WorkDeduction != 0 || !near(w.Tax, 612.77375, 1e-4) {
		t.Errorf("withholding view = %+v", w)
	}
}

// The DA 61 limit is the part of the general plus savings tax that belongs to
// the net pay, so savings tax counts and a missing general base is no obstacle.
func TestWorkDeductionLimitIncludesTheSavingsTax(t *testing.T) {
	g := FindRegion("general")
	// 12.500 € of pay and 6.500 € of savings (exactly the other-income limit).
	// SS 812,50; net 9.687,50; art. 20 takes 7.302 → general base 2.385,50, under
	// the minimum, so no general tax. The minimum leaves 5.550 − 2.385,50 =
	// 3.164,50 for the savings base. Per half: 6.000 × 9,5 % + 500 × 10,5 % =
	// 622,50, less 3.164,50 × 9,5 % = 300,6275 → 321,8725; two halves 643,745.
	// Net pay 9.687,50 is more than the 8.885,50 base, so the whole tax is the
	// limit, above 590,89: the deduction is 590,89 and the tax 52,855.
	b := Compute(Income{Age: 40, Salary: 12500, Savings: 6500}, g)
	if b.GeneralTax != 0 || !near(b.SavingsTax, 643.745, 1e-6) ||
		!near(b.WorkDeduction, 590.89, 1e-9) || !near(b.Tax, 643.745-590.89, 1e-6) {
		t.Errorf("12.500 + 6.500 savings = %+v", b)
	}
	// 15.000 € of pay and 6.000 € of savings. SS 975; net 12.025; art. 20 →
	// general base 4.723; 827 of the minimum is left for savings. Per half
	// 570 − 827 × 9,5 % = 491,435; two halves 982,87. Deduction 590,89.
	b = Compute(Income{Age: 40, Salary: 15000, Savings: 6000}, g)
	if b.GeneralTax != 0 || !near(b.SavingsTax, 982.87, 1e-6) ||
		!near(b.WorkDeduction, 590.89, 1e-9) || !near(b.Tax, 982.87-590.89, 1e-6) {
		t.Errorf("15.000 + 6.000 savings = %+v", b)
	}
}

func TestWorkDeductionWithNoGeneralBase(t *testing.T) {
	// 9.000 € of pay: SS 585; net 6.415, all taken by the art. 20 reduction, so
	// the general base is 0. 6.000 € of savings: the State and regional
	// minimums (5.550) are both used against it. Per half 570 − 5.550 × 9,5 %
	// = 42,75; two halves 85,50. Net pay 6.415 exceeds the 6.000 base, so the
	// limit is the whole 85,50, which binds (590,89 would be allowed): tax 0.
	b := Compute(Income{Age: 40, Salary: 9000, Savings: 6000}, FindRegion("general"))
	if b.GeneralBase != 0 || !near(b.SavingsTax, 85.5, 1e-9) || !near(b.WorkDeduction, 85.5, 1e-9) || !near(b.Tax, 0, 1e-9) {
		t.Errorf("no general base = %+v", b)
	}
}

func TestWorkDeductionLimitUsesTheNetPayShare(t *testing.T) {
	// 8.500 € of pay and 6.500 € of savings. SS 552,50; net 5.947,50, all taken
	// by art. 20, so the general base is 0 and the savings base 6.500. Per
	// half 622,50 − 5.550 × 9,5 % (527,25) = 95,25; two halves 190,50. The net
	// pay is 5.947,50 / 6.500 = 91,5 % of the base, so the limit is
	// 190,50 × 0,915 = 174,3075, below 590,89: deduction 174,3075, tax 16,1925.
	b := Compute(Income{Age: 40, Salary: 8500, Savings: 6500}, FindRegion("general"))
	if !near(b.WorkDeduction, 174.3075, 1e-6) || !near(b.Tax, 190.5-174.3075, 1e-6) {
		t.Errorf("share-limited = %+v", b)
	}
}
