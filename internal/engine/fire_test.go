package engine

import (
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func fireInput() Input {
	in := Input{
		People: []domain.Person{{ID: 1, Birth: domain.Month{Year: 1990, Month: 1}, LifeExpectancy: 90}},
		Accounts: []domain.Account{
			{ID: 1, Name: "Fondo", Kind: domain.AccountInvestment, Balance: domain.FromEuros(100000)},
		},
		Incomes: []domain.Income{
			{ID: 1, Name: "Sueldo", Kind: domain.IncomeSalary, Amount: domain.FromEuros(4000), PaymentsPerYear: 12},
			{ID: 2, Name: "Alquiler", Kind: domain.IncomeRental, Amount: domain.FromEuros(300), PaymentsPerYear: 12, AnnualGrowth: new(float64)},
			{ID: 3, Name: "Clases", Kind: domain.IncomeSelfEmployed, Amount: domain.FromEuros(200), PaymentsPerYear: 12,
				End: domain.Month{Year: 2028, Month: 6}},
		},
		Expenses: []domain.Expense{{ID: 1, Name: "Vida", Amount: domain.FromEuros(1500), PaymentsPerYear: 12}},
		Milestones: []domain.Milestone{
			{ID: 10, Name: "FIRE", Kind: domain.MilestoneFI, FIVariant: domain.FIStandard, FIMultiple: 25},
			{ID: 11, Name: "Imposible", Kind: domain.MilestoneFI, FIVariant: domain.FIFat, FIMultiple: 100,
				FIAmount: domain.FromEuros(1e7)},
		},
		Assumptions: DefaultAssumptions(),
		Start:       domain.Month{Year: 2026, Month: 11},
	}
	in.Assumptions.TaxModel = TaxFlat
	return in
}

func TestStopWorkAt(t *testing.T) {
	in := fireInput()
	base := Simulate(in)
	hit := base.Milestones[0]
	if !hit.Reached() {
		t.Fatal("FIRE should be reached in the base plan")
	}

	out, changed := StopWorkAt(in, base, 10)
	if !changed {
		t.Fatal("the open-ended salary should stop")
	}
	if s := out.Incomes[0]; s.EndMS == nil || *s.EndMS != 10 {
		t.Errorf("salary end = %v, want the FIRE milestone", s.EndMS)
	}
	if out.Incomes[1].EndMS != nil || out.Incomes[2].EndMS != nil || out.Incomes[2].End != in.Incomes[2].End {
		t.Error("rental income and work that already ends earlier must not change")
	}
	if in.Incomes[0].EndMS != nil {
		t.Error("the original input must not be modified")
	}

	// The new projection matches the plan until FIRE, and has no salary after it.
	res := Simulate(out)
	if got := res.Milestones[0].Month; got != hit.Month {
		t.Errorf("FIRE moved from %v to %v", hit.Month, got)
	}
	for i, m := range res.Months {
		if m.Month.Before(hit.Month) && m.NetWorth != base.Months[i].NetWorth {
			t.Fatalf("%v differs before FIRE", m.Month)
		}
		if !m.Month.Before(hit.Month) && m.Income > 300.01 {
			t.Fatalf("%v: income %.2f after FIRE, want only the rent", m.Month, m.Income)
		}
	}

	if _, changed := StopWorkAt(in, base, 11); changed {
		t.Error("a milestone that is never reached changes nothing")
	}
}
