# Horizonte

A self-hosted, privacy-first financial and retirement planner.
It is built with Go and htmx, runs as a single binary, and stores data in a local SQLite file.
The UI is in Spanish (default) and English, and amounts are in EUR.

See [PLAN.md](PLAN.md) for the roadmap and phase status.

## Requirements

- Go 1.22+

## Run

```sh
go run ./cmd/server
# open http://127.0.0.1:8080
```

Flags:

| Flag | Default | Description |
|---|---|---|
| `-addr` | `127.0.0.1:8080` | Listen address. Localhost only by default, so your data is not exposed on the network |
| `-db` | `horizonte.db` | SQLite database file (git-ignored) |
| `-dev` | `false` | Read templates and static files from `./ui` on every request, so edits show up without restarting |

## Build

```sh
go build -o bin/server ./cmd/server
```

All templates, CSS, JS and vendored libraries (htmx, Alpine.js, ECharts, Pico.css) are embedded in the binary.

## Test

```sh
go test ./...
```

## Project layout

```
cmd/server/          entrypoint
internal/i18n/       message catalogs (locales/*.json) and EUR/number formatting
internal/store/      SQLite connection and SQL migrations
internal/web/        routes, handlers, middleware, template rendering
ui/templates/        layout, partials and pages
ui/static/           CSS, JS and vendored libraries
```

## Historical market data

Backtests use Robert Shiller's U.S. stock market data (S&P Composite total returns, 10-year
Treasury returns and CPI, monthly since 1871), converted into
`internal/history/shiller_monthly.csv` and embedded in the binary. The app never downloads it
at runtime.

To refresh it with the latest published data:

```sh
go run ./cmd/dataset update                       # downloads ie_data.xls from Shiller's Yale page
go run ./cmd/dataset update -url <url>            # another copy (e.g. Shiller's newer site)
go run ./cmd/dataset update -in ie_data.xls -source <url>   # a file you downloaded yourself
```

Then rebuild. The command checks the converted data (no gaps, plausible values) before
replacing the CSV, and `go test ./internal/history` checks it against well-known years
(1931, 1974, 2008…).

## Adding translations

Add the key to **both** `internal/i18n/locales/es.json` and `en.json`. A test fails if the catalogs differ.
In templates use `{{t "key"}}`, plus `{{money v}}`, `{{percent v 1}}` and `{{number v 2}}` for locale-aware formatting.
