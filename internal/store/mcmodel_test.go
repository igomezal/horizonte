package store

import (
	"testing"

	"github.com/igomezal/horizonte/internal/engine"
)

func TestMonteCarloModelIsStoredAndCopied(t *testing.T) {
	s, plan := openTest(t)
	ctx := t.Context()

	a := engine.DefaultAssumptions()
	a.MCModel = ""
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssumptions(ctx, plan); got.MCModel != engine.MCParametric {
		t.Errorf("default model = %q", got.MCModel)
	}

	a.MCModel = engine.MCBootstrap
	if err := s.SaveAssumptions(ctx, plan, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssumptions(ctx, plan); got.MCModel != engine.MCBootstrap {
		t.Errorf("saved model = %q", got.MCModel)
	}

	cp, err := s.CopyPlan(ctx, plan, "Copia")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssumptions(ctx, cp); got.MCModel != engine.MCBootstrap {
		t.Errorf("copied model = %q", got.MCModel)
	}
}
