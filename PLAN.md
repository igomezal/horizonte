# Horizonte — Implementation Plan

A self-hosted, privacy-first financial and retirement planner.
Built with Go + htmx, server-rendered, single binary, local SQLite storage.

**Status:** all phases (0–10) are done. This file is the original plan, kept as a record
of the decisions and the order of the work, and corrected where later changes superseded
it (marked "superseded" below). What was added afterwards is listed in
[After the plan](#after-the-plan). For how the app works today, see the
[README](README.md) and [docs/](docs/).

## Decisions

| Topic | Decision |
|---|---|
| Users | Single user per instance, no authentication |
| Currency | EUR, formatted per locale (`es`: `1.234,56 €`, `en`: `€1,234.56`) |
| Languages | Spanish (default) and English; switchable in the UI, saved in a cookie |
| Taxes | Flat effective rate for the MVP; Spanish IRPF (general + savings base, regional scales) in phase 9. *Superseded in part:* IRPF is now the default tax model of a new plan and the flat rate is the alternative (see [docs/assumptions.md](docs/assumptions.md)) |
| Time step | Monthly simulation engine; results also rolled up by year. The UI switches between monthly and yearly views |
| Version control | Git; one commit per finished phase (phases 0–10). Afterwards, one commit per change |

## Stack

- **Backend:** Go 1.27, standard-library `net/http` router, `html/template`
- **Frontend:** htmx, Alpine.js, Apache ECharts (line, area, Sankey, fan charts), Pico.css + custom theme (light/dark). The first custom theme ("Billete") was replaced by the "Monitor" design system, see [ui/DESIGN.md](ui/DESIGN.md)
- **Storage:** SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **i18n:** JSON message catalogs (`internal/i18n/locales/es.json`, `en.json`) with a template `t` function
- **Packaging:** all static assets kept in the repo and embedded with `go:embed`; no outside CDNs at runtime

## Architecture

```
cmd/
  server/        HTTP entrypoint, automatic backups, logging, Sentry setup, health check
  dataset/       regenerates the embedded historical market data
docs/            data and privacy, plan file format, assumptions, development guide (added after the plan)
internal/
  domain/        Person, Account, Income, Expense, Debt, Prepayment, Milestone, Month, net worth tracking and their validation
  engine/        pure simulation engine (no I/O, unit-tested)
    engine.go       monthly simulation + yearly rollup
    withdrawal.go   withdrawal order and strategies
    tax.go          tax models: flat rate and Spanish IRPF
    montecarlo.go   parallel randomized runs, percentile bands
    bootstrap.go    historical block model for Monte Carlo (added after the plan)
    backtest.go     historical sequences
    fire.go         what-if: stop working at a milestone
  tax/es/        Spanish IRPF: general and savings bases, regional scales and personal minimums, DA 61 deduction
  pension/es/    Spanish public pension estimate: art. 208 early-retirement table, cap, legal age, minimum
  history/       historical monthly returns and inflation (Shiller dataset, embedded CSV)
  backup/        JSON import and export of plans (and net worth history in the all-plans file)
  xls/           minimal .xls reader used by cmd/dataset
  i18n/          message catalogs, number/currency formatting
  store/         SQLite repositories, migrations and automatic backups
  web/           routes, handlers, middleware, simulation cache, rendering
ui/
  templates/     layout, pages, partials
  static/        CSS, JS, fonts and vendored libraries
perf/            k6 load tests and a process sampler
```

The engine is a pure function, `Simulate(Input) -> Result`. The deterministic
projection, Monte Carlo and backtests all reuse it.

## Phases

| # | Phase | Status |
|---|---|---|
| 0 | Skeleton: module, router, layout, htmx, theme, i18n (es/en), SQLite migrations | ✅ |
| 1 | Current Finances: CRUD for people, accounts, income, expenses, debts | ✅ |
| 2 | Monthly engine + Plan view: net worth and per-account charts, monthly/yearly table | ✅ |
| 3 | Milestones: fixed and conditional, streams tied to milestones, chart timeline | ✅ |
| 4 | Cash flow: Sankey per year, savings rate, expense breakdown | ✅ |
| 5 | Chance of Success: Monte Carlo, fan chart, success %, milestone timing table | ✅ |
| 6 | Historical backtest: every start year, heat-map, worst sequences | ✅ |
| 7 | Scenarios: clone plans (conservative/central/optimistic), side-by-side comparison | ✅ |
| 8 | Withdrawal strategies: fixed %, 4% rule, guardrails, withdrawal order | ✅ |
| 9 | Spanish taxes: IRPF general/savings bases, regional scales, pension plan rules | ✅ |
| 10 | Extras: Spanish public pension, healthcare phases, estate view, JSON import/export | ✅ |

Phases 0–5 are the MVP.

## After the plan

Added since phase 10, from the git history (newest last within each group). None of it
changed the decisions above, except where noted.

**Model and rules**

- Early repayments limited to the month's surplus and money above a reserve.
- Tax and pension tables updated to the 2026 rules; the remaining common-regime regional
  IRPF scales; regional personal minimums for the regional half of the tax (Madrid,
  Galicia, Andalucía, Asturias, C. Valenciana, Canarias and Illes Balears); the DA 61
  work-income deduction; an option to freeze IRPF brackets in nominal euros.
- Pension estimate: the art. 208 early-retirement table, then fixes found in review
  (2 of the last 15 years, the maximum pension with the DT 34ª phase-in, the legal age
  between 65 and 67, the minimum pension check).
- An optional cost basis per account, and selling a real-estate account at a date or
  milestone.
- A historical block bootstrap model for Monte Carlo, backtests labelled as US data, and
  a comparison of the plan with stopping work at each FIRE milestone.

**Net worth tracking**

- A Tracking page: real net worth recorded monthly and compared with a baseline saved from
  a plan. Its history travels in the all-plans JSON export.

**Operations**

- Optional Sentry error reporting with logs, metrics and masked Session Replay; a
  Dockerfile and compose file with a persistent volume and health check; automatic
  database backups before migrations and every 24 hours; CI with build, vet, tests,
  staticcheck and govulncheck; Go 1.27.
- Performance: static files minified, compressed and cached by hash, every response
  compressed, the simulation cache warmed before the port opens, a Content Security
  Policy, an idle timeout, ECharts loaded on demand; k6 load tests (HTTP and real
  browsers) in `perf/`.

**Interface**

- Home page explaining the app, later with cards for every page including the public
  pension and health cost tools, and a "your plan at a glance" card that shows the active
  plan's own numbers (net worth, how long the money lasts, chance of success, next
  milestone).
- The first design system ("Billete") was replaced by the "Monitor" design: navy and
  blue, status chips, monospace numbers (JetBrains Mono instead of Instrument Sans),
  and an online/offline indicator. The sidebar marks the tool pages under Finances as
  active. *Superseded:* the earlier look.
- The project was renamed to Horizonte; documentation (README, `docs/`, `ui/DESIGN.md`,
  `perf/README.md`) was reviewed against the code.

Still out of scope (deliberately): bank links, multiple users or logins, the foral tax
systems of País Vasco and Navarra, and the items listed under "Left out on purpose" in
[docs/assumptions.md](docs/assumptions.md).
