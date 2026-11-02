package web

import (
	"context"
	"runtime"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"

	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/history"
)

// Metrics conventions (shared with ui/static/js/app.js and charts.js):
//
//   - Names are horizonte.<domain>.<measure>, lowercase and dot-separated.
//   - Every distribution has a unit (millisecond, byte).
//   - Attributes use the same keys as the logs (see cmd/server/logging.go):
//     standard ones like http.route, otherwise horizonte.<domain>.<field>.
//   - Only measure what Sentry can't derive itself. Request latency,
//     throughput and error counts already come from traces and issues.
//   - Never put amounts, plan names or results of a person's plan (success
//     rates, ages, net worth) in a metric or attribute.
//
// Without a Sentry client sentry.NewMeter returns a no-op meter, so these
// helpers are free when Sentry is off and in tests.

// meter returns a Sentry meter tied to the request's trace when ctx has one.
func meter(ctx context.Context) sentry.Meter { return sentry.NewMeter(ctx) }

// mcKey and rollingKey identify a simulation by everything it depends on, for
// the simulation cache (see simrunner.go). The history series is fixed, so
// the rolling backtest only depends on the plan.
func mcKey(in engine.Input, set engine.MCSettings) string {
	return simKey("mc", in, in.Assumptions.Volatility, set)
}

func rollingKey(in engine.Input) string { return simKey("rolling", in) }

// countSimCache counts simulation requests by whether the result was already
// cached, which shows how much work the cache saves. served says where the
// result went: "results" for the requests a page makes to load its results
// (monteCarlo, rolling), "page" for a page that found the result cached and
// rendered it straight away. Every lookup is counted once: a page only counts
// its hits, because on a miss it renders a placeholder and the results
// request that follows counts the miss.
func countSimCache(ctx context.Context, kind, source, served string, hit bool) {
	outcome := "miss"
	if hit {
		outcome = "hit"
	}
	meter(ctx).Count("horizonte.simulation.cache", 1, sentry.WithAttributes(
		attribute.String("horizonte.simulation.kind", kind),
		attribute.String("horizonte.simulation.source", source),
		attribute.String("horizonte.simulation.served", served),
		attribute.String("horizonte.simulation.cache", outcome),
	))
}

// Values of horizonte.simulation.served.
const (
	servedResults = "results"
	servedPage    = "page"
)

// monteCarlo runs the simulation through the simulation runner (cached,
// shared and limited) and records how long it took when it actually ran. Its
// cost grows with runs × months and depends on the CPU, so these attributes
// are what tell a slow machine apart from a plan with a very long horizon.
// The error is ctx's when the person left before it finished.
func (s *Server) monteCarlo(ctx context.Context, source string, in engine.Input, set engine.MCSettings) (engine.MCResult, error) {
	key := mcKey(in, set)
	if mc, ok := cachedSim[engine.MCResult](s.sims, key); ok {
		countSimCache(ctx, "monte_carlo", source, servedResults, true)
		return mc, nil
	}
	countSimCache(ctx, "monte_carlo", source, servedResults, false)
	return runSim(ctx, s.sims, key, func(ctx context.Context) (engine.MCResult, error) {
		start := time.Now()
		mc, err := engine.MonteCarloContext(ctx, in, in.Assumptions.Volatility, set)
		if err != nil {
			return mc, err
		}
		meter(ctx).Distribution("horizonte.simulation.duration", msSince(start),
			sentry.WithUnit(sentry.UnitMillisecond),
			sentry.WithAttributes(
				attribute.String("horizonte.simulation.kind", "monte_carlo"),
				attribute.String("horizonte.simulation.source", source),
				attribute.Int("horizonte.simulation.runs", mc.Runs),
				attribute.Int("horizonte.simulation.months", horizonMonths(in)),
				attribute.Int("horizonte.simulation.workers", runtime.GOMAXPROCS(0)),
			))
		return mc, nil
	})
}

// rolling runs the historical backtest from every start year (one simulation
// per start year), the same way as monteCarlo.
func (s *Server) rolling(ctx context.Context, source string, in engine.Input, hs *history.Series) ([]engine.RollingStart, error) {
	key := rollingKey(in)
	if starts, ok := cachedSim[[]engine.RollingStart](s.sims, key); ok {
		countSimCache(ctx, "rolling_backtest", source, servedResults, true)
		return starts, nil
	}
	countSimCache(ctx, "rolling_backtest", source, servedResults, false)
	return runSim(ctx, s.sims, key, func(ctx context.Context) ([]engine.RollingStart, error) {
		start := time.Now()
		starts, err := engine.RollingContext(ctx, in, hs)
		if err != nil {
			return nil, err
		}
		meter(ctx).Distribution("horizonte.simulation.duration", msSince(start),
			sentry.WithUnit(sentry.UnitMillisecond),
			sentry.WithAttributes(
				attribute.String("horizonte.simulation.kind", "rolling_backtest"),
				attribute.String("horizonte.simulation.source", source),
				attribute.Int("horizonte.simulation.runs", len(starts)),
				attribute.Int("horizonte.simulation.months", horizonMonths(in)),
				attribute.Int("horizonte.simulation.workers", runtime.GOMAXPROCS(0)),
			))
		return starts, nil
	})
}

func horizonMonths(in engine.Input) int {
	end := in.EndMonth()
	if end.IsZero() {
		return 0
	}
	return in.Start.MonthsUntil(end) + 1
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

// countImport counts plan imports by outcome, so a rise in rejections after a
// release (an export/import format mismatch) shows up on a chart or alert.
func countImport(ctx context.Context, outcome, reason string) {
	attrs := []attribute.Builder{attribute.String("horizonte.import.outcome", outcome)}
	if reason != "" {
		attrs = append(attrs, attribute.String("horizonte.import.reject_reason", reason))
	}
	meter(ctx).Count("horizonte.import", 1, sentry.WithAttributes(attrs...))
}

// countExport counts plan exports by scope and outcome. A failed export sends
// a truncated file without an error page, so this is how failures are seen.
func countExport(ctx context.Context, scope string, ok bool) {
	outcome := "completed"
	if !ok {
		outcome = "failed"
	}
	meter(ctx).Count("horizonte.export", 1, sentry.WithAttributes(
		attribute.String("horizonte.export.scope", scope),
		attribute.String("horizonte.export.outcome", outcome)))
}
