package web

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/history"
)

// The simulation cache is empty after every restart, so the warm-up fills it
// with what the simulation pages look up, active plan first. Everything it
// does is read-only: startup must not change the user's data.
//
// Startup waits for it up to warmTimeout. If that limit stops it, the plans it
// didn't reach carry on in one background goroutine (see StopWarm).

const (
	warmTimeout = 60 * time.Second
	warmSource  = "warmup" // value of horizonte.simulation.source

	// warmEntriesPerPlan is how many cache entries a plan can take: two Monte
	// Carlo results and a rolling backtest.
	warmEntriesPerPlan = 3
)

// Values of horizonte.warmup.phase.
const (
	warmPhaseStartup    = "startup"
	warmPhaseBackground = "background"
)

// Values of WarmResult.Outcome.
const (
	warmCompleted = "completed"
	warmPartial   = "partial"
	warmCancelled = "cancelled"
)

// WarmResult says what a phase of a warm-up did. Outcome is "completed", "partial" (some
// plans were left cold) or "cancelled" (the phase was stopped before it finished).
type WarmResult struct {
	Outcome     string
	Duration    time.Duration
	Plans       int // plans this phase considered
	Warmed      int // plans whose simulations all finished (a plan with no people does not count)
	Failed      int
	Simulations int // calculations that actually ran (not already cached)
}

// Warm fills the simulation cache, blocking for at most warmTimeout. Plans it
// can't warm are reported and left to be calculated on their first view, so it
// has no error to return. If the time limit stops it, the rest of the plans
// continue in the background until they finish or StopWarm is called.
func (s *Server) Warm(ctx context.Context) WarmResult { return s.warm(ctx, s.warmTimeout) }

func (s *Server) warm(parent context.Context, timeout time.Duration) WarmResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Logs must still go out after the context ends.
	logCtx := context.WithoutCancel(parent)

	var res WarmResult
	ids, listErr := s.warmOrder(ctx)
	if listErr != nil {
		if ctx.Err() == nil {
			s.log.ErrorContext(logCtx, "warmup failed", "error.type", fmt.Sprintf("%T", listErr))
			sentry.CurrentHub().CaptureException(listErr)
		}
	} else {
		s.log.InfoContext(logCtx, "warmup started",
			"horizonte.warmup.phase", warmPhaseStartup, "horizonte.warmup.plans", len(ids))
	}
	res.Plans = len(ids)

	// The limit keeps the warm-up from evicting its own results. Plans with
	// no people use up a place too: knowing which they are takes a load each.
	limit := min(s.sims.max/warmEntriesPerPlan, len(ids))
	next, skipped := s.warmPlans(ctx, logCtx, warmPhaseStartup, ids[:limit], &res)

	cold := res.Plans - res.Warmed - skipped
	res.Outcome = warmCompleted
	if cold > 0 || listErr != nil {
		res.Outcome = warmPartial
		if parent.Err() != nil {
			res.Outcome = warmCancelled
		}
	}
	res.Duration = time.Since(start)
	s.reportWarm(logCtx, warmPhaseStartup, res, cold)

	if next < limit && parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.startWarmBackground(parent, ids[next:limit])
	}
	return res
}

// warmPlans warms ids in order, one at a time, adding to res. It returns the
// position of the plan ctx interrupted (len(ids) if it got through them all)
// and how many plans had nothing to warm.
func (s *Server) warmPlans(ctx, logCtx context.Context, phase string, ids []int64, res *WarmResult) (next, skipped int) {
	for i, id := range ids {
		if ctx.Err() != nil {
			return i, skipped
		}
		ran, warmed, err := s.warmPlan(ctx, id)
		res.Simulations += ran
		switch {
		case err != nil && ctx.Err() != nil: // interrupted, not broken
			return i, skipped
		case err != nil:
			res.Failed++
			s.log.ErrorContext(logCtx, "warmup plan failed", "horizonte.warmup.phase", phase,
				"horizonte.plan.id", id, "error.type", fmt.Sprintf("%T", err))
			if !errors.Is(err, engine.ErrPanic) { // callSim or warmPlan already captured it
				sentry.CurrentHub().CaptureException(err)
			}
		case warmed:
			res.Warmed++
		default:
			skipped++
		}
	}
	return len(ids), skipped
}

// reportWarm logs and records how a phase ended.
func (s *Server) reportWarm(logCtx context.Context, phase string, res WarmResult, cold int) {
	if res.Outcome == warmPartial && cold > 0 { // without a list of plans, cold means nothing
		s.log.WarnContext(logCtx, "warmup incomplete",
			"horizonte.warmup.phase", phase, "horizonte.warmup.cold", cold)
	}
	s.log.InfoContext(logCtx, "warmup finished",
		"horizonte.warmup.phase", phase,
		"horizonte.warmup.duration_ms", res.Duration.Milliseconds(),
		"horizonte.warmup.outcome", res.Outcome,
		"horizonte.warmup.plans", res.Plans,
		"horizonte.warmup.warmed", res.Warmed,
		"horizonte.warmup.failed", res.Failed,
		"horizonte.warmup.simulations", res.Simulations)
	recordWarmup(logCtx, phase, res)
}

// warmBackground is the background phase of the warm-up. The zero value means
// it never started; a server runs it at most once.
type warmBackground struct {
	mu      sync.Mutex
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{} // closed when the goroutine has exited
}

func (s *Server) startWarmBackground(parent context.Context, ids []int64) {
	bg := &s.warmBG
	bg.mu.Lock()
	defer bg.mu.Unlock()
	if bg.stopped || bg.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	bg.cancel, bg.done = cancel, done
	go func() {
		defer close(done)
		defer cancel()
		s.runWarmBackground(ctx, ids)
	}()
}

// runWarmBackground warms what the time limit left over. It has no limit of
// its own: the simulations are finite and stop when ctx ends.
func (s *Server) runWarmBackground(ctx context.Context, ids []int64) {
	start := time.Now()
	logCtx := context.WithoutCancel(ctx)
	s.log.InfoContext(logCtx, "warmup started",
		"horizonte.warmup.phase", warmPhaseBackground, "horizonte.warmup.plans", len(ids))

	res := WarmResult{Plans: len(ids)}
	next, skipped := s.warmPlans(ctx, logCtx, warmPhaseBackground, ids, &res)

	cold := res.Plans - res.Warmed - skipped
	res.Outcome = warmCompleted
	switch {
	case next < len(ids):
		res.Outcome = warmCancelled
	case cold > 0:
		res.Outcome = warmPartial
	}
	res.Duration = time.Since(start)
	s.reportWarm(logCtx, warmPhaseBackground, res, cold)
}

// StopWarm cancels the background phase of the warm-up, if there is one, and
// waits for it to exit, so the database can be closed safely. No background
// phase can start afterwards. It is safe to call more than once and from
// several goroutines.
func (s *Server) StopWarm() {
	bg := &s.warmBG
	bg.mu.Lock()
	bg.stopped = true
	cancel, done := bg.cancel, bg.done
	bg.mu.Unlock()
	if done != nil {
		cancel()
		<-done
	}
}

// warmOrder lists the plans, the active one first.
func (s *Server) warmOrder(ctx context.Context) ([]int64, error) {
	plans, err := s.store.ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	active, ok, err := s.store.ActivePlanID(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(plans))
	if ok {
		ids = append(ids, active)
	}
	for _, p := range plans {
		if !ok || p.ID != active {
			ids = append(ids, p.ID)
		}
	}
	return ids, nil
}

// warmPlan calculates what the simulation pages look up for one plan, one at
// a time (each simulation already uses every CPU core). It returns how many
// calculations ran and whether the plan was warmed; a plan with no people
// has nothing to warm and returns false with no error. A plan deleted in the
// meantime loads as one with no people.
func (s *Server) warmPlan(ctx context.Context, id int64) (ran int, warmed bool, err error) {
	defer func() { // the simulations recover by themselves; this is for the load
		if recover() != nil {
			warmed, err = false, engine.NewPanicError()
			capturePanic(ctx, err)
		}
	}()
	// Read for each plan: the background phase can run across a month change.
	in, err := s.loadInput(ctx, finCtx{plan: id, now: domain.MonthOf(s.now())})
	if err != nil {
		return 0, false, err
	}
	if len(in.People) == 0 {
		return 0, false, nil
	}
	for _, set := range []engine.MCSettings{mcSettings(in, defaultSeed), compareSettings()} {
		if _, ok := cachedSim[engine.MCResult](s.sims, mcKey(in, set)); ok {
			continue
		}
		if _, err := s.runMonteCarlo(ctx, warmSource, in, set); err != nil {
			return ran, false, err
		}
		ran++
	}
	if _, ok := cachedSim[[]engine.RollingStart](s.sims, rollingKey(in)); !ok {
		hs, err := history.Load()
		if err != nil {
			return ran, false, err
		}
		if _, err := s.runRolling(ctx, warmSource, in, hs); err != nil {
			return ran, false, err
		}
		ran++
	}
	return ran, true, nil
}
