package store

import (
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func TestCostBasisRoundTripAndCopy(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	basis := domain.Cents(0) // zero is a real cost, unlike nil
	id, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Fondo", Kind: domain.AccountInvestment, Balance: 500000, CostBasis: &basis})
	if err != nil {
		t.Fatal(err)
	}
	none, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Otro", Kind: domain.AccountInvestment, Balance: 500000})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetAccount(ctx, plan, id)
	if got.CostBasis == nil || *got.CostBasis != 0 {
		t.Fatalf("saved basis = %v", got.CostBasis)
	}
	if got, _ := s.GetAccount(ctx, plan, none); got.CostBasis != nil {
		t.Fatalf("nil basis became %v", *got.CostBasis)
	}
	updated := domain.Cents(123400)
	got.CostBasis = &updated
	if _, err := s.SaveAccount(ctx, got); err != nil {
		t.Fatal(err)
	}
	cp, err := s.CopyPlan(ctx, plan, "Copia")
	if err != nil {
		t.Fatal(err)
	}
	copies, _ := s.ListAccounts(ctx, cp)
	if len(copies) != 2 || copies[0].CostBasis == nil || *copies[0].CostBasis != 123400 || copies[1].CostBasis != nil {
		t.Errorf("copies = %+v", copies)
	}
}

func TestPrepaymentReserveRoundTrip(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	a, _ := s.GetAssumptions(ctx, plan)
	if a.PrepaymentReserve != 0 {
		t.Fatalf("default reserve = %d", a.PrepaymentReserve)
	}
	a.PrepaymentReserve = 1234500
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssumptions(ctx, plan); got.PrepaymentReserve != 1234500 {
		t.Errorf("saved reserve = %d", got.PrepaymentReserve)
	}
	cp, err := s.CopyPlan(ctx, plan, "Copia")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssumptions(ctx, cp); got.PrepaymentReserve != 1234500 {
		t.Errorf("copied reserve = %d", got.PrepaymentReserve)
	}
}

func TestFreezeIRPFRoundTrip(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	a, _ := s.GetAssumptions(ctx, plan)
	if a.FreezeIRPF {
		t.Fatal("brackets must be indexed by default")
	}
	a.FreezeIRPF = true
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssumptions(ctx, plan); !got.FreezeIRPF {
		t.Error("not saved")
	}
	cp, err := s.CopyPlan(ctx, plan, "Copia")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssumptions(ctx, cp); !got.FreezeIRPF {
		t.Error("not copied")
	}
}
