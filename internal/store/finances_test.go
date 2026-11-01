package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func openTest(t *testing.T) (*Store, int64) {
	t.Helper()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	planID, err := s.CurrentPlan(ctx, "Test")
	if err != nil {
		t.Fatal(err)
	}
	return s, planID
}

func TestCurrentPlanIsStable(t *testing.T) {
	s, id := openTest(t)
	again, err := s.CurrentPlan(t.Context(), "Other")
	if err != nil || again != id {
		t.Fatalf("CurrentPlan = %d, %v; want %d", again, err, id)
	}
}

func TestFinancesCRUD(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()

	pid, err := s.SavePerson(ctx, domain.Person{PlanID: plan, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 4}, LifeExpectancy: 90})
	if err != nil {
		t.Fatal(err)
	}

	ret := 0.06
	aid, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Fondo", Kind: domain.AccountInvestment,
		OwnerID: &pid, Balance: 1234567, MonthlyContribution: 30000, AnnualReturn: &ret})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAccount(ctx, plan, aid)
	if err != nil || a.Balance != 1234567 || a.OwnerID == nil || *a.OwnerID != pid || *a.AnnualReturn != 0.06 {
		t.Fatalf("GetAccount = %+v, %v", a, err)
	}

	iid, err := s.SaveIncome(ctx, domain.Income{PlanID: plan, Name: "Nómina", Kind: domain.IncomeSalary,
		OwnerID: &pid, Amount: 200000, PaymentsPerYear: 14, End: domain.Month{Year: 2055, Month: 4}})
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := s.GetIncome(ctx, plan, iid)
	if inc.AnnualGrowth != nil || !inc.Start.IsZero() || inc.End != (domain.Month{Year: 2055, Month: 4}) {
		t.Errorf("GetIncome = %+v", inc)
	}

	if _, err := s.SaveExpense(ctx, domain.Expense{PlanID: plan, Name: "Alquiler", Category: domain.ExpenseHousing,
		Amount: 90000, PaymentsPerYear: 12}); err != nil {
		t.Fatal(err)
	}
	did, err := s.SaveDebt(ctx, domain.Debt{PlanID: plan, Name: "Coche", Kind: domain.DebtCar,
		Balance: 1000000, AnnualRate: 0.05, MonthlyPayment: 25000})
	if err != nil {
		t.Fatal(err)
	}

	// Update.
	a.Balance = 1
	if _, err := s.SaveAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAccount(ctx, plan, aid); got.Balance != 1 {
		t.Errorf("updated balance = %d", got.Balance)
	}

	// Deleting a person makes their records joint.
	if err := s.DeletePerson(ctx, plan, pid); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAccount(ctx, plan, aid); got.OwnerID != nil {
		t.Errorf("owner after person delete = %v, want nil", *got.OwnerID)
	}

	if err := s.DeleteDebt(ctx, plan, did); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDebt(ctx, plan, did); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDebt after delete err = %v", err)
	}
	if list, _ := s.ListExpenses(ctx, plan); len(list) != 1 {
		t.Errorf("ListExpenses = %d items", len(list))
	}
}

func TestPlanScoping(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	res, err := s.DB.Exec(`INSERT INTO plans (name) VALUES ('other')`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()

	id, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Mine", Kind: domain.AccountCash})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAccount(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-plan get err = %v", err)
	}
	if err := s.DeleteAccount(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-plan delete err = %v", err)
	}
	// An owner from another plan is rejected.
	foreign, _ := s.SavePerson(ctx, domain.Person{PlanID: other, Name: "X", Birth: domain.Month{Year: 1980, Month: 1}, LifeExpectancy: 90})
	if _, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Bad", Kind: domain.AccountCash, OwnerID: &foreign}); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign owner err = %v", err)
	}
}
