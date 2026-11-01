package store

import (
	"errors"
	"testing"

	"github.com/igomezal/horizonte/internal/domain"
)

func TestMilestonesAndLinks(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	pid, _ := s.SavePerson(ctx, domain.Person{PlanID: plan, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 4}, LifeExpectancy: 90})

	ret, err := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "Jubilación", Kind: domain.MilestoneAge, PersonID: &pid, Age: 65})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "IF", Kind: domain.MilestoneFI, FIMultiple: 25,
		FIVariant: domain.FIStandard, FIAmount: 500, Age: 99, PersonID: &pid}) // stray fields are not stored
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMilestone(ctx, plan, fi)
	if got.PersonID != nil || got.Age != 0 || got.FIMultiple != 25 || got.FIAmount != 0 || got.FIVariant != domain.FIStandard {
		t.Errorf("fi milestone stored stray fields: %+v", got)
	}

	iid, err := s.SaveIncome(ctx, domain.Income{PlanID: plan, Name: "Nómina", Kind: domain.IncomeSalary,
		Amount: 100000, PaymentsPerYear: 14, EndMS: &ret})
	if err != nil {
		t.Fatal(err)
	}
	aid, err := s.SaveAccount(ctx, domain.Account{PlanID: plan, Name: "Fondo", Kind: domain.AccountInvestment, ContributionEndMS: &fi})
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := s.GetIncome(ctx, plan, iid)
	if inc.EndMS == nil || *inc.EndMS != ret || inc.StartMS != nil {
		t.Errorf("income links = %+v", inc)
	}

	// Deleting a milestone unlinks streams.
	if err := s.DeleteMilestone(ctx, plan, ret); err != nil {
		t.Fatal(err)
	}
	if inc, _ = s.GetIncome(ctx, plan, iid); inc.EndMS != nil {
		t.Error("income still linked to a deleted milestone")
	}
	// Deleting the person removes their age milestones.
	m2, _ := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "70", Kind: domain.MilestoneAge, PersonID: &pid, Age: 70})
	if err := s.DeletePerson(ctx, plan, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMilestone(ctx, plan, m2); !errors.Is(err, ErrNotFound) {
		t.Errorf("age milestone survived its person: %v", err)
	}
	if a, _ := s.GetAccount(ctx, plan, aid); a.ContributionEndMS == nil || *a.ContributionEndMS != fi {
		t.Errorf("account link lost: %+v", a)
	}

	// A milestone from another plan cannot be linked.
	res, _ := s.DB.Exec(`INSERT INTO plans (name) VALUES ('other')`)
	other, _ := res.LastInsertId()
	foreign, _ := s.SaveMilestone(ctx, domain.Milestone{PlanID: other, Name: "x", Kind: domain.MilestoneDate, Date: domain.Month{Year: 2030, Month: 1}})
	if _, err := s.SaveIncome(ctx, domain.Income{PlanID: plan, Name: "x", Kind: domain.IncomeOther, Amount: 1, PaymentsPerYear: 12, StartMS: &foreign}); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign milestone link err = %v", err)
	}
}

func TestFIREVariantsStored(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()
	pid, _ := s.SavePerson(ctx, domain.Person{PlanID: plan, Name: "Ana", Birth: domain.Month{Year: 1990, Month: 4}, LifeExpectancy: 90})
	lean, err := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "Lean", Kind: domain.MilestoneFI,
		FIVariant: domain.FILean, FIMultiple: 25, FIAmount: 1800000})
	if err != nil {
		t.Fatal(err)
	}
	coast, err := s.SaveMilestone(ctx, domain.Milestone{PlanID: plan, Name: "Coast", Kind: domain.MilestoneFI,
		FIVariant: domain.FICoast, FIMultiple: 25, PersonID: &pid, Age: 60, FIAmount: 99})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMilestone(ctx, plan, lean); m.FIVariant != domain.FILean || m.FIAmount != 1800000 {
		t.Errorf("lean = %+v", m)
	}
	if m, _ := s.GetMilestone(ctx, plan, coast); m.FIVariant != domain.FICoast || m.Age != 60 ||
		m.PersonID == nil || *m.PersonID != pid || m.FIAmount != 0 {
		t.Errorf("coast = %+v", m)
	}
}
