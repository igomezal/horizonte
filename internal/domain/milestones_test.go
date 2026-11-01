package domain

import "testing"

func TestMilestoneResolutionAndWindow(t *testing.T) {
	ana := Person{ID: 1, Birth: Month{1990, 4}, LifeExpectancy: 90}
	ms := []Milestone{
		{ID: 10, Name: "Jubilación", Kind: MilestoneAge, PersonID: ptr(int64(1)), Age: 65},
		{ID: 11, Name: "Mudanza", Kind: MilestoneDate, Date: Month{2030, 9}},
		{ID: 12, Name: "IF", Kind: MilestoneFI, FIVariant: FICoast, FIMultiple: 25, PersonID: ptr(int64(1)), Age: 50},
	}
	r := ResolveFixed(ms, []Person{ana})
	if r[10] != (Month{2055, 4}) || r[11] != (Month{2030, 9}) {
		t.Fatalf("resolved = %v", r)
	}
	if _, ok := r[12]; ok {
		t.Error("conditional milestone must not resolve without the projection")
	}

	// Salary until retirement ends the month before; pension from retirement starts on it.
	salaryEnd := ptr(int64(10))
	if r.ActiveIn(Month{}, Month{}, nil, salaryEnd, Month{2055, 4}) {
		t.Error("salary should stop in the retirement month")
	}
	if !r.ActiveIn(Month{}, Month{}, nil, salaryEnd, Month{2055, 3}) {
		t.Error("salary should run until the month before retirement")
	}
	if !r.ActiveIn(Month{}, Month{}, salaryEnd, nil, Month{2055, 4}) {
		t.Error("pension should start in the retirement month")
	}
	// Bound to an unreached milestone: never starts, never ends.
	fi := ptr(int64(12))
	if r.ActiveIn(Month{}, Month{}, fi, nil, Month{2080, 1}) {
		t.Error("stream waiting for an unreached milestone must not be active")
	}
	if !r.ActiveIn(Month{}, Month{}, nil, fi, Month{2080, 1}) {
		t.Error("stream ending at an unreached milestone must stay active")
	}
	// Contributions stop in the milestone month.
	a := Account{ContributionEndMS: salaryEnd}
	if !r.ContributingIn(a, Month{2055, 3}) || r.ContributingIn(a, Month{2055, 4}) {
		t.Error("contributions should stop at retirement")
	}
}

func TestMilestoneValidate(t *testing.T) {
	cases := []struct {
		m     Milestone
		field string
	}{
		{Milestone{Name: "x", Kind: MilestoneAge, Age: 65}, "person"},
		{Milestone{Name: "x", Kind: MilestoneAge, PersonID: ptr(int64(1)), Age: 0}, "age"},
		{Milestone{Name: "x", Kind: MilestoneDate}, "date"},
		{Milestone{Name: "x", Kind: MilestoneFI, FIVariant: FIStandard, FIMultiple: 2}, "multiple"},
		{Milestone{Name: "x", Kind: MilestoneFI, FIVariant: "turbo", FIMultiple: 25}, "fi_variant"},
		{Milestone{Name: "x", Kind: MilestoneFI, FIVariant: FILean, FIMultiple: 25}, "fi_amount"},
		{Milestone{Name: "x", Kind: MilestoneFI, FIVariant: FIBarista, FIMultiple: 25}, "fi_amount"},
		{Milestone{Name: "x", Kind: MilestoneFI, FIVariant: FICoast, FIMultiple: 25, Age: 65}, "coast_person"},
		{Milestone{Name: "x", Kind: MilestoneFI, FIVariant: FICoast, FIMultiple: 25, PersonID: ptr(int64(1)), Age: 5}, "coast_age"},
		{Milestone{Name: "x", Kind: "other"}, "kind"},
		{Milestone{Name: "", Kind: MilestoneDate, Date: Month{2030, 1}}, "name"},
	}
	for _, c := range cases {
		if _, ok := c.m.Validate()[c.field]; !ok {
			t.Errorf("%+v: expected error on %s", c.m, c.field)
		}
	}
	ok := Milestone{Name: "IF", Kind: MilestoneFI, FIVariant: FIStandard, FIMultiple: 25}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Errorf("valid milestone has errors: %v", errs)
	}
}
