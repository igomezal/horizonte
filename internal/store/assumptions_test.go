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
	a.SurplusAccountID = &acc
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetAssumptions(ctx, plan)
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
