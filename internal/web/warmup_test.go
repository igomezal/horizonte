package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/history"
)

// planIsWarm reports whether everything the pages look up for a plan is cached.
func planIsWarm(t *testing.T, s *Server, id int64) bool {
	t.Helper()
	in, err := s.loadInput(t.Context(), finCtx{plan: id, now: domain.MonthOf(s.now())})
	if err != nil {
		t.Fatal(err)
	}
	_, mc := cachedSim[engine.MCResult](s.sims, mcKey(in, mcSettings(in, defaultSeed)))
	_, cmp := cachedSim[engine.MCResult](s.sims, mcKey(in, compareSettings()))
	_, roll := cachedSim[[]engine.RollingStart](s.sims, rollingKey(in))
	return mc && cmp && roll
}

// seedPlans creates one seeded active plan and n-1 copies of it, oldest first.
func seedPlans(t *testing.T, s *Server, n int) []int64 {
	t.Helper()
	seedMC(t, s)
	first, err := s.store.CurrentPlan(t.Context(), "x")
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{first}
	for i := 1; i < n; i++ {
		id, err := s.store.CopyPlan(t.Context(), first, fmt.Sprintf("copy %d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// addEmptyPlan creates a plan with no people.
func addEmptyPlan(t *testing.T, s *Server) int64 {
	t.Helper()
	res, err := s.store.DB.ExecContext(t.Context(), `INSERT INTO plans (name) VALUES ('empty')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// holdSimSlots fills the runner's slots so no calculation can start: a
// warm-up then blocks on its first simulation until its context ends.
func holdSimSlots(t *testing.T, s *Server) {
	t.Helper()
	for range cap(s.sims.sem) {
		s.sims.sem <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(s.sims.sem) {
			<-s.sims.sem
		}
	})
}

// dbState is the part of the database a warm-up could plausibly write to.
func dbState(t *testing.T, s *Server) string {
	t.Helper()
	var b strings.Builder
	rows, err := s.store.DB.QueryContext(t.Context(), `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"plans", "people", "plan_assumptions"} {
		var n int
		if err := s.store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s: %d\n", table, n)
	}
	return b.String()
}

// captureEvents binds a Sentry client that records every error event instead
// of sending it, for the duration of the test.
func captureEvents(t *testing.T) func() []*sentry.Event {
	t.Helper()
	var mu sync.Mutex
	var got []*sentry.Event
	err := sentry.Init(sentry.ClientOptions{
		Dsn:       "https://key@o0.ingest.sentry.io/0",
		Transport: nopTransport{},
		BeforeSend: func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
			return nil // recorded; never sent
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return func() []*sentry.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]*sentry.Event(nil), got...)
	}
}

func metricsNamed(ms []capturedMetric, name string) []capturedMetric {
	var out []capturedMetric
	for _, m := range ms {
		if m.name == name {
			out = append(out, m)
		}
	}
	return out
}

// The most important test: if the warm-up's keys ever drift from the
// handlers', the pages stop finding its results and this fails.
func TestWarmFillsExactlyWhatThePagesLookUp(t *testing.T) {
	metrics := captureMetrics(t)
	s := newTestServer(t)
	seedMC(t, s)

	res := s.Warm(t.Context())
	if res.Outcome != warmCompleted || res.Plans != 1 || res.Warmed != 1 || res.Failed != 0 || res.Simulations != 3 {
		t.Fatalf("result = %+v", res)
	}
	if res.Duration <= 0 {
		t.Errorf("duration = %v", res.Duration)
	}
	entries := s.sims.order.Len()
	if entries != 3 {
		t.Fatalf("cache entries = %d, want 3", entries)
	}
	simsBefore := len(metricsNamed(metrics(), "horizonte.simulation.duration"))
	if simsBefore != 3 {
		t.Errorf("simulation duration metrics = %d, want 3", simsBefore)
	}
	for _, m := range metricsNamed(metrics(), "horizonte.simulation.duration") {
		if m.attrs["horizonte.simulation.source"] != warmSource {
			t.Errorf("source = %q", m.attrs["horizonte.simulation.source"])
		}
	}
	if got := metricsNamed(metrics(), "horizonte.simulation.cache"); len(got) != 0 {
		t.Errorf("warm-up touched the cache hit rate: %+v", got)
	}

	for path, pendingMarks := range map[string][]string{
		"/success":   {`hx-trigger="load`},
		"/backtest":  {`hx-get="/backtest/summary"`, `hx-get="/backtest/heat"`},
		"/scenarios": {`/odds"`},
	} {
		rec, body := do(s, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, rec.Code)
		}
		for _, mark := range pendingMarks {
			if strings.Contains(body, mark) {
				t.Errorf("%s still has a pending placeholder (%s)", path, mark)
			}
		}
		if got := s.sims.order.Len(); got != entries {
			t.Errorf("%s: cache entries = %d, want %d", path, got, entries)
		}
		if got := len(s.sims.flights); got != 0 {
			t.Errorf("%s: %d calculations running", path, got)
		}
	}
	if got := len(metricsNamed(metrics(), "horizonte.simulation.duration")); got != simsBefore {
		t.Errorf("the pages ran %d calculations", got-simsBefore)
	}
	for _, m := range metricsNamed(metrics(), "horizonte.simulation.cache") {
		if m.attrs["horizonte.simulation.cache"] != "hit" {
			t.Errorf("a page missed the cache: %+v", m)
		}
	}

	durations := metricsNamed(metrics(), "horizonte.warmup.duration")
	if len(durations) != 1 {
		t.Fatalf("warmup duration metrics = %d, want 1", len(durations))
	}
	m := durations[0]
	if m.unit != sentry.UnitMillisecond || m.attrs["horizonte.warmup.outcome"] != warmCompleted ||
		m.attrs["horizonte.warmup.phase"] != warmPhaseStartup || m.attrs["horizonte.warmup.plans"] != "1" || m.attrs["horizonte.warmup.simulations"] != "3" ||
		m.attrs["horizonte.simulation.workers"] == "" {
		t.Errorf("warmup metric = %+v", m)
	}

	// Everything is cached now: a second warm-up has nothing to calculate.
	if again := s.Warm(t.Context()); again.Outcome != warmCompleted || again.Warmed != 1 || again.Simulations != 0 {
		t.Errorf("second warm-up = %+v", again)
	}
}

func TestWarmDefaultLimit(t *testing.T) {
	if got := newTestServer(t).warmTimeout; got != 60*time.Second {
		t.Errorf("default limit = %v, want 60s", got)
	}
}

func TestWarmComputesCoincidingKeysOnce(t *testing.T) {
	s := newTestServer(t)
	id := seedPlans(t, s, 1)[0]
	a, err := s.store.GetAssumptions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	a.MCRuns = mcRunsCompare
	if err := s.store.SaveAssumptions(t.Context(), id, a); err != nil {
		t.Fatal(err)
	}
	res := s.Warm(t.Context())
	if res.Outcome != warmCompleted || res.Warmed != 1 || res.Simulations != 2 || s.sims.order.Len() != 2 {
		t.Errorf("result = %+v, cache entries = %d", res, s.sims.order.Len())
	}
}

func TestWarmActivePlanFirst(t *testing.T) {
	s := newTestServer(t)
	s.sims = newSimRunner(simConcurrency, warmEntriesPerPlan) // room for one plan
	ids := seedPlans(t, s, 2)
	if err := s.store.SetCurrentPlan(t.Context(), ids[1]); err != nil {
		t.Fatal(err)
	}
	res := s.Warm(t.Context())
	if res.Outcome != warmPartial || res.Warmed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !planIsWarm(t, s, ids[1]) {
		t.Error("the active plan should have been warmed")
	}
	if planIsWarm(t, s, ids[0]) {
		t.Error("the other plan should be cold")
	}
}

func TestWarmEmptyDatabase(t *testing.T) {
	s := newTestServer(t)
	before := dbState(t, s)
	res := s.Warm(t.Context())
	if res.Outcome != warmCompleted || res.Plans != 0 || res.Warmed != 0 || res.Simulations != 0 {
		t.Errorf("result = %+v", res)
	}
	if after := dbState(t, s); after != before {
		t.Errorf("database changed:\n%s\nwant:\n%s", after, before)
	}
}

func TestWarmSkipsPlanWithNoPeople(t *testing.T) {
	s := newTestServer(t)
	seedPlans(t, s, 1)
	addEmptyPlan(t, s)
	res := s.Warm(t.Context())
	if res.Outcome != warmCompleted || res.Plans != 2 || res.Warmed != 1 || res.Failed != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestWarmFailingPlanDoesNotStopTheRest(t *testing.T) {
	s, logs := newLoggedServer(t)
	ids := seedPlans(t, s, 3)
	const corrupt = "not-a-month"
	if _, err := s.store.DB.ExecContext(t.Context(), `UPDATE people SET birth_month = ? WHERE plan_id = ?`, corrupt, ids[1]); err != nil {
		t.Fatal(err)
	}

	res := s.Warm(t.Context())
	if res.Outcome != warmPartial || res.Plans != 3 || res.Warmed != 2 || res.Failed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !planIsWarm(t, s, ids[0]) || !planIsWarm(t, s, ids[2]) {
		t.Error("the healthy plans should be warm")
	}

	level, attrs, ok := logs.find("warmup plan failed")
	if !ok {
		t.Fatalf("no failure log; got:\n%s", logs.dump())
	}
	if level != slog.LevelError || attrs["horizonte.plan.id"] != fmt.Sprint(ids[1]) || attrs["error.type"] == "" ||
		attrs["horizonte.warmup.phase"] != warmPhaseStartup || len(attrs) != 3 {
		t.Errorf("failure log = %v %v", level, attrs)
	}
	if strings.Contains(logs.dump(), corrupt) {
		t.Error("the log repeats the plan's data")
	}
	if level, attrs, ok := logs.find("warmup incomplete"); !ok || level != slog.LevelWarn || attrs["horizonte.warmup.cold"] != "1" {
		t.Errorf("incomplete log = %v %v %v", level, attrs, ok)
	}
	if _, attrs, ok := logs.find("warmup finished"); !ok || attrs["horizonte.warmup.outcome"] != warmPartial || attrs["horizonte.warmup.failed"] != "1" {
		t.Errorf("finished log = %v %v", attrs, ok)
	}
}

func TestWarmPlanFailureIsCapturedInSentry(t *testing.T) {
	events := captureEvents(t)
	s := newTestServer(t)
	id := seedPlans(t, s, 1)[0]
	if _, err := s.store.DB.ExecContext(t.Context(), `UPDATE people SET birth_month = 'not-a-month' WHERE plan_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if res := s.Warm(t.Context()); res.Failed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if got := events(); len(got) != 1 || len(got[0].Exception) == 0 {
		t.Errorf("sentry events = %+v, want one exception", got)
	}
}

func TestWarmTimeoutAndCancelAreNotCapturedInSentry(t *testing.T) {
	events := captureEvents(t)
	s := newTestServer(t)
	seedPlans(t, s, 1)
	holdSimSlots(t, s)
	s.warm(t.Context(), 30*time.Millisecond)
	if got := events(); len(got) != 0 {
		t.Errorf("sentry events = %+v, want none", got)
	}
}

func TestWarmListFailureIsNotReportedAsCold(t *testing.T) {
	events := captureEvents(t)
	s, logs := newLoggedServer(t)
	s.store.DB.Close()

	res := s.Warm(t.Context())
	if res.Outcome != warmPartial || res.Plans != 0 {
		t.Errorf("result = %+v", res)
	}
	if _, _, ok := logs.find("warmup incomplete"); ok {
		t.Error("with no list of plans there is no cold count to report")
	}
	if level, _, ok := logs.find("warmup failed"); !ok || level != slog.LevelError {
		t.Errorf("failed log = %v, %v", level, ok)
	}
	if _, _, ok := logs.find("warmup finished"); !ok {
		t.Error("warmup finished is always logged")
	}
	if got := events(); len(got) != 1 {
		t.Errorf("sentry events = %d, want 1", len(got))
	}
}

func TestWarmPlanLoadPanicBecomesAnError(t *testing.T) {
	s := newTestServer(t)
	s.store = nil // loading the plan dereferences it
	ran, warmed, err := s.warmPlan(t.Context(), 1)
	if !errors.Is(err, engine.ErrPanic) || warmed || ran != 0 {
		t.Errorf("warmPlan = %d, %v, %v; want ErrPanic", ran, warmed, err)
	}
}

// panicFramesIn reports whether an event's stack reaches the code that
// panicked: the engine's injection point and the hook in this file.
func panicFramesIn(e *sentry.Event) bool {
	st := e.Exception[len(e.Exception)-1].Stacktrace
	if st == nil {
		return false
	}
	engineFrame, hookFrame := false, false
	for _, f := range st.Frames {
		engineFrame = engineFrame || f.Function == "testPoint"
		hookFrame = hookFrame || strings.HasSuffix(f.AbsPath, "warmup_test.go")
	}
	return engineFrame && hookFrame
}

func TestSimulationPanicOnRequestIsAServerError(t *testing.T) {
	for _, c := range []struct{ path, point string }{
		{"/success/results", "monte_carlo.item"},
		{"/success/results", "monte_carlo.start"},
		{"/backtest/summary", "rolling.item"},
		{"/scenarios/1/odds", "monte_carlo.item"},
	} {
		t.Run(c.path+" "+c.point, func(t *testing.T) {
			events := captureEvents(t)
			s := newTestServer(t)
			seedMC(t, s)
			engine.TestHook = func(point string, _ engine.Input) {
				if point == c.point {
					panic("boom")
				}
			}
			t.Cleanup(func() { engine.TestHook = nil })

			rec, body := do(s, htmxReq("GET", c.path, nil))
			if rec.Code != http.StatusInternalServerError || body == "" {
				t.Errorf("status = %d, body %d bytes; want a 500 page", rec.Code, len(body))
			}
			if strings.Contains(body, "boom") {
				t.Error("the page repeats the panic's value")
			}
			if got := events(); len(got) != 1 || !panicFramesIn(got[0]) {
				t.Errorf("sentry events = %+v, want one with the panic's stack", got)
			}
		})
	}
}

func TestPersonLeavingIsSilent(t *testing.T) {
	events := captureEvents(t)
	s := newTestServer(t)
	seedMC(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	release := make(chan struct{})
	engine.TestHook = func(point string, _ engine.Input) {
		if point == "monte_carlo.start" {
			cancel() // the person leaves while the simulation runs
			<-release
		}
	}
	t.Cleanup(func() { engine.TestHook = nil })

	rec, body := do(s, htmxReq("GET", "/success/results", nil).WithContext(ctx))
	close(release)
	waitFor(t, func() bool { s.sims.mu.Lock(); defer s.sims.mu.Unlock(); return len(s.sims.flights) == 0 })
	if rec.Code != http.StatusOK || body != "" {
		t.Errorf("status = %d, body = %q; want nothing written", rec.Code, body)
	}
	if got := events(); len(got) != 0 {
		t.Errorf("sentry events = %+v, want none", got)
	}
}

// A panic anywhere in a simulation must come back as an error, leave the
// runner as it found it, and cost only that plan.
func TestSimulationPanic(t *testing.T) {
	const secret = "boom with a person's data"
	hs, err := history.Load()
	if err != nil {
		t.Fatal(err)
	}
	type run struct {
		point string
		call  func(s *Server, ctx context.Context, in engine.Input) error
		key   func(in engine.Input) string
	}
	mc := func(s *Server, ctx context.Context, in engine.Input) error {
		_, err := s.runMonteCarlo(ctx, warmSource, in, mcSettings(in, defaultSeed))
		return err
	}
	mcK := func(in engine.Input) string { return mcKey(in, mcSettings(in, defaultSeed)) }
	rolling := func(s *Server, ctx context.Context, in engine.Input) error {
		_, err := s.runRolling(ctx, warmSource, in, hs)
		return err
	}
	for _, c := range []run{
		{"monte_carlo.start", mc, mcK},
		{"monte_carlo.item", mc, mcK},
		{"rolling.start", rolling, rollingKey},
		{"rolling.item", rolling, rollingKey},
	} {
		t.Run(c.point, func(t *testing.T) {
			events := captureEvents(t)
			s, logs := newLoggedServer(t)
			ids := seedPlans(t, s, 2) // ids[0] is active, so it goes first
			engine.TestHook = func(point string, in engine.Input) {
				if point == c.point && in.People[0].PlanID == ids[0] {
					panic(secret)
				}
			}
			t.Cleanup(func() { engine.TestHook = nil })
			in, err := s.loadInput(t.Context(), finCtx{plan: ids[0], now: domain.MonthOf(s.now())})
			if err != nil {
				t.Fatal(err)
			}

			idle := func(when string) {
				t.Helper()
				if len(s.sims.sem) != 0 || len(s.sims.flights) != 0 {
					t.Errorf("%s: slots=%d flights=%d, want none", when, len(s.sims.sem), len(s.sims.flights))
				}
				if _, ok := s.sims.cached(c.key(in)); ok {
					t.Errorf("%s: a panicked calculation was cached", when)
				}
			}

			if err := c.call(s, t.Context(), in); !errors.Is(err, engine.ErrPanic) {
				t.Fatalf("err = %v, want ErrPanic", err)
			}
			idle("direct run")
			got := events()
			if len(got) != 1 || len(got[0].Exception) == 0 {
				t.Fatalf("sentry events = %+v, want one exception", got)
			}
			if v := got[0].Exception[len(got[0].Exception)-1].Value; strings.Contains(v, "boom") {
				t.Errorf("the event repeats the panic's value: %q", v)
			}
			if !panicFramesIn(got[0]) {
				t.Errorf("the panicking frames are not in the event: %+v", got[0].Exception[len(got[0].Exception)-1].Stacktrace)
			}

			res := s.Warm(t.Context())
			if res.Outcome != warmPartial || res.Plans != 2 || res.Warmed != 1 || res.Failed != 1 {
				t.Fatalf("result = %+v", res)
			}
			idle("warm-up")
			if !planIsWarm(t, s, ids[1]) {
				t.Error("the next plan should have been warmed")
			}
			if _, attrs, ok := logs.find("warmup plan failed"); !ok || attrs["horizonte.plan.id"] != fmt.Sprint(ids[0]) {
				t.Errorf("failure log = %v, %v", attrs, ok)
			}
			if strings.Contains(logs.dump(), "boom") {
				t.Error("the log repeats the panic's value")
			}
			if n := len(events()); n != 2 {
				t.Errorf("sentry events = %d, want one per panic (2)", n)
			}

			// Nothing is left broken: without the panic the plan warms normally.
			engine.TestHook = nil
			if again := s.Warm(t.Context()); again.Outcome != warmCompleted || again.Warmed != 2 || again.Failed != 0 {
				t.Errorf("after the panic = %+v", again)
			}
			if !planIsWarm(t, s, ids[0]) {
				t.Error("the plan should be warm once it stops panicking")
			}
		})
	}
}

func TestWarmTimeoutLeavesPlansCold(t *testing.T) {
	metrics := captureMetrics(t)
	s, logs := newLoggedServer(t)
	t.Cleanup(s.StopWarm)
	seedPlans(t, s, 2)
	holdSimSlots(t, s)

	start := time.Now()
	res := s.warm(t.Context(), 30*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v", elapsed)
	}
	if res.Outcome != warmPartial || res.Plans != 2 || res.Warmed != 0 || res.Failed != 0 {
		t.Errorf("result = %+v", res)
	}
	if _, _, ok := logs.find("warmup plan failed"); ok {
		t.Error("a timeout is not a plan failure")
	}
	if _, attrs, ok := logs.find("warmup incomplete"); !ok || attrs["horizonte.warmup.cold"] != "2" {
		t.Errorf("incomplete log = %v %v", attrs, ok)
	}
	if m, ok := findMetric(metrics(), "horizonte.warmup.duration"); !ok || m.attrs["horizonte.warmup.outcome"] != warmPartial {
		t.Errorf("metric = %+v, %v", m, ok)
	}
}

func TestWarmParentCancelled(t *testing.T) {
	t.Run("before starting", func(t *testing.T) {
		s := newTestServer(t)
		seedPlans(t, s, 1)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if res := s.Warm(ctx); res.Outcome != warmCancelled || res.Warmed != 0 || res.Failed != 0 {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("while simulating", func(t *testing.T) {
		metrics := captureMetrics(t)
		s, logs := newLoggedServer(t)
		seedPlans(t, s, 2)
		holdSimSlots(t, s)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(30*time.Millisecond, cancel)

		start := time.Now()
		res := s.Warm(ctx)
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("took %v", elapsed)
		}
		if res.Outcome != warmCancelled || res.Plans != 2 || res.Warmed != 0 || res.Failed != 0 {
			t.Errorf("result = %+v", res)
		}
		if _, _, ok := logs.find("warmup plan failed"); ok {
			t.Error("a cancellation is not a plan failure")
		}
		if _, _, ok := logs.find("warmup incomplete"); ok {
			t.Error("only a partial warm-up is logged as incomplete")
		}
		if _, attrs, ok := logs.find("warmup finished"); !ok || attrs["horizonte.warmup.outcome"] != warmCancelled {
			t.Errorf("finished log = %v %v", attrs, ok)
		}
		if m, ok := findMetric(metrics(), "horizonte.warmup.duration"); !ok || m.attrs["horizonte.warmup.outcome"] != warmCancelled {
			t.Errorf("metric = %+v, %v", m, ok)
		}
	})
}

func TestWarmCacheLimit(t *testing.T) {
	if got := simCacheSize / warmEntriesPerPlan; got != 85 {
		t.Errorf("plan limit = %d, want 85", got)
	}
	s := newTestServer(t)
	s.sims = newSimRunner(simConcurrency, 2*warmEntriesPerPlan) // room for two plans
	ids := seedPlans(t, s, 3)

	res := s.Warm(t.Context())
	if res.Outcome != warmPartial || res.Plans != 3 || res.Warmed != 2 || res.Failed != 0 {
		t.Fatalf("result = %+v", res)
	}
	if got := s.sims.order.Len(); got != 2*warmEntriesPerPlan {
		t.Errorf("cache entries = %d", got)
	}
	if !planIsWarm(t, s, ids[0]) || !planIsWarm(t, s, ids[1]) {
		t.Error("the plans within the limit should still be cached")
	}
	if planIsWarm(t, s, ids[2]) {
		t.Error("the plan beyond the limit should be cold")
	}
}

func TestWarmDoesNotWrite(t *testing.T) {
	s := newTestServer(t)
	seedPlans(t, s, 2)
	addEmptyPlan(t, s)
	// A saved active plan that no longer exists would make CurrentPlan insert.
	if err := s.store.SetSetting(t.Context(), "current_plan_id", "9999"); err != nil {
		t.Fatal(err)
	}
	before := dbState(t, s)
	if res := s.Warm(t.Context()); res.Warmed != 2 {
		t.Errorf("result = %+v", res)
	}
	if after := dbState(t, s); after != before {
		t.Errorf("database changed:\n%s\nwant:\n%s", after, before)
	}
}

// testWarmLimit is long enough to list the plans, always; what happens after
// that is paced by holdPlans, never by the clock.
const testWarmLimit = 250 * time.Millisecond

// simPacer records the calculations the engine starts and holds a plan's
// first one until the test releases it.
type simPacer struct {
	mu     sync.Mutex
	starts []int64
	gates  map[int64]chan struct{}
}

func (p *simPacer) count(id int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, got := range p.starts {
		if got == id {
			n++
		}
	}
	return n
}

// order returns each plan once, in the order their calculations first started.
func (p *simPacer) order() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []int64
	for i, id := range p.starts {
		if i == 0 || p.starts[i-1] != id {
			out = append(out, id)
		}
	}
	return out
}

func (p *simPacer) release(id int64) {
	p.mu.Lock()
	gate, ok := p.gates[id]
	delete(p.gates, id)
	p.mu.Unlock()
	if ok {
		close(gate)
	}
}

// holdPlans installs an engine hook holding the first calculation of each plan
// in hold. onStart, if set, runs for every calculation and may panic. Cleanup
// lets everything finish before clearing the hook, which goroutines read.
func holdPlans(t *testing.T, s *Server, onStart func(id int64), hold ...int64) *simPacer {
	t.Helper()
	p := &simPacer{gates: map[int64]chan struct{}{}}
	for _, id := range hold {
		p.gates[id] = make(chan struct{})
	}
	engine.TestHook = func(point string, in engine.Input) {
		if point != "monte_carlo.start" && point != "rolling.start" {
			return
		}
		id := in.People[0].PlanID
		p.mu.Lock()
		p.starts = append(p.starts, id)
		gate := p.gates[id]
		p.mu.Unlock()
		if onStart != nil {
			onStart(id)
		}
		if gate != nil {
			<-gate
		}
	}
	t.Cleanup(func() {
		for _, id := range hold {
			p.release(id)
		}
		s.StopWarm()
		// A replaced calculation has left the map but still holds its slot.
		eventually(t, func() bool { return flightWaiters(s) == nil && len(s.sims.sem) == 0 })
		engine.TestHook = nil
	})
	return p
}

// flightWaiters lists how many callers each running calculation has.
func flightWaiters(s *Server) []int {
	s.sims.mu.Lock()
	defer s.sims.mu.Unlock()
	var out []int
	for _, f := range s.sims.flights {
		out = append(out, f.waiters)
	}
	return out
}

// eventually is waitFor for what takes real simulations to come true.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

func warmBackgroundDone(s *Server) <-chan struct{} {
	s.warmBG.mu.Lock()
	defer s.warmBG.mu.Unlock()
	return s.warmBG.done
}

func waitWarmBackground(t *testing.T, s *Server) {
	t.Helper()
	done := warmBackgroundDone(s)
	if done == nil {
		t.Fatal("no background phase started")
	}
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("the background phase did not finish")
	}
}

// warmLogs returns the warm-up's log messages, each with its phase.
func warmLogs(logs *logRecorder) []string {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	var out []string
	for _, r := range logs.records {
		if !strings.HasPrefix(r.Message, "warmup ") {
			continue
		}
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "horizonte.warmup.phase" {
				out = append(out, r.Message+"/"+a.Value.String())
			}
			return true
		})
	}
	return out
}

// findPhase is logRecorder.find limited to one phase of the warm-up.
func findPhase(logs *logRecorder, msg, phase string) (map[string]string, bool) {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	for _, r := range logs.records {
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		if r.Message == msg && attrs["horizonte.warmup.phase"] == phase {
			return attrs, true
		}
	}
	return nil, false
}

func TestWarmBackgroundResumesAtTheInterruptedPlan(t *testing.T) {
	metrics := captureMetrics(t)
	s, logs := newLoggedServer(t)
	ids := seedPlans(t, s, 3)
	pace := holdPlans(t, s, nil, ids[0])

	res := s.warm(t.Context(), testWarmLimit)
	if res.Outcome != warmPartial || res.Plans != 3 || res.Warmed != 0 || res.Failed != 0 {
		t.Fatalf("startup result = %+v", res)
	}

	// The calculation the limit cancelled is still winding down: wait for the
	// background to start over beside it, the worst moment for it to resume.
	eventually(t, func() bool { w := flightWaiters(s); return len(w) == 1 && w[0] == 1 })
	pace.release(ids[0])
	waitWarmBackground(t, s)

	for _, id := range ids {
		if !planIsWarm(t, s, id) {
			t.Errorf("plan %d should be warm", id)
		}
	}
	if got := pace.order(); !slices.Equal(got, ids) {
		t.Errorf("calculation order = %v, want %v", got, ids)
	}
	// Three calculations per plan, and one more for the cancelled attempt.
	if a, b, c := pace.count(ids[0]), pace.count(ids[1]), pace.count(ids[2]); a != 4 || b != 3 || c != 3 {
		t.Errorf("calculations per plan = %d, %d, %d; want 4, 3, 3", a, b, c)
	}

	want := []string{
		"warmup started/startup", "warmup incomplete/startup", "warmup finished/startup",
		"warmup started/background", "warmup finished/background",
	}
	if got := warmLogs(logs); !slices.Equal(got, want) {
		t.Errorf("logs = %v, want %v", got, want)
	}
	if attrs, _ := findPhase(logs, "warmup started", warmPhaseBackground); attrs["horizonte.warmup.plans"] != "3" {
		t.Errorf("background started = %v", attrs)
	}
	attrs, _ := findPhase(logs, "warmup finished", warmPhaseBackground)
	if attrs["horizonte.warmup.outcome"] != warmCompleted || attrs["horizonte.warmup.warmed"] != "3" ||
		attrs["horizonte.warmup.failed"] != "0" {
		t.Errorf("background finished = %v", attrs)
	}

	durations := metricsNamed(metrics(), "horizonte.warmup.duration")
	if len(durations) != 2 {
		t.Fatalf("warmup duration metrics = %d, want 2: %+v", len(durations), durations)
	}
	if m := durations[0]; m.attrs["horizonte.warmup.phase"] != warmPhaseStartup || m.attrs["horizonte.warmup.outcome"] != warmPartial {
		t.Errorf("startup metric = %+v", m)
	}
	if m := durations[1]; m.attrs["horizonte.warmup.phase"] != warmPhaseBackground || m.attrs["horizonte.warmup.outcome"] != warmCompleted {
		t.Errorf("background metric = %+v", m)
	}
	for _, m := range metricsNamed(metrics(), "horizonte.simulation.duration") {
		if m.attrs["horizonte.simulation.source"] != warmSource {
			t.Errorf("source = %q", m.attrs["horizonte.simulation.source"])
		}
	}
	if got := metricsNamed(metrics(), "horizonte.simulation.cache"); len(got) != 0 {
		t.Errorf("the background touched the cache hit rate: %+v", got)
	}
}

func TestWarmStartsNoBackgroundUnlessTheLimitStoppedIt(t *testing.T) {
	expectNone := func(t *testing.T, s *Server, logs *logRecorder) {
		t.Helper()
		if warmBackgroundDone(s) != nil {
			t.Error("a background phase started")
		}
		if _, ok := findPhase(logs, "warmup started", warmPhaseBackground); ok {
			t.Error("the background phase was logged")
		}
	}

	t.Run("completed", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		seedPlans(t, s, 2)
		if res := s.Warm(t.Context()); res.Outcome != warmCompleted {
			t.Errorf("result = %+v", res)
		}
		expectNone(t, s, logs)
	})

	t.Run("parent cancelled", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		seedPlans(t, s, 2)
		holdSimSlots(t, s)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(30*time.Millisecond, cancel)
		if res := s.Warm(ctx); res.Outcome != warmCancelled {
			t.Errorf("result = %+v", res)
		}
		expectNone(t, s, logs)
	})

	t.Run("listing failed", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		s.store.DB.Close()
		if res := s.warm(t.Context(), testWarmLimit); res.Outcome != warmPartial {
			t.Errorf("result = %+v", res)
		}
		expectNone(t, s, logs)
	})

	t.Run("only plans beyond the cache cap are left", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		s.sims = newSimRunner(simConcurrency, warmEntriesPerPlan) // room for one plan
		seedPlans(t, s, 2)
		if res := s.Warm(t.Context()); res.Outcome != warmPartial || res.Warmed != 1 {
			t.Errorf("result = %+v", res)
		}
		expectNone(t, s, logs)
	})
}

func TestWarmBackgroundStaysWithinTheCacheCap(t *testing.T) {
	s, logs := newLoggedServer(t)
	s.sims = newSimRunner(simConcurrency, 2*warmEntriesPerPlan) // room for two plans
	ids := seedPlans(t, s, 3)
	pace := holdPlans(t, s, nil, ids[0])

	s.warm(t.Context(), testWarmLimit)
	eventually(t, func() bool { return len(flightWaiters(s)) == 1 })
	pace.release(ids[0])
	waitWarmBackground(t, s)

	if !planIsWarm(t, s, ids[0]) || !planIsWarm(t, s, ids[1]) {
		t.Error("the plans within the cap should be warm")
	}
	if pace.count(ids[2]) != 0 {
		t.Error("the plan beyond the cap should not be calculated")
	}
	if attrs, _ := findPhase(logs, "warmup started", warmPhaseBackground); attrs["horizonte.warmup.plans"] != "2" {
		t.Errorf("background started = %v", attrs)
	}
}

// warmPhaseRuns runs each phase of the warm-up on a server's plans.
var warmPhaseRuns = []struct {
	phase string
	run   func(ctx context.Context, s *Server, ids []int64)
}{
	{warmPhaseStartup, func(ctx context.Context, s *Server, _ []int64) { s.Warm(ctx) }},
	{warmPhaseBackground, func(ctx context.Context, s *Server, ids []int64) { s.runWarmBackground(ctx, ids) }},
}

// A visitor's comings and goings around a calculation the warm-up needs must
// not cost the plan: whichever of them is first, it ends up properly warmed.
func TestWarmIsUnaffectedByVisitorsLeaving(t *testing.T) {
	type stage struct {
		s       *Server
		id      int64
		pace    *simPacer
		visit   func() <-chan error // a visitor opens the plan; the channel says when it has left
		leave   func()
		runWarm func()
	}
	left := func(t *testing.T, gone <-chan error) {
		t.Helper()
		if err := <-gone; !errors.Is(err, context.Canceled) {
			t.Errorf("visitor err = %v", err)
		}
	}
	waiters := func(s *Server, want int) func() bool {
		return func() bool { w := flightWaiters(s); return len(w) == 1 && w[0] == want }
	}
	for _, c := range []struct {
		name   string
		act    func(t *testing.T, st stage)
		starts int // calculations the engine starts for the plan
	}{
		{
			name: "visitor leaves a calculation the warm-up joined",
			act: func(t *testing.T, st stage) {
				gone := st.visit()
				eventually(t, func() bool { return st.pace.count(st.id) == 1 })
				st.runWarm()
				eventually(t, waiters(st.s, 2))
				st.leave()
				left(t, gone)
				if !waiters(st.s, 1)() {
					t.Errorf("waiters = %v, want the warm-up alone", flightWaiters(st.s))
				}
			},
			starts: 3,
		},
		{
			name: "visitor leaves before the warm-up arrives",
			act: func(t *testing.T, st stage) {
				gone := st.visit()
				eventually(t, func() bool { return st.pace.count(st.id) == 1 })
				st.leave()
				left(t, gone)
				if !waiters(st.s, 0)() {
					t.Fatalf("waiters = %v, want a calculation winding down", flightWaiters(st.s))
				}
				st.runWarm()
				eventually(t, waiters(st.s, 1))
			},
			starts: 4, // the abandoned calculation, and the warm-up's own three
		},
		{
			name: "visitor joins the warm-up's calculation and leaves",
			act: func(t *testing.T, st stage) {
				st.runWarm()
				eventually(t, func() bool { return st.pace.count(st.id) == 1 })
				gone := st.visit()
				eventually(t, waiters(st.s, 2))
				st.leave()
				left(t, gone)
			},
			starts: 3,
		},
	} {
		for _, phase := range warmPhaseRuns {
			t.Run(c.name+"/"+phase.phase, func(t *testing.T) {
				events := captureEvents(t)
				s, logs := newLoggedServer(t)
				ids := seedPlans(t, s, 1)
				id := ids[0]
				pace := holdPlans(t, s, nil, id)
				in, err := s.loadInput(t.Context(), finCtx{plan: id, now: domain.MonthOf(s.now())})
				if err != nil {
					t.Fatal(err)
				}

				testCtx := t.Context()
				ctx, leave := context.WithCancel(testCtx)
				defer leave()
				warmed := make(chan struct{})
				c.act(t, stage{
					s: s, id: id, pace: pace, leave: leave,
					visit: func() <-chan error {
						gone := make(chan error, 1)
						go func() {
							_, err := s.runMonteCarlo(ctx, "test", in, mcSettings(in, defaultSeed))
							gone <- err
						}()
						return gone
					},
					runWarm: func() {
						go func() {
							defer close(warmed)
							phase.run(testCtx, s, ids)
						}()
					},
				})
				pace.release(id)
				<-warmed

				attrs, ok := findPhase(logs, "warmup finished", phase.phase)
				if !ok || attrs["horizonte.warmup.outcome"] != warmCompleted || attrs["horizonte.warmup.warmed"] != "1" ||
					attrs["horizonte.warmup.failed"] != "0" || attrs["horizonte.warmup.simulations"] != "3" {
					t.Errorf("finished = %v, %v", attrs, ok)
				}
				if _, ok := findPhase(logs, "warmup incomplete", phase.phase); ok {
					t.Error("nothing was left cold")
				}
				if _, _, ok := logs.find("warmup plan failed"); ok {
					t.Error("a visitor leaving is not a plan failure")
				}
				if !planIsWarm(t, s, id) {
					t.Error("the plan should be warm")
				}
				if got := pace.count(id); got != c.starts {
					t.Errorf("calculations = %d, want %d", got, c.starts)
				}
				if got := events(); len(got) != 0 {
					t.Errorf("sentry events = %+v, want none", got)
				}
			})
		}
	}
}

func corruptPlan(t *testing.T, s *Server, id int64) {
	t.Helper()
	if _, err := s.store.DB.ExecContext(t.Context(), `UPDATE people SET birth_month = 'not-a-month' WHERE plan_id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func TestWarmBackgroundDoesNotRetryAPlanThatFailedAtStartup(t *testing.T) {
	s, logs := newLoggedServer(t)
	ids := seedPlans(t, s, 3)
	corruptPlan(t, s, ids[0])
	pace := holdPlans(t, s, nil, ids[1])

	if res := s.warm(t.Context(), testWarmLimit); res.Failed != 1 || res.Warmed != 0 {
		t.Fatalf("startup result = %+v", res)
	}
	eventually(t, func() bool { return len(flightWaiters(s)) == 1 })
	pace.release(ids[1])
	waitWarmBackground(t, s)

	if _, ok := findPhase(logs, "warmup plan failed", warmPhaseBackground); ok {
		t.Error("the failed plan was tried again")
	}
	attrs, _ := findPhase(logs, "warmup finished", warmPhaseBackground)
	if attrs["horizonte.warmup.plans"] != "2" || attrs["horizonte.warmup.warmed"] != "2" || attrs["horizonte.warmup.outcome"] != warmCompleted {
		t.Errorf("background finished = %v", attrs)
	}
}

func TestStopWarm(t *testing.T) {
	// startBlocked leaves a background phase waiting for a simulation slot.
	startBlocked := func(t *testing.T) (*Server, *logRecorder) {
		t.Helper()
		s, logs := newLoggedServer(t)
		t.Cleanup(s.StopWarm)
		seedPlans(t, s, 2)
		holdSimSlots(t, s)
		s.warm(t.Context(), 30*time.Millisecond)
		if warmBackgroundDone(s) == nil {
			t.Fatal("no background phase started")
		}
		return s, logs
	}

	t.Run("with no background phase", func(t *testing.T) {
		s := newTestServer(t)
		s.StopWarm()
		s.StopWarm()
	})

	t.Run("cancels promptly and waits for the exit", func(t *testing.T) {
		metrics := captureMetrics(t)
		before := runtime.NumGoroutine()
		s, logs := startBlocked(t)

		start := time.Now()
		s.StopWarm()
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("took %v", elapsed)
		}
		select {
		case <-warmBackgroundDone(s):
		default:
			t.Fatal("StopWarm returned before the goroutine exited")
		}
		s.StopWarm() // idempotent

		attrs, ok := findPhase(logs, "warmup finished", warmPhaseBackground)
		if !ok || attrs["horizonte.warmup.outcome"] != warmCancelled || attrs["horizonte.warmup.failed"] != "0" {
			t.Errorf("background finished = %v, %v", attrs, ok)
		}
		if _, ok := findPhase(logs, "warmup plan failed", warmPhaseBackground); ok {
			t.Error("a cancellation is not a plan failure")
		}
		durations := metricsNamed(metrics(), "horizonte.warmup.duration")
		if len(durations) != 2 || durations[1].attrs["horizonte.warmup.outcome"] != warmCancelled {
			t.Errorf("duration metrics = %+v", durations)
		}

		eventually(t, func() bool { return flightWaiters(s) == nil })
		// The database and the server's own goroutines are new since "before":
		// close the database, and everything the warm-up started must be gone.
		s.store.DB.Close()
		eventually(t, func() bool { return runtime.NumGoroutine() <= before })
	})

	t.Run("concurrent calls all wait", func(t *testing.T) {
		s, _ := startBlocked(t)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				s.StopWarm()
				select {
				case <-warmBackgroundDone(s):
				default:
					t.Error("StopWarm returned before the goroutine exited")
				}
			})
		}
		wg.Wait()
	})

	t.Run("stops a phase when the parent is cancelled", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		t.Cleanup(s.StopWarm)
		seedPlans(t, s, 2)
		holdSimSlots(t, s)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		s.warm(ctx, 30*time.Millisecond)
		cancel()
		waitWarmBackground(t, s)
		if attrs, _ := findPhase(logs, "warmup finished", warmPhaseBackground); attrs["horizonte.warmup.outcome"] != warmCancelled {
			t.Errorf("background finished = %v", attrs)
		}
	})

	t.Run("no phase starts after it", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		seedPlans(t, s, 2)
		holdSimSlots(t, s)
		s.StopWarm()
		s.warm(t.Context(), 30*time.Millisecond)
		if warmBackgroundDone(s) != nil {
			t.Error("a background phase started after StopWarm")
		}
		if _, ok := findPhase(logs, "warmup started", warmPhaseBackground); ok {
			t.Error("the background phase was logged")
		}
	})
}

// A server runs one background phase: a later call that also hits the limit,
// whether that phase is running or has ended, must not start another.
func TestWarmStartsOneBackgroundPhase(t *testing.T) {
	expectOne := func(t *testing.T, s *Server, logs *logRecorder, first <-chan struct{}) {
		t.Helper()
		if got := warmBackgroundDone(s); got != first {
			t.Error("a second background phase started")
		}
		starts := 0
		for _, line := range warmLogs(logs) {
			if line == "warmup started/background" {
				starts++
			}
		}
		if starts != 1 {
			t.Errorf("background phases started = %d, want 1", starts)
		}
		stopped := make(chan struct{})
		go func() { s.StopWarm(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(time.Minute):
			t.Fatal("StopWarm did not return")
		}
		select {
		case <-first:
		default:
			t.Error("StopWarm returned before the background phase exited")
		}
	}

	t.Run("while one is running", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		t.Cleanup(s.StopWarm)
		seedPlans(t, s, 2)
		holdSimSlots(t, s)

		s.warm(t.Context(), testWarmLimit)
		first := warmBackgroundDone(s)
		if first == nil {
			t.Fatal("no background phase started")
		}
		if res := s.warm(t.Context(), testWarmLimit); res.Outcome != warmPartial || res.Warmed != 0 {
			t.Fatalf("second call = %+v, want it stopped by the limit", res)
		}
		expectOne(t, s, logs, first)
	})

	t.Run("after one has ended", func(t *testing.T) {
		s, logs := newLoggedServer(t)
		ids := seedPlans(t, s, 1)
		pace := holdPlans(t, s, nil, ids[0])

		s.warm(t.Context(), testWarmLimit)
		first := warmBackgroundDone(s)
		if first == nil {
			t.Fatal("no background phase started")
		}
		pace.release(ids[0])
		waitWarmBackground(t, s)

		// A new plan the second call can't calculate, so that the limit stops it.
		if _, err := s.store.CopyPlan(t.Context(), ids[0], "new"); err != nil {
			t.Fatal(err)
		}
		holdSimSlots(t, s)
		if res := s.warm(t.Context(), testWarmLimit); res.Outcome != warmPartial || res.Warmed != 1 {
			t.Fatalf("second call = %+v, want it stopped by the limit", res)
		}
		expectOne(t, s, logs, first)
	})
}

// warmWithBackgroundFailures runs a warm-up whose background phase meets a
// panicking plan (ids[1]) and one that fails to load (ids[2]).
func warmWithBackgroundFailures(t *testing.T, s *Server) []int64 {
	t.Helper()
	ids := seedPlans(t, s, 4)
	corruptPlan(t, s, ids[2])
	pace := holdPlans(t, s, func(id int64) {
		if id == ids[1] {
			panic("boom")
		}
	}, ids[0])

	s.warm(t.Context(), testWarmLimit)
	eventually(t, func() bool { return len(flightWaiters(s)) == 1 })
	pace.release(ids[0])
	waitWarmBackground(t, s)
	return ids
}

func TestWarmBackgroundFailuresCostOnlyThatPlan(t *testing.T) {
	metrics := captureMetrics(t)
	s, logs := newLoggedServer(t)
	ids := warmWithBackgroundFailures(t, s)

	if !planIsWarm(t, s, ids[0]) || !planIsWarm(t, s, ids[3]) {
		t.Error("the healthy plans should be warm")
	}
	attrs, _ := findPhase(logs, "warmup finished", warmPhaseBackground)
	if attrs["horizonte.warmup.outcome"] != warmPartial || attrs["horizonte.warmup.failed"] != "2" || attrs["horizonte.warmup.warmed"] != "2" {
		t.Errorf("background finished = %v", attrs)
	}
	if attrs, ok := findPhase(logs, "warmup incomplete", warmPhaseBackground); !ok || attrs["horizonte.warmup.cold"] != "2" {
		t.Errorf("background incomplete = %v, %v", attrs, ok)
	}
	if attrs, ok := findPhase(logs, "warmup plan failed", warmPhaseBackground); !ok || attrs["horizonte.plan.id"] != fmt.Sprint(ids[1]) {
		t.Errorf("plan failed = %v, %v", attrs, ok)
	}
	if strings.Contains(logs.dump(), "boom") {
		t.Error("the log repeats the panic's value")
	}
	if durations := metricsNamed(metrics(), "horizonte.warmup.duration"); len(durations) != 2 || durations[1].attrs["horizonte.warmup.outcome"] != warmPartial {
		t.Errorf("duration metrics = %+v", durations)
	}
}

func TestWarmBackgroundFailuresAreCapturedInSentry(t *testing.T) {
	events := captureEvents(t)
	s := newTestServer(t)
	warmWithBackgroundFailures(t, s)
	if got := events(); len(got) != 2 {
		t.Errorf("sentry events = %d, want 2", len(got))
	}
}

func TestWarmBackgroundFollowsTheCurrentData(t *testing.T) {
	s, logs := newLoggedServer(t)
	ids := seedPlans(t, s, 3)
	var now atomic.Pointer[time.Time]
	month := time.Date(2030, 1, 31, 12, 0, 0, 0, time.UTC)
	now.Store(&month)
	s.now = func() time.Time { return *now.Load() }
	pace := holdPlans(t, s, nil, ids[0])

	s.warm(t.Context(), testWarmLimit)
	eventually(t, func() bool { return len(flightWaiters(s)) == 1 })
	if err := s.store.DeletePlan(t.Context(), ids[1]); err != nil {
		t.Fatal(err)
	}
	next := month.AddDate(0, 0, 1)
	now.Store(&next)
	pace.release(ids[0])
	waitWarmBackground(t, s)

	attrs, _ := findPhase(logs, "warmup finished", warmPhaseBackground)
	if attrs["horizonte.warmup.outcome"] != warmCompleted || attrs["horizonte.warmup.failed"] != "0" || attrs["horizonte.warmup.warmed"] != "2" {
		t.Errorf("background finished = %v", attrs)
	}
	if pace.count(ids[1]) != 0 {
		t.Error("a deleted plan should have nothing to calculate")
	}
	if !planIsWarm(t, s, ids[2]) {
		t.Error("the last plan should be warm for the new month")
	}
}

func TestWarmBackgroundServesLiveRequests(t *testing.T) {
	metrics := captureMetrics(t)
	s := newTestServer(t)
	ids := seedPlans(t, s, 2)
	pace := holdPlans(t, s, nil, ids[0], ids[1])

	s.warm(t.Context(), testWarmLimit)
	eventually(t, func() bool { return len(flightWaiters(s)) == 1 })
	pace.release(ids[0])
	// The background has finished the first plan and is holding the second.
	eventually(t, func() bool { return pace.count(ids[1]) == 1 })

	before := pace.count(ids[0])
	rec, body := do(s, httptest.NewRequest("GET", "/success", nil))
	if rec.Code != http.StatusOK || strings.Contains(body, `hx-trigger="load`) {
		t.Errorf("/success: status = %d, placeholder = %v", rec.Code, strings.Contains(body, `hx-trigger="load`))
	}
	if got := pace.count(ids[0]); got != before {
		t.Errorf("the request repeated a finished calculation (%d, was %d)", got, before)
	}

	// A visitor opening the plan being calculated joins that calculation.
	if err := s.store.SetCurrentPlan(t.Context(), ids[1]); err != nil {
		t.Fatal(err)
	}
	type response struct {
		code int
		body string
	}
	got := make(chan response, 1)
	go func() {
		rec, body := do(s, htmxReq("GET", "/success/results?seed=1", nil))
		got <- response{rec.Code, body}
	}()
	eventually(t, func() bool { w := flightWaiters(s); return len(w) == 1 && w[0] == 2 })
	pace.release(ids[1])
	if r := <-got; r.code != http.StatusOK || r.body == "" {
		t.Errorf("results: status = %d, %d bytes", r.code, len(r.body))
	}
	waitWarmBackground(t, s)

	if n := pace.count(ids[1]); n != 3 {
		t.Errorf("calculations for the shared plan = %d, want 3", n)
	}
	if !planIsWarm(t, s, ids[1]) {
		t.Error("the plan should be warm")
	}
	durations := metricsNamed(metrics(), "horizonte.warmup.duration")
	if len(durations) != 2 || durations[1].attrs["horizonte.warmup.outcome"] != warmCompleted {
		t.Errorf("duration metrics = %+v", durations)
	}
}

// main.go closes the database right after StopWarm returns: nothing may be
// left that would go on to read it.
func TestStopWarmLeavesTheDatabaseUnused(t *testing.T) {
	events := captureEvents(t)
	s, logs := newLoggedServer(t)
	t.Cleanup(s.StopWarm)
	seedPlans(t, s, 3)
	holdSimSlots(t, s)

	s.warm(t.Context(), 30*time.Millisecond)
	s.StopWarm()
	s.store.DB.Close()

	select {
	case <-warmBackgroundDone(s):
	default:
		t.Fatal("the goroutine is still running with the database closed")
	}
	if strings.Contains(logs.dump(), "warmup plan failed") {
		t.Errorf("a plan failed:\n%s", logs.dump())
	}
	if got := events(); len(got) != 0 {
		t.Errorf("sentry events = %+v, want none", got)
	}
}
