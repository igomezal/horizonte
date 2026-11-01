package store

import (
	"context"
	"errors"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func TestPrepaymentsCRUD(t *testing.T) {
	s, plan := openTest(t)
	ctx := context.Background()
	debt, _ := s.SaveDebt(ctx, domain.Debt{PlanID: plan, Name: "Hipoteca", Kind: domain.DebtMortgage,
		Balance: 10000000, AnnualRate: 0.03, MonthlyPayment: 60000})

	once, err := s.SavePrepayment(ctx, domain.Prepayment{PlanID: plan, DebtID: debt, Amount: 500000,
		Frequency: domain.PrepayOnce, Mode: domain.ReduceTerm, FeeRate: 0.0025,
		Start: domain.Month{Year: 2028, Month: 6}, End: domain.Month{Year: 2030, Month: 1}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetPrepayment(ctx, plan, once)
	if got.Amount != 500000 || got.FeeRate != 0.0025 || !got.End.IsZero() || got.Start != (domain.Month{Year: 2028, Month: 6}) {
		t.Errorf("one-off prepayment = %+v (end must not be stored)", got)
	}
	if _, err := s.SavePrepayment(ctx, domain.Prepayment{PlanID: plan, DebtID: debt, Amount: 10000,
		Frequency: domain.PrepayMonthly, Mode: domain.ReducePayment}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListPrepayments(ctx, plan); len(list) != 2 {
		t.Errorf("list = %d items", len(list))
	}

	// A debt from nowhere is rejected; deleting the debt removes its prepayments.
	if _, err := s.SavePrepayment(ctx, domain.Prepayment{PlanID: plan, DebtID: 999, Amount: 1,
		Frequency: domain.PrepayMonthly, Mode: domain.ReduceTerm}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown debt err = %v", err)
	}
	if err := s.DeleteDebt(ctx, plan, debt); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListPrepayments(ctx, plan); len(list) != 0 {
		t.Errorf("prepayments survived their debt: %d", len(list))
	}
}

func TestRedirectAccounts(t *testing.T) {
	s, plan := openTest(t)
	ctx := context.Background()
	fund, _ := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Fondo", Kind: domain.AccountInvestment})
	debt, err := s.SaveDebt(ctx, domain.Debt{PlanID: plan, Name: "Coche", Kind: domain.DebtCar,
		Balance: 100000, MonthlyPayment: 10000, RedirectAccountID: &fund})
	if err != nil {
		t.Fatal(err)
	}
	pp, err := s.SavePrepayment(ctx, domain.Prepayment{PlanID: plan, DebtID: debt, Amount: 5000,
		Frequency: domain.PrepayMonthly, Mode: domain.ReduceTerm, RedirectAccountID: &fund})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDebt(ctx, plan, debt); d.RedirectAccountID == nil || *d.RedirectAccountID != fund {
		t.Errorf("debt redirect = %v", d.RedirectAccountID)
	}
	if p, _ := s.GetPrepayment(ctx, plan, pp); p.RedirectAccountID == nil || *p.RedirectAccountID != fund {
		t.Errorf("prepayment redirect = %v", p.RedirectAccountID)
	}
	// Deleting the account resets both to "unallocated".
	if err := s.DeleteAccount(ctx, plan, fund); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDebt(ctx, plan, debt); d.RedirectAccountID != nil {
		t.Error("debt still points at a deleted account")
	}
	bad := int64(999)
	if _, err := s.SaveDebt(ctx, domain.Debt{PlanID: plan, Name: "x", Kind: domain.DebtCar, Balance: 1,
		MonthlyPayment: 1, RedirectAccountID: &bad}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown redirect account err = %v", err)
	}
}
