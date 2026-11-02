package web

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSimRunnerCachesAndShares(t *testing.T) {
	r := newSimRunner(2, 8)
	var calls atomic.Int32
	release := make(chan struct{})
	fn := func(ctx context.Context) (int, error) {
		calls.Add(1)
		<-release
		return 42, nil
	}

	// Five identical requests at once share one calculation.
	var wg sync.WaitGroup
	results := make([]int, 5)
	for i := range results {
		wg.Go(func() {
			v, err := runSim(context.Background(), r, "k", fn)
			if err != nil {
				t.Error(err)
			}
			results[i] = v
		})
	}
	waitFor(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.flights["k"] != nil && r.flights["k"].waiters == 5
	})
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("calculation ran %d times, want 1", calls.Load())
	}
	for _, v := range results {
		if v != 42 {
			t.Errorf("result = %d", v)
		}
	}

	// Then it is cached.
	if v, ok := cachedSim[int](r, "k"); !ok || v != 42 {
		t.Errorf("cached = %d, %v", v, ok)
	}
	if v, _ := runSim(context.Background(), r, "k", fn); v != 42 || calls.Load() != 1 {
		t.Error("a cached result should not run again")
	}
}

func TestSimRunnerCancelsWhenEveryoneLeaves(t *testing.T) {
	r := newSimRunner(1, 8)
	stopped := make(chan struct{})
	fn := func(ctx context.Context) (int, error) {
		<-ctx.Done() // a long calculation that honours cancellation
		close(stopped)
		return 0, ctx.Err()
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	errs := make(chan error, 2)
	go func() { _, err := runSim(ctx1, r, "k", fn); errs <- err }()
	waitFor(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.flights["k"] != nil })
	go func() { _, err := runSim(ctx2, r, "k", fn); errs <- err }()
	waitFor(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.flights["k"].waiters == 2 })

	cancel1()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Errorf("first caller err = %v", err)
	}
	select {
	case <-stopped:
		t.Fatal("calculation stopped while someone was still waiting")
	case <-time.After(50 * time.Millisecond):
	}
	cancel2()
	<-errs
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("calculation kept running after everyone left")
	}
	waitFor(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.flights["k"] == nil })
	if _, ok := r.cached("k"); ok {
		t.Error("a cancelled calculation must not be cached")
	}
}

func TestSimRunnerLimitsConcurrency(t *testing.T) {
	r := newSimRunner(2, 8)
	var running, peak atomic.Int32
	fn := func(ctx context.Context) (int, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return 1, nil
	}
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Go(func() { runSim(context.Background(), r, string(rune('a'+i)), fn) })
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Errorf("%d calculations ran at once, want at most 2", peak.Load())
	}
}

func TestSimRunnerEvictsOldest(t *testing.T) {
	r := newSimRunner(1, 2)
	one := func(v int) func(context.Context) (int, error) {
		return func(context.Context) (int, error) { return v, nil }
	}
	runSim(context.Background(), r, "a", one(1))
	runSim(context.Background(), r, "b", one(2))
	cachedSim[int](r, "a") // a is now the most recently used
	runSim(context.Background(), r, "c", one(3))
	if _, ok := r.cached("b"); ok {
		t.Error("b should have been evicted")
	}
	if _, ok := r.cached("a"); !ok {
		t.Error("a was used recently and should stay")
	}
}

func TestSimKeyChangesWithInput(t *testing.T) {
	type in struct{ A int }
	if simKey("mc", in{1}) == simKey("mc", in{2}) || simKey("mc", in{1}) != simKey("mc", in{1}) ||
		simKey("mc", in{1}) == simKey("rolling", in{1}) {
		t.Error("keys must depend on kind and input, and be stable")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}
