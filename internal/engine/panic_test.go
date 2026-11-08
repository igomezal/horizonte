package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/igomezal/horizonte/internal/history"
)

// simulations are the two parallel simulations, by the name of their hook.
func simulations(t *testing.T) map[string]func() error {
	t.Helper()
	hs, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	in := mcInput()
	return map[string]func() error{
		"monte_carlo": func() error {
			_, err := MonteCarloContext(context.Background(), in, DefaultVolatility(), MCSettings{Runs: 5000, Seed: 1, Correlation: 0.6})
			return err
		},
		"rolling": func() error {
			_, err := RollingContext(context.Background(), in, hs)
			return err
		},
	}
}

func setTestHook(t *testing.T, hook func(point string, in Input)) {
	t.Helper()
	TestHook = hook
	t.Cleanup(func() { TestHook = nil })
}

func TestWorkerPanicReturnsError(t *testing.T) {
	for kind, run := range simulations(t) {
		t.Run(kind+" every item", func(t *testing.T) {
			// Every worker panics on every item: it must keep draining the
			// work channel, or feeding it would block forever.
			setTestHook(t, func(point string, _ Input) {
				if point == kind+".item" {
					panic("boom")
				}
			})
			if err := run(); !errors.Is(err, ErrPanic) {
				t.Errorf("err = %v, want ErrPanic", err)
			}
		})

		t.Run(kind+" one item", func(t *testing.T) {
			var calls atomic.Int32
			setTestHook(t, func(point string, _ Input) {
				if point == kind+".item" && calls.Add(1) == 1 {
					panic("boom")
				}
			})
			if err := run(); !errors.Is(err, ErrPanic) {
				t.Errorf("err = %v, want ErrPanic, not a result with a hole in it", err)
			}
		})

		t.Run(kind+" recovers", func(t *testing.T) {
			TestHook = nil
			if err := run(); err != nil {
				t.Errorf("err = %v after the panics", err)
			}
		})
	}
}

func TestPanicErrorKeepsOnlyTheLocation(t *testing.T) {
	for kind, run := range simulations(t) {
		t.Run(kind, func(t *testing.T) {
			setTestHook(t, func(point string, _ Input) {
				if point == kind+".item" {
					panic("boom")
				}
			})
			var pe *PanicError
			if err := run(); !errors.As(err, &pe) {
				t.Fatalf("err = %v, want a PanicError", err)
			}
			if pe.Error() != ErrPanic.Error() {
				t.Errorf("error text = %q", pe.Error())
			}
			found := false
			frames := runtime.CallersFrames(pe.StackTrace())
			for {
				f, more := frames.Next()
				found = found || strings.HasSuffix(f.Function, "engine.testPoint")
				if !more {
					break
				}
			}
			if !found {
				t.Error("the panicking frame is not in the stack")
			}
		})
	}
}

func TestPanicStopsTheRemainingWork(t *testing.T) {
	var calls atomic.Int32
	setTestHook(t, func(point string, _ Input) {
		calls.Add(1)
		if point == "monte_carlo.item" {
			panic("boom")
		}
	})
	const runs = 100000
	_, err := MonteCarloContext(context.Background(), mcInput(), DefaultVolatility(), MCSettings{Runs: runs, Seed: 1, Correlation: 0.6})
	if !errors.Is(err, ErrPanic) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() >= runs {
		t.Errorf("all %d runs were attempted after the first panic", calls.Load())
	}
}
