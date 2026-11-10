# Load testing

[k6](https://k6.io) scripts that simulate people using Horizonte. The first,
`perf/k6/navigation.js`, sends HTTP requests: 15 virtual users for 20 minutes by
default, each repeating one of four journeys with 0.5 to 6 s of think time between
steps. The second, `perf/k6/browser.js`, drives real browsers (see below).

| Journey    | Weight | Steps |
|------------|--------|-------|
| planner    | 40 %   | home → plan → month/year and real/nominal toggles → success odds (Monte Carlo) → reseed → scenario comparison |
| cashflow   | 20 %   | cash flow → year slider moved 4 times → monthly nominal view |
| backtester | 20 %   | historical backtest → 3 crises → a start year |
| bookkeeper | 20 %   | finances → each section → summary → pension tool → health tool (`/finances/health`) → tracking (`/tracking`) |

Requests send the same `HX-Request`/`HX-Boosted` headers htmx does, and are
tagged `kind` = `page`, `partial`, `simulation` or `static` (thresholds are
set per kind) and `name` (per-endpoint latency in the summary). The script
sends `Accept-Encoding: gzip` and follows the requests that a page's placeholders
make for their simulation results.

## Running it

Always use a separate, empty database: `setup()` seeds a sample plan (the
one the Go tests use) when the active plan has no people, and the run must
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

## Real browsers

`perf/k6/browser.js` drives headless Chromium (k6's browser module) instead of
sending requests: 30 users for 1 hour by default. Each visit opens a fresh
browser context, clicks 15 random visible links with 2–8 s of think time, and
closes it. The app's JavaScript runs, so Sentry receives browser transactions,
web vitals, logs and Session Replays (and the traces link to the server's).

It only clicks `<a href>` links (never buttons, forms or selects) and skips the
export downloads, so it never changes the plan and can run against a real
server:

```sh
k6 run -e BASE_URL=http://127.0.0.1:8347 perf/k6/browser.js
# -e VUS=30 -e DURATION=1h -e CLICKS=15 -e THINK_MIN=2 -e THINK_MAX=8
# -e SETTLE_TIMEOUT=15 -e SLOW_SETTLE_TIMEOUT=60   (seconds per click)
# -e SIMULATION_P95=10   (seconds, p95 limit for the simulation pages)
# -e CLICK_FAILURE_RATE=0.05   (highest share of clicks that may fail, 0 for none)
```

`SIMULATION_P95` must be lower than `SLOW_SETTLE_TIMEOUT`, or the script refuses
to start: a click over the timeout records no time, so the limit could never be
crossed.

A click fails if an htmx request during the click gets an error response, loses
its connection or times out, if its swap fails, if a full page load it starts
(`hx-boost="false"` links) ends with status 400 or above, or if its page hasn't
finished loading within `SETTLE_TIMEOUT`, or `SLOW_SETTLE_TIMEOUT` for the simulation
pages (Monte Carlo, scenarios, backtest). A failed click records no settle time
and no "page rendered" check. Simulation results are cached and warmed at
startup. When a result is ready the page renders it straight away; otherwise the
page renders a placeholder and the browser fetches the result, which can be slow
under load. When `DURATION` is over, visits stop clicking and close their browser
on their own, so k6 exits cleanly instead of interrupting them mid-click.

The run also fails if the `checks` rate drops to 95 % or below, or the p75 of the
largest contentful paint (`browser_web_vital_lcp`) reaches 2.5 s. It fails if more than `CLICK_FAILURE_RATE` of clicks fail (5 % by default,
0 allows none; `synthetic_click_failed`), or if the p95 of
`synthetic_click_settle` is over 5 s for ordinary pages or over
`SIMULATION_P95` for simulation pages. A home page that can't be loaded counts
as a failed visit (`synthetic_visit_failures`) and a failed check, and ends
that visit.

`synthetic_click_settle` runs from the click to the swap being finished, with
no fixed wait inside it. Values from runs before this change included a floor
of about 200 ms and are not directly comparable.

The scripts were tested on k6 2.3.0; other versions are untested. On that
version the k6 REST API is off unless you pass `--address 127.0.0.1:6565`.
Each user runs its own Chromium, which is about 8 processes. With 30 users at the
script's 1440x900 viewport, Chromium used about 10 GB of private memory (11 to 13 GB
at peak) and k6 about 250 MB.

To record the server's CPU and memory during the run (Windows):

```powershell
powershell -File perf/sample-process.ps1 -ProcessId <server pid> -Out perf/results/process.csv
```

It writes one CSV row every `-Interval` seconds (default 5) for `-Seconds` (default
1260, a little over the 20-minute HTTP test; raise it for the browser run), or until the
process exits. `cpu_pct` is the share of the whole machine, and the columns also give
working set, private memory, threads and handles.

## Thresholds of the HTTP test (`navigation.js`)

| Metric | Limit |
|--------|-------|
| failed requests | < 1 % |
| checks (status 200, page rendered) | > 99 % |
| checks on static assets (`checks{kind:static}`) | > 99 % |
| p95 pages (including `/success`, `/backtest` and `/scenarios`) and partials | < 800 ms |
| p95 simulation requests (deferred results and reseed) | < 3 s |
| p95 whole journey (think time included) | < 60 s |
