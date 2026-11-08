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

func TestPropertySaleRoundTrip(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	ms, err := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "Venta", Kind: domain.MilestoneDate, Date: domain.Month{Year: 2040, Month: 3}})
	if err != nil {
		t.Fatal(err)
	}
	// The destination is created after the property, as in a copy.
	home, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Piso", Kind: domain.AccountRealEstate, Balance: 100,
		Sale: &domain.PropertySale{MS: &ms, Share: 0.5, CostRate: 0.05, MainResidence: true}})
	if err != nil {
		t.Fatal(err)
	}
	fund, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Fondo", Kind: domain.AccountInvestment})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetAccount(ctx, plan, home)
	if got.Sale == nil || *got.Sale.MS != ms || got.Sale.Share != 0.5 || got.Sale.CostRate != 0.05 || !got.Sale.MainResidence || got.Sale.DestinationID != nil {
		t.Fatalf("sale = %+v", got.Sale)
	}
	got.Sale.DestinationID = &fund
	if _, err := s.SaveAccount(ctx, got); err != nil {
		t.Fatal(err)
	}

	// A copy points at its own milestone and account.
	cp, err := s.CopyPlan(ctx, plan, "Copia")
	if err != nil {
		t.Fatal(err)
	}
	copies, _ := s.ListAccounts(ctx, cp)
	mss, _ := s.ListMilestones(ctx, cp)
	sale := copies[0].Sale
	if sale == nil || *sale.MS != mss[0].ID || sale.DestinationID == nil || *sale.DestinationID != copies[1].ID || *sale.DestinationID == fund {
		t.Fatalf("copied sale = %+v (accounts %+v)", sale, copies)
	}

	// Deleting the destination goes back to the default.
	if err := s.DeleteAccount(ctx, plan, fund); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetAccount(ctx, plan, home); got.Sale == nil || got.Sale.DestinationID != nil {
		t.Errorf("after deleting the destination: %+v", got.Sale)
	}
	// Deleting the milestone that dates the sale cancels it.
	if err := s.DeleteMilestone(ctx, plan, ms); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetAccount(ctx, plan, home); got.Sale != nil {
		t.Errorf("sale without a date: %+v", got.Sale)
	}
}

func TestPropertySaleRejectsBadLinks(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	pension, _ := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Plan", Kind: domain.AccountPension})
	missing := int64(999)
	month := domain.Month{Year: 2040, Month: 1}
	for _, sale := range []*domain.PropertySale{
		{Month: month, Share: 1, DestinationID: &pension},
		{Month: month, Share: 1, DestinationID: &missing},
		{MS: &missing, Share: 1},
	} {
		if _, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Piso", Kind: domain.AccountRealEstate, Sale: sale}); err == nil {
			t.Errorf("sale %+v accepted", sale)
		}
	}
}
