# Horizonte

A self-hosted, privacy-first financial and retirement planner.
It is built with Go and htmx, runs as a single binary, and stores data in a local SQLite file.
The UI is in Spanish (default) and English, and amounts are in EUR.
It is for one person or household: there are no user accounts and no login.

Every figure is an estimate for planning, not financial or tax advice.

See [PLAN.md](PLAN.md) for the original implementation plan and what was added after it.

## What it does

Page names are shown as English (Spanish) and match the sidebar and the home page.

| Page | Path | What it is for |
|---|---|---|
| Home (Inicio) | `/` | Your plan at a glance (net worth, how long the money lasts, chance of success, next milestone) when the active plan has people; a compound-interest example otherwise. Links to every page below |
| Current finances (Finanzas actuales) | `/finances` | Enter people, milestones, accounts, income, expenses, debts and early repayments by hand. Nothing is linked to a bank |
| Public pension (Pensión pública) | `/finances/pension` | Estimates a Spanish Social Security retirement pension and adds it to the plan as income |
| Health costs (Gastos de salud) | `/finances/health` | Estimates health spending by age band and adds it to the plan as expenses |
| Plan (Plan) | `/plan` | Month-by-month projection of net worth (chart stacked by account type, or by account) with milestones, assumptions (inflation, taxes, returns, withdrawal strategy), inheritance and a "stop working at FIRE" comparison |
| Cash flow (Flujo de caja) | `/cashflow` | One year at a time: Sankey diagram, savings rate, spending by category and the income tax breakdown |
| Chance of success (Probabilidad de éxito) | `/success` | Monte Carlo simulation: how often the money lasts, a fan chart and when milestones are reached |
| Historical backtest (Prueba histórica) | `/backtest` | The plan replayed through US market history from every start year and through named crises |
| Scenarios (Escenarios) | `/scenarios` | Copies of the plan to compare side by side, and JSON import and export |
| Tracking (Seguimiento) | `/tracking` | Record your real net worth each month and compare it with a baseline saved from a plan |

The top bar shows a Scenario switcher (when there is more than one plan), an online/offline indicator, the language switch and the light/dark toggle. The indicator is computed in the browser (it never polls the server).

What the simulation does and does not model is in [docs/assumptions.md](docs/assumptions.md).

## Requirements

- Go 1.27.4 or newer (the version in `go.mod`)

## Run

```sh
go run ./cmd/server
# open http://127.0.0.1:8347
```

Flags:

| Flag | Default | Description |
|---|---|---|
| `-addr` | `127.0.0.1:8347` | Listen address. Localhost only by default, so your data is not exposed on the network |
| `-db` | `horizonte.db` | SQLite database file (git-ignored) |
| `-dev` | `false` | Read templates and static files from `./ui` on every request, so edits show up without restarting. Without it, static files are minified, compressed (gzip, and brotli for browsers that ask for it, which over plain HTTP Chrome and Firefox don't) and cached by browsers (versioned URLs). Pages, partials and exports are compressed on the fly in both modes |
| `-healthcheck` | `false` | Ask the server on `-addr` for `/healthz`, then exit with status 0 (healthy) or 1. The Docker health check uses it |
| `-backup-dir` | `backups` next to the database | Where automatic backups are kept |
| `-backup-interval` | `24h` | Time between automatic backups; `0` disables the schedule |
| `-backup-keep` | `7` | Number of backups to keep (at least 1) |

After every start or restart, the app first prepares your plans' simulations, and until the port opens it does not answer. The port opens when that finishes, usually within seconds; past about 60 s it opens anyway and the remaining plans carry on in the background. Any plan left unprepared is calculated the first time you open it.

## Build

```sh
go build -o bin/server ./cmd/server
```

All templates, CSS, JS, fonts and vendored libraries (htmx, Alpine.js, ECharts, Pico.css, the Sentry browser SDK) are embedded in the binary.

## Docker

```sh
docker compose up -d        # build and start on http://localhost:8347
```

The database is stored in the `/data` volume (`horizonte-data` in
`compose.yaml`), outside the container, so it survives restarts, crashes and
image upgrades. `restart: unless-stopped` brings the app back after a crash or
a reboot. Writes are committed to disk before they are acknowledged (SQLite
runs with `synchronous=FULL`), so stopping or killing the container never loses
saved changes. The image runs as the non-root user 65532 and has a health check
that calls `horizonte -healthcheck`.

To keep the database in a host folder instead of a named volume, mount a
directory (not the `.db` file: SQLite also keeps `-wal` and `-shm` files next
to it), writable by the container user, uid 65532:

```sh
docker run -d --restart unless-stopped -p 127.0.0.1:8347:8347 \
  -v /srv/horizonte:/data horizonte:latest
```

Build for both architectures (push to a registry, since a local image store
holds one platform per tag):

```sh
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=$(git rev-parse --short HEAD) -t <registry>/horizonte:latest --push .
```

The app has no login, so publish the port on `127.0.0.1` (as above) or put it
behind an authenticating reverse proxy. `compose.yaml` passes `SENTRY_DSN` and
`SENTRY_ENVIRONMENT` from your environment; leave them unset to send nothing.
Backups, restore and Sentry are described in [docs/data-and-privacy.md](docs/data-and-privacy.md).

## Data and privacy

- Your data lives in one SQLite file (`horizonte.db` by default, plus `-wal` and `-shm` files while the app runs). Nothing is sent anywhere unless you set `SENTRY_DSN`; error reporting is off by default and, when on, sends stack traces, route names and timings, server and browser logs and metrics, and masked Session Replays (10% of browser sessions, all of them in development, plus the minute before an error). Collection is restricted so that figures are never sent.
- The app copies the database automatically (every 24 hours, and before upgrading its schema), keeping the last 7 copies next to the database.
- To move data between machines, copy a backup file, or export your plans as JSON on the Scenarios page.

Where data lives, how to restore a backup, what a JSON export contains and what Sentry sends: [docs/data-and-privacy.md](docs/data-and-privacy.md). The JSON file format: [docs/plan-format.md](docs/plan-format.md).

## Test

```sh
go test ./...
```

CI (`.github/workflows/ci.yml`) runs `go build ./...`, `go vet ./...`, `go test ./... -count=1`, staticcheck and govulncheck on every push and pull request. Load tests with k6 are in [perf/](perf/README.md).

## Development

[docs/development.md](docs/development.md) covers `-dev` mode, how templates, static files and asset versioning work, adding translations and pages, the tests and the code layout. The visual language is in [ui/DESIGN.md](ui/DESIGN.md).

## Project layout

```
.github/workflows/   CI: build, vet, tests, staticcheck, govulncheck
cmd/server/          web server entrypoint, backups, logging, Sentry setup, health check
cmd/dataset/         refreshes the embedded historical market data
docs/                data and privacy, plan file format, assumptions, development guide
internal/domain/     plan model: people, accounts, income, expenses, debts, prepayments, milestones, net worth tracking
internal/engine/     pure simulation: monthly projection, withdrawals, taxes, Monte Carlo, backtests
internal/tax/es/     Spanish IRPF
internal/pension/es/ Spanish public pension estimate
internal/history/    embedded historical market data (Shiller)
internal/backup/     JSON import and export of plans and net worth history
internal/xls/        minimal .xls reader used by cmd/dataset
internal/i18n/       message catalogs (locales/*.json) and EUR/number formatting
internal/store/      SQLite connection, repositories, SQL migrations and automatic backups
internal/web/        routes, handlers, middleware, simulation cache, template rendering
ui/templates/        layout, partials and pages
ui/static/           CSS, JS, fonts (JetBrains Mono) and vendored libraries
perf/                k6 load tests and a process sampler (see perf/README.md)
```

## Historical market data

Backtests use Robert Shiller's U.S. stock market data (S&P Composite total returns, 10-year
Treasury returns and CPI, monthly since 1871), converted into
`internal/history/shiller_monthly.csv` and embedded in the binary. The app never downloads it
at runtime. It is US data, whatever currency your plan is in.

To refresh it with the latest published data:

```sh
go run ./cmd/dataset update                       # downloads ie_data.xls from Shiller's Yale page
go run ./cmd/dataset update -url <url>            # another copy (e.g. Shiller's newer site)
go run ./cmd/dataset update -in ie_data.xls -source <url>   # a file you downloaded yourself
go run ./cmd/dataset update -out <file>           # write the CSV somewhere else
```

Then rebuild. The command checks the converted data (no gaps, plausible monthly values) before
replacing the CSV, and `go test ./internal/history` checks it against well-known years
(1931, 1974, 2008).

### Monte Carlo market models

Each plan chooses its model on the Chance of success page. `Random` (the default) draws
independent normal monthly returns with fixed inflation. `Historical blocks` strings together
contiguous 12-month blocks of this data, taking stocks, bonds and inflation from the same
months, with each series shifted so its long-run mean matches the plan's expected return and
inflation.

## Acknowledgements

Horizonte is an independent project. Its ideas come from personal finance tools on the
market such as ProjectionLab, Monarch, Spendee and getquin, among other commercial and
open-source tools. It is not affiliated with or endorsed by any of them, and all product
names are trademarks of their respective owners.

The project was also built as a hands-on way to learn Claude Code: how to plan and build
software together with AI agents, how to get the most out of the different Claude models,
and how these new ways of working change the day-to-day development workflow.

## License

Horizonte is released under the [MIT License](LICENSE).

Bundled third-party files keep their own licenses: htmx (0BSD), Alpine.js, Pico.css and
the Sentry browser SDK (MIT), Apache ECharts (Apache 2.0) in `ui/static/vendor/`, and the
JetBrains Mono font (SIL Open Font License, see `ui/static/fonts/LICENSE-OFL.txt`). The
historical market data comes from Robert Shiller's published dataset.
