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
	// Worked by hand: SS 1.944; net 28.056 − 2.000 = 26.056 (no art. 20
	// reduction above 19.747,50); combined tax 2 × 2.991,15 = 5.982,30;
	// minimum 5.550 × 19 % = 1.054,50 → 4.927,80 €.
	b := Compute(Income{Age: 36, Salary: 30000}, FindRegion("general"))
	if !near(b.SocialSecurity, 1944, 1e-6) || !near(b.GeneralBase, 26056, 1e-6) {
		t.Fatalf("breakdown = %+v", b)
	}
	if !near(b.Tax, 4927.80, 0.01) {
		t.Errorf("tax = %.2f, want 4927.80", b.Tax)
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
