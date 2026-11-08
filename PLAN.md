# Horizonte — Implementation Plan

A self-hosted, privacy-first financial and retirement planner.
Built with Go + htmx, server-rendered, single binary, local SQLite storage.

## Decisions

| Topic | Decision |
|---|---|
| Users | Single user per instance, no authentication |
| Currency | EUR, formatted per locale (`es`: `1.234,56 €`, `en`: `€1,234.56`) |
| Languages | Spanish (default) and English; switchable in the UI, saved in a cookie |
| Taxes | Flat effective rate for the MVP; Spanish IRPF (general + savings base, regional scales) in phase 9 |
| Time step | Monthly simulation engine; results also rolled up by year. The UI switches between monthly and yearly views |
| Version control | Git; one commit per finished phase |

## Stack

- **Backend:** Go 1.27, standard-library `net/http` router, `html/template`
- **Frontend:** htmx, Alpine.js, Apache ECharts (line, area, Sankey, fan charts), Pico.css + custom theme (light/dark)
- **Storage:** SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **i18n:** JSON message catalogs (`internal/i18n/locales/es.json`, `en.json`) with a template `t` function
- **Packaging:** all static assets kept in the repo and embedded with `go:embed`; no outside CDNs at runtime

## Architecture

```
cmd/
  server/        HTTP entrypoint, logging and Sentry setup
  dataset/       regenerates the embedded historical market data
internal/
  domain/        Person, Account, Income, Expense, Debt, Prepayment, Milestone, Month and their validation
  engine/        pure simulation engine (no I/O, unit-tested)
    engine.go       monthly simulation + yearly rollup
    withdrawal.go   withdrawal order and strategies
    tax.go          tax models: flat rate and Spanish IRPF
    montecarlo.go   parallel randomized runs, percentile bands
    backtest.go     historical sequences
    fire.go         what-if: stop working at a milestone
  tax/es/        Spanish IRPF: general and savings bases, regional scales
  pension/es/    Spanish public pension estimate
  history/       historical monthly returns and inflation (Shiller dataset, embedded CSV)
  backup/        JSON import and export of a plan
  xls/           minimal .xls reader used by cmd/dataset
  i18n/          message catalogs, number/currency formatting
  store/         SQLite repositories + migrations
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
