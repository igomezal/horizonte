package web

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/igomezal/horizonte/internal/engine"
)

func TestSimRunnerSurvivesPanic(t *testing.T) {
	for name, key := range map[string]string{"shared": "k", "not shareable": ""} {
		t.Run(name, func(t *testing.T) {
			r := newSimRunner(1, 8) // one slot: a leaked one blocks the next run
			v, err := runSim(context.Background(), r, key, func(context.Context) (int, error) { panic("boom") })
			if v != 0 || !errors.Is(err, engine.ErrPanic) {
				t.Fatalf("got %d, %v; want ErrPanic", v, err)
			}
			if len(r.sem) != 0 || len(r.flights) != 0 || r.order.Len() != 0 {
				t.Errorf("slots=%d flights=%d cached=%d, want all 0", len(r.sem), len(r.flights), r.order.Len())
			}
			v, err = runSim(context.Background(), r, key, func(context.Context) (int, error) { return 7, nil })
			if v != 7 || err != nil {
				t.Errorf("after the panic got %d, %v", v, err)
			}
		})
	}
}

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

// windingDown is a calculation whose only caller has left: it has been
// cancelled, and keeps running (holding its slot) until release is closed.
type windingDown struct {
	flight  *simFlight
	release chan struct{}
}

// startAndAbandon runs fn on r under key until its only caller has left. fn
// receives the calculation's context and is expected to block on the release
// channel it is given.
func startAndAbandon(t *testing.T, r *simRunner, key string, fn func(ctx context.Context, release <-chan struct{}) (int, error)) windingDown {
	t.Helper()
	release := make(chan struct{})
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	left := make(chan error, 1)
	go func() {
		_, err := runSim(ctx, r, key, func(ctx context.Context) (int, error) {
			close(started)
			return fn(ctx, release)
		})
		left <- err
	}()
	<-started
	r.mu.Lock()
	f := r.flights[key]
	r.mu.Unlock()
	cancel()
	if err := <-left; !errors.Is(err, context.Canceled) {
		t.Fatalf("the caller that left got %v", err)
	}
	return windingDown{flight: f, release: release}
}

// honoursCancel is a calculation that stops once it is both cancelled and
// released, reporting the cancellation.
func honoursCancel(ctx context.Context, release <-chan struct{}) (int, error) {
	<-ctx.Done()
	<-release
	return 0, ctx.Err()
}

// ignoresCancel is a calculation that finishes normally once released, even
// though it was cancelled.
func ignoresCancel(_ context.Context, release <-chan struct{}) (int, error) {
	<-release
	return 1, nil
}

type simOutcome struct {
	val int
	err error
}

// callSimAsync calls runSim on its own goroutine and returns its outcome.
func callSimAsync(ctx context.Context, r *simRunner, key string, fn func(context.Context) (int, error)) <-chan simOutcome {
	out := make(chan simOutcome, 1)
	go func() {
		v, err := runSim(ctx, r, key, fn)
		out <- simOutcome{v, err}
	}()
	return out
}

// currentFlight returns the calculation running under key, if any, and how
// many callers it has.
func currentFlight(r *simRunner, key string) (*simFlight, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.flights[key]
	if f == nil {
		return nil, 0
	}
	return f, f.waiters
}

// A caller arriving while an abandoned calculation winds down has done nothing
// wrong: it must get a real result, not the other caller's cancellation.
func TestSimRunnerDoesNotJoinACalculationBeingCancelled(t *testing.T) {
	r := newSimRunner(2, 8)
	old := startAndAbandon(t, r, "k", honoursCancel)

	hold := make(chan struct{})
	second := callSimAsync(context.Background(), r, "k", func(context.Context) (int, error) {
		<-hold
		return 42, nil
	})
	waitFor(t, func() bool { _, waiters := currentFlight(r, "k"); return waiters == 1 })

	close(old.release)
	close(hold)
	if got := <-second; got.err != nil || got.val != 42 {
		t.Fatalf("second caller = %d, %v; want 42 and no error", got.val, got.err)
	}
}

// The old calculation winding down must leave its replacement entirely alone.
func TestSimRunnerReplacedCalculationLeavesTheNewOneAlone(t *testing.T) {
	r := newSimRunner(2, 8)
	old := startAndAbandon(t, r, "k", honoursCancel)

	hold := make(chan struct{})
	defer close(hold)
	var calls atomic.Int32
	var newCtx atomic.Pointer[context.Context]
	fn := func(ctx context.Context) (int, error) {
		calls.Add(1)
		newCtx.Store(&ctx)
		<-hold
		return 42, nil
	}
	second := callSimAsync(context.Background(), r, "k", fn)
	waitFor(t, func() bool { f, waiters := currentFlight(r, "k"); return f != old.flight && waiters == 1 })
	replacement, _ := currentFlight(r, "k")
	waitFor(t, func() bool { return calls.Load() == 1 })

	close(old.release)
	<-old.flight.done
	if f, waiters := currentFlight(r, "k"); f != replacement || waiters != 1 {
		t.Fatalf("after the old calculation ended: flight = %p with %d waiters, want %p with 1", f, waiters, replacement)
	}
	if !errors.Is(old.flight.err, context.Canceled) {
		t.Errorf("old calculation err = %v", old.flight.err)
	}
	if _, ok := r.cached("k"); ok {
		t.Error("a cancelled calculation must not be cached")
	}

	// New callers join the replacement, and one of them leaving doesn't stop it.
	thirdCtx, leave := context.WithCancel(context.Background())
	defer leave()
	third := callSimAsync(thirdCtx, r, "k", fn)
	waitFor(t, func() bool { _, waiters := currentFlight(r, "k"); return waiters == 2 })
	leave()
	if got := <-third; !errors.Is(got.err, context.Canceled) {
		t.Errorf("third caller = %d, %v", got.val, got.err)
	}
	if _, waiters := currentFlight(r, "k"); waiters != 1 {
		t.Errorf("waiters = %d, want 1", waiters)
	}
	if err := (*newCtx.Load()).Err(); err != nil {
		t.Errorf("the replacement was cancelled: %v", err)
	}
	if old.flight.waiters != 0 {
		t.Errorf("old calculation's waiters = %d, want 0", old.flight.waiters)
	}

	hold <- struct{}{}
	if got := <-second; got.err != nil || got.val != 42 {
		t.Fatalf("second caller = %d, %v", got.val, got.err)
	}
	if calls.Load() != 1 {
		t.Errorf("replacement ran %d times, want 1", calls.Load())
	}
	if v, ok := cachedSim[int](r, "k"); !ok || v != 42 {
		t.Errorf("cached = %d, %v", v, ok)
	}
	if f, _ := currentFlight(r, "k"); f != nil {
		t.Error("the replacement should have left the map")
	}
}

// A cancelled calculation that finishes anyway may store its result, but what
// the calculation that took over stores is what stays.
func TestSimRunnerReplacedCalculationThatSucceeds(t *testing.T) {
	for name, oldFirst := range map[string]bool{"finishing first": true, "finishing last": false} {
		t.Run(name, func(t *testing.T) {
			r := newSimRunner(2, 8)
			old := startAndAbandon(t, r, "k", ignoresCancel)
			hold := make(chan struct{})
			second := callSimAsync(context.Background(), r, "k", func(context.Context) (int, error) {
				<-hold
				return 2, nil
			})
			waitFor(t, func() bool { f, waiters := currentFlight(r, "k"); return f != old.flight && waiters == 1 })

			if oldFirst {
				close(old.release)
				<-old.flight.done
				if v, ok := cachedSim[int](r, "k"); !ok || v != 1 {
					t.Errorf("cached after the old one ended = %d, %v; want its result", v, ok)
				}
				if f, waiters := currentFlight(r, "k"); f == nil || waiters != 1 {
					t.Errorf("the replacement should still be running: %p, %d waiters", f, waiters)
				}
			}
			close(hold)
			if got := <-second; got.err != nil || got.val != 2 {
				t.Fatalf("second caller = %d, %v", got.val, got.err)
			}
			if !oldFirst {
				close(old.release)
				<-old.flight.done
			}
			if v, ok := cachedSim[int](r, "k"); !ok || v != 2 {
				t.Errorf("cached = %d, %v; want the replacement's 2", v, ok)
			}
		})
	}
}

func TestSimRunnerReplacementWaitsForItsSlot(t *testing.T) {
	r := newSimRunner(1, 8)
	old := startAndAbandon(t, r, "k", honoursCancel)

	started := make(chan struct{})
	second := callSimAsync(context.Background(), r, "k", func(context.Context) (int, error) {
		close(started)
		return 42, nil
	})
	waitFor(t, func() bool { f, waiters := currentFlight(r, "k"); return f != old.flight && waiters == 1 })
	select {
	case <-started:
		t.Fatal("the replacement ran beyond the limit, beside the old calculation")
	case <-time.After(20 * time.Millisecond):
	}

	close(old.release)
	if got := <-second; got.err != nil || got.val != 42 {
		t.Fatalf("second caller = %d, %v", got.val, got.err)
	}
	if len(r.sem) != 0 {
		t.Errorf("slots still held: %d", len(r.sem))
	}
}

// Only meaningful under -race: a cache hit must not read the entry outside the
// lock while the calculation that took over overwrites it.
func TestSimRunnerCacheHitDuringOverwrite(t *testing.T) {
	never := func(context.Context) (int, error) {
		t.Error("the result is cached: nothing should run")
		return 0, nil
	}
	for range 20 {
		r := newSimRunner(2, 8)
		old := startAndAbandon(t, r, "k", ignoresCancel)
		hold := make(chan struct{})
		second := callSimAsync(context.Background(), r, "k", func(context.Context) (int, error) {
			<-hold
			time.Sleep(time.Millisecond) // leaves the hits below time to overlap the overwrite
			return 2, nil
		})
		waitFor(t, func() bool { f, waiters := currentFlight(r, "k"); return f != old.flight && waiters == 1 })
		close(old.release)
		<-old.flight.done

		var hits atomic.Int32
		var stop atomic.Bool
		var wg sync.WaitGroup
		wg.Go(func() {
			for !stop.Load() {
				if v, err := runSim(context.Background(), r, "k", never); err != nil || (v != 1 && v != 2) {
					t.Errorf("cache hit = %d, %v", v, err)
				}
				hits.Add(1)
			}
		})
		waitFor(t, func() bool { return hits.Load() > 0 })
		close(hold)
		if got := <-second; got.err != nil || got.val != 2 {
			t.Errorf("second caller = %d, %v", got.val, got.err)
		}
		stop.Store(true)
		wg.Wait()
	}
}

// A calculation abandoned before it got a slot has no result: it must end with
// its cancellation, leaving nothing behind.
func TestSimRunnerCancelledWhileQueuedLeavesNothing(t *testing.T) {
	r := newSimRunner(1, 8)
	r.sem <- struct{}{} // the only slot is taken

	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	queued := callSimAsync(ctx, r, "k", func(context.Context) (int, error) {
		t.Error("a calculation that never got a slot ran")
		return 0, nil
	})
	waitFor(t, func() bool { _, waiters := currentFlight(r, "k"); return waiters == 1 })
	f, _ := currentFlight(r, "k")

	leave()
	if got := <-queued; !errors.Is(got.err, context.Canceled) {
		t.Errorf("caller = %d, %v", got.val, got.err)
	}
	<-f.done
	if !errors.Is(f.err, context.Canceled) {
		t.Errorf("calculation err = %v, want its cancellation", f.err)
	}
	if v, ok := r.cached("k"); ok {
		t.Fatalf("cached %v for a calculation that never ran", v)
	}
	if got, _ := currentFlight(r, "k"); got != nil {
		t.Error("the calculation should have left the map")
	}
	if len(r.sem) != 1 {
		t.Errorf("slots held = %d, want only the test's", len(r.sem))
	}

	<-r.sem
	if v, err := runSim(context.Background(), r, "k", func(context.Context) (int, error) { return 7, nil }); v != 7 || err != nil {
		t.Errorf("next caller = %d, %v", v, err)
	}
	if len(r.sem) != 0 {
		t.Errorf("slots still held: %d", len(r.sem))
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
