# Proyección

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
| `-db` | `projection.db` | SQLite database file (git-ignored) |
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

## Adding translations

Add the key to **both** `internal/i18n/locales/es.json` and `en.json`. A test fails if the catalogs differ.
In templates use `{{t "key"}}`, plus `{{money v}}`, `{{percent v 1}}` and `{{number v 2}}` for locale-aware formatting.
