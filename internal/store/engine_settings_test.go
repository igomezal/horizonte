package store

import "testing"

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
