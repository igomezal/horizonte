package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/igomezal/horizonte/internal/history"
)

func TestSimulationsStopWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := mcInput()

	mc, err := MonteCarloContext(ctx, in, DefaultVolatility(), MCSettings{Runs: 5000, Seed: 1, Correlation: 0.6})
	if !errors.Is(err, context.Canceled) || mc.Runs != 0 {
		t.Errorf("monte carlo: runs=%d err=%v, want no result and context.Canceled", mc.Runs, err)
	}
	hs, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	starts, err := RollingContext(ctx, in, hs)
	if !errors.Is(err, context.Canceled) || starts != nil {
		t.Errorf("rolling: %d starts, err=%v, want none and context.Canceled", len(starts), err)
	}

	// Not cancelled: same result as the plain functions.
	got, err := MonteCarloContext(context.Background(), in, DefaultVolatility(), MCSettings{Runs: 50, Seed: 3, Correlation: 0.6})
	want := MonteCarlo(in, DefaultVolatility(), MCSettings{Runs: 50, Seed: 3, Correlation: 0.6})
	if err != nil || got.Successes != want.Successes || got.Runs != 50 {
		t.Errorf("uncancelled run differs: %+v vs %+v (%v)", got.Successes, want.Successes, err)
	}
}
