package web

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// simRunner runs the heavy simulations (Monte Carlo, rolling backtests) so
// that many people opening those pages at once don't bring the server down:
//
//   - Results are cached by their inputs: a plan that hasn't changed is not
//     simulated again (the key includes the start month, so it expires with
//     the calendar).
//   - Identical requests in flight share one calculation.
//   - At most `limit` calculations run at once (each one already uses every
//     CPU core); the rest wait their turn instead of slowing everyone down.
//   - A calculation nobody is waiting for any more (everyone left the page)
//     is cancelled, so abandoned requests don't keep the CPU busy.
type simRunner struct {
	sem chan struct{}

	mu      sync.Mutex
	flights map[string]*simFlight
	cache   map[string]*list.Element // LRU: front = most recently used
	order   *list.List
	max     int
}

type simEntry struct {
	key string
	val any
}

type simFlight struct {
	done    chan struct{}
	val     any
	err     error
	waiters int
	cancel  context.CancelFunc
}

func newSimRunner(limit, cacheSize int) *simRunner {
	return &simRunner{
		sem:     make(chan struct{}, max(1, limit)),
		flights: map[string]*simFlight{},
		cache:   map[string]*list.Element{},
		order:   list.New(),
		max:     max(1, cacheSize),
	}
}

// simKey identifies a calculation by its kind and everything it depends on.
func simKey(kind string, parts ...any) string {
	h := sha256.New()
	h.Write([]byte(kind))
	enc := json.NewEncoder(h)
	for _, p := range parts {
		if err := enc.Encode(p); err != nil {
			// Inputs are plain data and always encode; if one ever doesn't,
			// fall back to a key that is never shared or cached.
			return ""
		}
	}
	return kind + ":" + hex.EncodeToString(h.Sum(nil))
}

// cached returns a finished result without running anything.
func (r *simRunner) cached(key string) (any, bool) {
	if key == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if el, ok := r.cache[key]; ok {
		r.order.MoveToFront(el)
		return el.Value.(*simEntry).val, true
	}
	return nil, false
}

func (r *simRunner) store(key string, val any) {
	if el, ok := r.cache[key]; ok {
		el.Value.(*simEntry).val = val
		r.order.MoveToFront(el)
		return
	}
	r.cache[key] = r.order.PushFront(&simEntry{key: key, val: val})
	for r.order.Len() > r.max {
		last := r.order.Back()
		r.order.Remove(last)
		delete(r.cache, last.Value.(*simEntry).key)
	}
}

// do returns the cached result for key, joins a calculation already running
// for it, or starts fn. It returns ctx's error if the caller gives up first.
func (r *simRunner) do(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	if key == "" { // not shareable: run it directly, still within the limit
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-r.sem }()
		return fn(ctx)
	}

	r.mu.Lock()
	if el, ok := r.cache[key]; ok {
		r.order.MoveToFront(el)
		r.mu.Unlock()
		return el.Value.(*simEntry).val, nil
	}
	f, ok := r.flights[key]
	if !ok {
		// The calculation outlives the request that started it (others may
		// join), but keeps its values (trace, logger) for telemetry.
		fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		f = &simFlight{done: make(chan struct{}), cancel: cancel}
		r.flights[key] = f
		go r.run(fctx, key, f, fn)
	}
	f.waiters++
	r.mu.Unlock()

	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		r.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
		}
		r.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (r *simRunner) run(ctx context.Context, key string, f *simFlight, fn func(context.Context) (any, error)) {
	defer f.cancel()
	select {
	case r.sem <- struct{}{}:
		f.val, f.err = fn(ctx)
		<-r.sem
	case <-ctx.Done():
		f.err = ctx.Err()
	}
	r.mu.Lock()
	delete(r.flights, key)
	if f.err == nil {
		r.store(key, f.val)
	}
	r.mu.Unlock()
	close(f.done)
}

// runSim is the typed form of simRunner.do.
func runSim[T any](ctx context.Context, r *simRunner, key string, fn func(context.Context) (T, error)) (T, error) {
	v, err := r.do(ctx, key, func(ctx context.Context) (any, error) { return fn(ctx) })
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

// cachedSim is the typed form of simRunner.cached.
func cachedSim[T any](r *simRunner, key string) (T, bool) {
	v, ok := r.cached(key)
	if !ok {
		var zero T
		return zero, false
	}
	return v.(T), true
}
