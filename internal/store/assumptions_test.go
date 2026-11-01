package store

import (
	"context"
	"errors"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
)

func TestAssumptionsRoundTrip(t *testing.T) {
	s, plan := openTest(t)
	ctx := context.Background()

	got, err := s.GetAssumptions(ctx, plan)
	if err != nil || got.Inflation != engine.DefaultAssumptions().Inflation {
		t.Fatalf("defaults = %+v, %v", got, err)
	}

	acc, _ := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Cash", Kind: domain.AccountCash})
	a := engine.DefaultAssumptions()
	a.Inflation, a.TaxRate = 0.03, 0.25
	a.Returns[domain.AccountInvestment] = 0.07
	a.Volatility[domain.AccountInvestment] = 0.2
	a.MCRuns = 500
	a.SurplusAccountID = &acc
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetAssumptions(ctx, plan)
	if got.Volatility[domain.AccountInvestment] != 0.2 || got.Volatility[domain.AccountPension] != 0.10 || got.MCRuns != 500 {
		t.Errorf("monte carlo settings = %v / %d", got.Volatility, got.MCRuns)
	}
	if got.Inflation != 0.03 || got.TaxRate != 0.25 || got.Returns[domain.AccountInvestment] != 0.07 ||
		got.SurplusAccountID == nil || *got.SurplusAccountID != acc {
		t.Errorf("saved = %+v", got)
	}

	// Deleting the surplus account falls back to the default choice.
	if err := s.DeleteAccount(ctx, plan, acc); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetAssumptions(ctx, plan); got.SurplusAccountID != nil {
		t.Errorf("surplus after delete = %v", *got.SurplusAccountID)
	}

	bad := int64(999)
	a.SurplusAccountID = &bad
	if err := s.SaveAssumptions(ctx, plan, a); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign surplus account err = %v", err)
	}
}

func TestWithdrawalSettingsRoundTrip(t *testing.T) {
	s, plan := openTest(t)
	ctx := context.Background()
	pid, _ := s.SavePerson(ctx, domain.Person{PlanID: plan, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 4}, LifeExpectancy: 90})
	ret, _ := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "Jubilación", Kind: domain.MilestoneAge, PersonID: &pid, Age: 65})

	got, _ := s.GetAssumptions(ctx, plan)
	if got.Withdrawal.Strategy != engine.WithdrawFixed || len(got.Order) != 5 {
		t.Fatalf("defaults = %+v / %v", got.Withdrawal, got.Order)
	}
	a := got
	a.Withdrawal = engine.WithdrawalSettings{Strategy: engine.WithdrawGuardrails, StartMS: &ret, Rate: 0.05, Floor: 0.8, Ceiling: 1.2}
	a.Order = engine.ParseOrder("investment,cash,deposit,other,pension")
	a.PensionAvailableMS = &ret
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetAssumptions(ctx, plan)
	if got.Withdrawal.Strategy != engine.WithdrawGuardrails || *got.Withdrawal.StartMS != ret || got.Withdrawal.Rate != 0.05 ||
		got.Order[0] != domain.AccountInvestment || got.PensionAvailableMS == nil {
		t.Errorf("saved = %+v / %v", got.Withdrawal, got.Order)
	}

	// A copy points at its own milestones.
	cp, err := s.CopyPlan(ctx, plan, "Copia")
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := s.GetAssumptions(ctx, cp)
	ms, _ := s.ListMilestones(ctx, cp)
	if ca.Withdrawal.StartMS == nil || *ca.Withdrawal.StartMS != ms[0].ID || *ca.PensionAvailableMS != ms[0].ID {
		t.Errorf("copy withdrawal links = %v / %v, want %d", ca.Withdrawal.StartMS, ca.PensionAvailableMS, ms[0].ID)
	}

	// Deleting the milestone clears both links.
	if err := s.DeleteMilestone(ctx, plan, ret); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetAssumptions(ctx, plan); got.Withdrawal.StartMS != nil || got.PensionAvailableMS != nil {
		t.Error("links to a deleted milestone remain")
	}
}
