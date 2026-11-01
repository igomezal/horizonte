package store

import (
	"context"
	"errors"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

// seedFull creates a plan that uses every kind of link.
func seedFull(t *testing.T, s *Store, plan int64) {
	t.Helper()
	ctx := context.Background()
	must := func(_ int64, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	pid, _ := s.SavePerson(ctx, domain.Person{PlanID: plan, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 4}, LifeExpectancy: 90})
	ret, _ := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "Jubilación", Kind: domain.MilestoneAge, PersonID: &pid, Age: 65})
	cash, _ := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Cuenta", Kind: domain.AccountCash, OwnerID: &pid, Balance: 100})
	fund, _ := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Fondo", Kind: domain.AccountInvestment, ContributionEndMS: &ret})
	must(s.SaveIncome(ctx, domain.Income{PlanID: plan, Name: "Nómina", Kind: domain.IncomeSalary, OwnerID: &pid, Amount: 1, PaymentsPerYear: 14, EndMS: &ret}))
	must(s.SaveExpense(ctx, domain.Expense{PlanID: plan, Name: "Casa", Category: domain.ExpenseHousing, Amount: 1, PaymentsPerYear: 12, StartMS: &ret}))
	debt, _ := s.SaveDebt(ctx, domain.Debt{PlanID: plan, Name: "Hipoteca", Kind: domain.DebtMortgage, Balance: 100, MonthlyPayment: 10, RedirectAccountID: &fund})
	must(s.SavePrepayment(ctx, domain.Prepayment{PlanID: plan, DebtID: debt, Amount: 5, Frequency: domain.PrepayYearly,
		Mode: domain.ReduceTerm, EndMS: &ret, RedirectAccountID: &fund}))
	a, _ := s.GetAssumptions(ctx, plan)
	a.SurplusAccountID, a.Inflation = &cash, 0.031
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
}

func TestCopyPlanRemapsEveryLink(t *testing.T) {
	s, plan := openTest(t)
	ctx := context.Background()
	seedFull(t, s, plan)

	cp, err := s.CopyPlan(ctx, plan, "Jubilarme a los 60")
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := s.PlanCounts(ctx, plan)
	copied, _ := s.PlanCounts(ctx, cp)
	for table, n := range orig {
		if copied[table] != n {
			t.Errorf("%s: copy has %d, original %d", table, copied[table], n)
		}
	}
	p, _ := s.GetPlan(ctx, cp)
	if p.Name != "Jubilarme a los 60" || p.ParentID == nil || *p.ParentID != plan {
		t.Errorf("copied plan = %+v", p)
	}

	// Every link in the copy points inside the copy.
	people, _ := s.ListPeople(ctx, cp)
	ms, _ := s.ListMilestones(ctx, cp)
	accts, _ := s.ListAccounts(ctx, cp)
	debts, _ := s.ListDebts(ctx, cp)
	incomes, _ := s.ListIncomes(ctx, cp)
	expenses, _ := s.ListExpenses(ctx, cp)
	pps, _ := s.ListPrepayments(ctx, cp)
	a, _ := s.GetAssumptions(ctx, cp)
	inCopy := map[int64]bool{}
	for _, x := range people {
		inCopy[x.ID] = true
	}
	for _, x := range ms {
		inCopy[x.ID] = true
	}
	for _, x := range accts {
		inCopy[x.ID] = true
	}
	for _, x := range debts {
		inCopy[x.ID] = true
	}
	check := func(what string, id *int64) {
		t.Helper()
		if id == nil || !inCopy[*id] {
			t.Errorf("%s does not point inside the copy: %v", what, id)
		}
	}
	check("milestone person", ms[0].PersonID)
	check("account owner", accts[0].OwnerID)
	check("contribution end", accts[1].ContributionEndMS)
	check("income owner", incomes[0].OwnerID)
	check("income end", incomes[0].EndMS)
	check("expense start", expenses[0].StartMS)
	check("debt redirect", debts[0].RedirectAccountID)
	check("prepayment end", pps[0].EndMS)
	check("prepayment redirect", pps[0].RedirectAccountID)
	check("surplus account", a.SurplusAccountID)
	if !inCopy[pps[0].DebtID] {
		t.Error("prepayment debt does not point inside the copy")
	}
	if a.Inflation != 0.031 {
		t.Errorf("assumptions not copied: %+v", a)
	}

	// Editing the copy leaves the original alone.
	accts[0].Balance = 999999
	if _, err := s.SaveAccount(ctx, accts[0]); err != nil {
		t.Fatal(err)
	}
	origAccts, _ := s.ListAccounts(ctx, plan)
	if origAccts[0].Balance != 100 {
		t.Errorf("original changed: %d", origAccts[0].Balance)
	}
}

func TestDeleteAndSwitchPlans(t *testing.T) {
	s, plan := openTest(t)
	ctx := context.Background()
	if err := s.DeletePlan(ctx, plan); !errors.Is(err, ErrLastPlan) {
		t.Fatalf("deleting the last plan: %v", err)
	}
	seedFull(t, s, plan)
	cp, _ := s.CopyPlan(ctx, plan, "Copia")
	if err := s.SetCurrentPlan(ctx, cp); err != nil {
		t.Fatal(err)
	}
	if cur, _ := s.CurrentPlan(ctx, "x"); cur != cp {
		t.Fatalf("current = %d, want %d", cur, cp)
	}
	// Deleting the active plan activates another and removes its records.
	if err := s.DeletePlan(ctx, cp); err != nil {
		t.Fatal(err)
	}
	if cur, _ := s.CurrentPlan(ctx, "x"); cur != plan {
		t.Errorf("current after delete = %d, want %d", cur, plan)
	}
	if n, _ := s.PlanCounts(ctx, cp); n["accounts"] != 0 || n["debt_prepayments"] != 0 {
		t.Errorf("records of the deleted plan remain: %v", n)
	}
	if err := s.RenamePlan(ctx, plan, "  Base  "); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetPlan(ctx, plan); p.Name != "Base" {
		t.Errorf("renamed = %q", p.Name)
	}
	if err := s.SetCurrentPlan(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("switch to a missing plan: %v", err)
	}
}
