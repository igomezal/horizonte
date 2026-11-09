package engine

import (
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/tax/es"
)

// salaryPlan is 30 years of a high salary, indexed to inflation, under the
// Spanish tax model, starting in January so every year is a full one.
func salaryPlan(inflation float64, frozen bool) Input {
	first := domain.Month{Year: 2027, Month: 1}
	a := flat()
	a.Inflation, a.TaxModel, a.TaxRegion, a.FreezeIRPF = inflation, TaxES, "general", frozen
	return Input{
		People:      []domain.Person{{ID: 1, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 1}, LifeExpectancy: 67}},
		Accounts:    []domain.Account{{ID: 1, Kind: domain.AccountCash, Balance: 10000000}},
		Incomes:     []domain.Income{{Kind: domain.IncomeSalary, Amount: 700000, PaymentsPerYear: 12}}, // 84.000 €/year
		Assumptions: a,
		Start:       first,
	}
}

func TestFrozenBracketsRaiseTaxInAnInflationaryPlan(t *testing.T) {
	indexed, frozen := Simulate(salaryPlan(0.03, false)), Simulate(salaryPlan(0.03, true))
	if !near(indexed.TaxYears[0].IRPF(), frozen.TaxYears[0].IRPF(), 1e-6) {
		t.Errorf("first year differs: %v vs %v", indexed.TaxYears[0].IRPF(), frozen.TaxYears[0].IRPF())
	}
	y := 25
	if frozen.TaxYears[y].IRPF() <= indexed.TaxYears[y].IRPF()*1.05 {
		t.Errorf("year %d: frozen %v should be well above indexed %v", y, frozen.TaxYears[y].IRPF(), indexed.TaxYears[y].IRPF())
	}
	// Withholding and settlements follow: total money taken is higher.
	var totalIndexed, totalFrozen float64
	for i := range indexed.Months {
		totalIndexed += indexed.Months[i].Tax
		totalFrozen += frozen.Months[i].Tax
	}
	if totalFrozen <= totalIndexed {
		t.Errorf("tax paid: frozen %v, indexed %v", totalFrozen, totalIndexed)
	}
	if frozen.TaxYears[y].Withheld <= indexed.TaxYears[y].Withheld {
		t.Errorf("withholding: frozen %v, indexed %v", frozen.TaxYears[y].Withheld, indexed.TaxYears[y].Withheld)
	}
	// With the withholding rate worked out from the same frozen scale, what
	// is left to settle is small next to the tax itself.
	if s := frozen.TaxYears[y].Settlement; s > frozen.TaxYears[y].IRPF()*0.05 || s < -frozen.TaxYears[y].IRPF()*0.05 {
		t.Errorf("settlement %v against a tax of %v", s, frozen.TaxYears[y].IRPF())
	}
}

func TestSocialSecurityBaseRisesInBothModes(t *testing.T) {
	indexed, frozen := Simulate(salaryPlan(0.03, false)), Simulate(salaryPlan(0.03, true))
	y := 25
	a, b := indexed.TaxYears[y].People[0].Breakdown.SocialSecurity, frozen.TaxYears[y].People[0].Breakdown.SocialSecurity
	if !near(a, b, 1e-6) {
		t.Errorf("yearly Social Security: indexed %v, frozen %v", a, b)
	}
	// Above the base, so the cap is in play and it has grown with prices.
	deflator := indexed.Months[y*12].Deflator
	if want := es.SocialSecurityMaxBase * deflator * es.SocialSecurityRate; !near(b, want, 1e-6) {
		t.Errorf("frozen Social Security = %v, want the inflated cap %v", b, want)
	}
	// What the months charge in Social Security matches too.
	var ssIndexed, ssFrozen float64
	for i := y * 12; i < (y+1)*12; i++ {
		ssIndexed += indexed.Months[i].Tax
		ssFrozen += frozen.Months[i].Tax
	}
	gapIRPF := frozen.TaxYears[y].Withheld - indexed.TaxYears[y].Withheld + frozen.TaxYears[y-1].Settlement - indexed.TaxYears[y-1].Settlement
	if !near(ssFrozen-ssIndexed, gapIRPF, 1e-6) {
		t.Errorf("months differ by %v, IRPF explains %v", ssFrozen-ssIndexed, gapIRPF)
	}
}

func TestDefaultKeepsBracketsIndexed(t *testing.T) {
	if DefaultAssumptions().FreezeIRPF {
		t.Fatal("brackets must be indexed by default")
	}
	res := Simulate(salaryPlan(0.03, false))
	for _, y := range []int{0, 10, 25} {
		deflator := res.Months[y*12].Deflator
		gross := res.TaxYears[y].People[0].Income
		real := es.Income{Age: gross.Age, Salary: gross.Salary / deflator}
		want := es.Compute(real, es.FindRegion("general")).Tax * deflator
		if got := res.TaxYears[y].IRPF(); !near(got, want, 1e-6) {
			t.Errorf("year %d: tax = %v, want %v (scale in today's euros)", y, got, want)
		}
	}
}

func TestFrozenMatchesIndexedWithoutInflation(t *testing.T) {
	a, b := Simulate(salaryPlan(0, false)), Simulate(salaryPlan(0, true))
	if !near(a.Months[len(a.Months)-1].NetWorth, b.Months[len(b.Months)-1].NetWorth, 1e-6) {
		t.Errorf("no inflation: %v vs %v", a.Months[len(a.Months)-1].NetWorth, b.Months[len(b.Months)-1].NetWorth)
	}
}

func TestRegionalMinimumFollowsTheIndexingOption(t *testing.T) {
	// Madrid's minimum behaves like the State's: indexed, it grows with
	// prices; frozen, it stays at its euro amount.
	for _, frozen := range []bool{false, true} {
		in := salaryPlan(0.03, frozen)
		in.Assumptions.TaxRegion = "madrid"
		res := Simulate(in)
		for _, y := range []int{0, 10} {
			deflator := res.Months[y*12].Deflator
			b := res.TaxYears[y].People[0].Breakdown
			wantState, wantRegional := 5550.0, 5956.65
			if !frozen {
				wantState, wantRegional = wantState*deflator, wantRegional*deflator
			}
			if !near(b.Minimum, wantState, 1e-6) || !near(b.RegionalMin, wantRegional, 1e-6) {
				t.Errorf("frozen=%v year %d: minimums %.2f / %.2f, want %.2f / %.2f", frozen, y, b.Minimum, b.RegionalMin, wantState, wantRegional)
			}
		}
	}
}

func TestWorkDeductionThresholdsFollowTheIndexingOption(t *testing.T) {
	// 18.500 € of pay is inside the DA 61 phase-out today. Indexed, the
	// thresholds grow with prices like the art. 20 ones, so the amount is the
	// one for today's euros times the deflator; frozen, the nominal pay meets
	// the fixed thresholds.
	plan := func(frozen bool) Input {
		in := salaryPlan(0.03, frozen)
		in.Incomes[0].Amount = 18500 * 100 / 12
		return in
	}
	indexed, frozen := Simulate(plan(false)), Simulate(plan(true))
	for _, y := range []int{0, 10} {
		deflator := indexed.Months[y*12].Deflator
		in := indexed.TaxYears[y].People[0].Income
		wantIndexed := es.Compute(es.Income{Age: in.Age, Salary: in.Salary / deflator}, es.FindRegion("general")).WorkDeduction * deflator
		if got := indexed.TaxYears[y].People[0].Breakdown.WorkDeduction; !near(got, wantIndexed, 1e-6) || got == 0 {
			t.Errorf("indexed year %d: deduction %.4f, want %.4f", y, got, wantIndexed)
		}
		fin := frozen.TaxYears[y].People[0].Income
		wantFrozen := es.ComputeWithMaxBase(fin, es.FindRegion("general"), es.SocialSecurityMaxBase*deflator).WorkDeduction
		if got := frozen.TaxYears[y].People[0].Breakdown.WorkDeduction; !near(got, wantFrozen, 1e-6) {
			t.Errorf("frozen year %d: deduction %.4f, want %.4f", y, got, wantFrozen)
		}
	}
}
