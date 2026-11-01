package engine

import (
	"math"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func mcInput() Input {
	a := DefaultAssumptions()
	a.TaxRate = 0
	return Input{
		People: []domain.Person{{ID: 1, Birth: start.AddMonths(-60 * 12), LifeExpectancy: 90}},
		Accounts: []domain.Account{
			{ID: 1, Kind: domain.AccountCash, Balance: 1000000},
			{ID: 2, Kind: domain.AccountInvestment, Balance: 50000000},
		},
		Expenses:    []domain.Expense{{Amount: 200000, PaymentsPerYear: 12}}, // 24.000 €/year on 510.000 €
		Milestones:  []domain.Milestone{{ID: 5, Name: "FI", Kind: domain.MilestoneFI, FIVariant: domain.FIStandard, FIMultiple: 25}},
		Assumptions: a,
		Start:       start,
	}
}

func TestMonteCarloIsReproducibleAndSensible(t *testing.T) {
	in := mcInput()
	s := MCSettings{Runs: 400, Seed: 7, Correlation: 0.6}
	r1 := MonteCarlo(in, DefaultVolatility(), s)
	r2 := MonteCarlo(in, DefaultVolatility(), s)
	if r1.Successes != r2.Successes || r1.NetWorth[10] != r2.NetWorth[10] {
		t.Fatal("same seed must give the same result")
	}
	if rate := r1.SuccessRate(); rate <= 0.3 || rate >= 1 {
		t.Errorf("success rate = %.2f, want something between certain failure and certainty", rate)
	}
	// Percentiles are ordered and the survival curve never rises.
	for y, p := range r1.NetWorth {
		for k := 1; k < len(p); k++ {
			if p[k] < p[k-1] {
				t.Fatalf("year %d percentiles not ordered: %v", r1.Years[y], p)
			}
		}
		if y > 0 && r1.Survival[y] > r1.Survival[y-1]+1e-12 {
			t.Fatalf("survival rises in %d", r1.Years[y])
		}
	}
	if r1.Survival[0] != 1 {
		t.Errorf("everyone has money in the first year: %v", r1.Survival[0])
	}
	if got := r1.Survival[len(r1.Survival)-1]; math.Abs(got-r1.SuccessRate()) > 1e-12 {
		t.Errorf("final survival %v != success rate %v", got, r1.SuccessRate())
	}
	// A different seed gives (slightly) different scenarios.
	r3 := MonteCarlo(in, DefaultVolatility(), MCSettings{Runs: 400, Seed: 8, Correlation: 0.6})
	if r3.NetWorth[10] == r1.NetWorth[10] {
		t.Error("a new seed should change the scenarios")
	}
}

func TestMonteCarloMedianTracksDeterministicPlan(t *testing.T) {
	in := mcInput()
	in.Expenses = nil // pure growth, so the median should match the projection closely
	det := Simulate(in)
	mc := MonteCarlo(in, DefaultVolatility(), MCSettings{Runs: 2000, Seed: 1, Correlation: 0.6})
	y := 20
	detReal := 0.0
	for _, m := range det.Months {
		if m.Month == mc.YearEnd[y] {
			detReal = m.NetWorth / m.Deflator
		}
	}
	if med := mc.NetWorth[y][2]; math.Abs(med-detReal)/detReal > 0.05 {
		t.Errorf("median %.0f vs deterministic %.0f after %d years", med, detReal, y)
	}
	if mc.SuccessRate() != 1 {
		t.Errorf("no spending: every run succeeds, got %v", mc.SuccessRate())
	}
}

func TestMonteCarloZeroVolatilityEqualsDeterministic(t *testing.T) {
	in := mcInput()
	flatVol := map[domain.AccountKind]float64{}
	det := Simulate(in)
	mc := MonteCarlo(in, flatVol, MCSettings{Runs: 20, Seed: 3, Correlation: 0.6})
	wantSuccess := 20
	if det.Failed() {
		wantSuccess = 0
	}
	if mc.Successes != wantSuccess {
		t.Errorf("zero volatility: successes %d, want %d", mc.Successes, wantSuccess)
	}
	p := mc.NetWorth[5]
	if p[0] != p[4] {
		t.Errorf("zero volatility should collapse the fan: %v", p)
	}
	if fi := mc.Milestone[0]; fi.Reached != 0 && fi.Reached != 20 {
		t.Errorf("FI reached in %d of 20 identical runs", fi.Reached)
	}
}
