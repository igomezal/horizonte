# Load testing

[k6](https://k6.io) script that simulates people using Horizonte: 15 virtual
users for 20 minutes by default, each repeating one of four journeys with
1–6 s of think time between steps.

| Journey    | Weight | Steps |
|------------|--------|-------|
| planner    | 40 %   | home → plan → month/year and real/nominal toggles → success odds (Monte Carlo) → reseed → scenario comparison |
| cashflow   | 20 %   | cash flow → year slider moved 4 times → monthly nominal view |
| backtester | 20 %   | historical backtest → 3 crises → a start year |
| bookkeeper | 20 %   | finances → each section → summary → pension tool |

Requests send the same `HX-Request`/`HX-Boosted` headers htmx does, and are
tagged `kind` = `page`, `partial`, `simulation` or `static` (thresholds are
set per kind) and `name` (per-endpoint latency in the summary).

## Running it

Always use a separate, empty database: `setup()` seeds a sample plan (the
one the Go tests use) when the database has no people, and the run must
never touch your real data.

```sh
go build -o horizonte-perf ./cmd/server
./horizonte-perf -addr 127.0.0.1:8092 -db /tmp/horizonte-perf.db

k6 run -e BASE_URL=http://127.0.0.1:8092 perf/k6/navigation.js
# shorter or smaller: -e VUS=3 -e DURATION=1m
# summary JSON goes to perf/results/ (ignored by git), or -e SUMMARY_DIR=...
```

To see the run in Sentry, start the server with `SENTRY_DSN` and
`SENTRY_ENVIRONMENT=perf-test`, then filter by that environment. Server-side
traces, logs and the `horizonte.simulation.duration` metric are recorded; k6
doesn't run the browser JavaScript, so no browser telemetry is sent.

To record the server's CPU and memory during the run (Windows):

```powershell
powershell -File perf/sample-process.ps1 -ProcessId <server pid> -Out perf/results/process.csv
```

## Thresholds

| Metric | Limit |
|--------|-------|
| failed requests | < 1 % |
| checks (status 200, page rendered) | > 99 % |
| p95 pages and partials | < 800 ms |
| p95 simulation pages (Monte Carlo, backtest, scenarios) | < 3 s |
| p95 whole journey (think time included) | < 60 s |
