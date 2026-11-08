# Horizonte

A self-hosted, privacy-first financial and retirement planner.
It is built with Go and htmx, runs as a single binary, and stores data in a local SQLite file.
The UI is in Spanish (default) and English, and amounts are in EUR.

See [PLAN.md](PLAN.md) for the roadmap and phase status.

## Requirements

- Go 1.25+

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

After every start or restart, the app first prepares your plans' simulations, and until the port opens it does not answer. The port opens when that finishes, usually within seconds; past about 60 s it opens anyway and the remaining plans carry on in the background. Any plan left unprepared is calculated the first time you open it.

## Build

```sh
go build -o bin/server ./cmd/server
```

All templates, CSS, JS and vendored libraries (htmx, Alpine.js, ECharts, Pico.css) are embedded in the binary.

## Docker

```sh
docker compose up -d        # build and start on http://localhost:8347
```

The database is stored in the `/data` volume (`horizonte-data` in
`compose.yaml`), outside the container, so it survives restarts, crashes and
image upgrades. `restart: unless-stopped` brings the app back after a crash or
a reboot. Writes are committed to disk before they are acknowledged, so
stopping or killing the container never loses saved changes.

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
behind an authenticating reverse proxy. Back up by copying `/data` while the
container is stopped. Set `SENTRY_DSN` in the environment to enable error
reporting.

## Automatic backups

While it runs, Horizonte copies the database with SQLite's `VACUUM INTO`, which is safe with the app in use: when the app starts if a copy is due, and then every 24 hours. It also makes a copy before applying pending schema migrations to an existing database; if that copy fails, the app does not migrate and refuses to start. A failed scheduled copy is logged and reported, and the app keeps serving.

The last 7 copies are kept in a `backups` directory next to the database, named `<database>-<UTC time>.db` (with `-pre-vNNNN` for a copy taken before migrating from schema version NNNN). In Docker that is `/data/backups`, inside the volume, so **copy that directory somewhere else too**: it protects against mistakes and bad upgrades, not against losing the disk.

| Flag | Default | Description |
|---|---|---|
| `-backup-dir` | `backups` next to the database | Where copies are kept |
| `-backup-interval` | `24h` | Time between copies; `0` disables the schedule (the pre-migration copy still happens) |
| `-backup-keep` | `7` | Number of copies to keep |

To restore, stop the app, replace the database with the copy, delete the old `-wal` and `-shm` files if there are any, and start it again. A copy from an older schema version is migrated on start:

```sh
cp backups/horizonte-20261109T030000.000Z.db horizonte.db
rm -f horizonte.db-wal horizonte.db-shm
```

With the Docker volume, while the container is stopped:

```sh
docker run --rm -v horizonte-data:/data alpine sh -c 'cd /data && cp backups/<copy>.db horizonte.db \
  && rm -f horizonte.db-wal horizonte.db-shm && chown 65532:65532 horizonte.db'
```

## Net worth tracking

The Tracking page (`/tracking`) records what you really own and owe, one entry per calendar month, and compares it with a baseline: a plan's projected month-by-month path, saved on purpose, with one baseline at a time. History and baseline belong to the household, not to a plan, so copying or deleting plans leaves them alone. Everything is in nominal euros, since the app has no record of actual inflation.

The history is included in the all-plans JSON export. Importing such a file only adds months that are missing and keeps the existing baseline unless there is none.

## Error reporting (optional)

Horizonte can report errors and performance data to [Sentry](https://sentry.io), from both the server and the browser.
It is **off by default**: nothing is sent unless `SENTRY_DSN` is set.

```sh
SENTRY_DSN=https://<key>@<org>.ingest.de.sentry.io/<project> go run ./cmd/server
```

| Variable | Default | Description |
|---|---|---|
| `SENTRY_DSN` | empty (disabled) | Sentry project DSN |
| `SENTRY_ENVIRONMENT` | `production` (`development` with `-dev`) | Environment tag |
| `SENTRY_RELEASE` | git revision | Release tag; can also be set with `-ldflags "-X main.release=horizonte@1.2.3"` |

Because the app holds personal financial data, collection is restricted: no IP addresses, cookies, request bodies or query strings, no browser console output, and only a few harmless request headers.
Stack traces, route names and timings are sent.

With Sentry enabled, server logs at info level and above (startup, schema migrations, plan created/activated/deleted, imports and exports, blocked cross-origin posts, server errors) and a few browser logs (failed htmx requests, broken charts) are also sent to Sentry Logs, linked to their traces.
Logs carry IDs, counts and categories only, never amounts or names you typed; the conventions are documented in `cmd/server/logging.go`.
Per-request access lines stay on stdout.
Browser console output (from the app, htmx and Alpine) is sent too, but only when it can't contain figures: a console message with a currency, a decimal or grouped number, four or more digits in a row, HTML or a serialized object is not sent at all, and query strings are stripped from the rest.

Sentry Metrics cover what traces and issues can't show: Monte Carlo and backtest run time (with runs, horizon and CPU count), import and export outcomes, rejected forms per route, blocked cross-origin posts, database size and plan count at startup, and in the browser the time of htmx requests and chart rendering.
Plan results such as success rates, ages or amounts are never sent; the conventions are in `internal/web/metrics.go`.

Session Replay records 10% of browser sessions (all of them with `-dev` or `SENTRY_ENVIRONMENT=development`), plus the minute before any error.
All text and input values are masked and all media is blocked, charts included, so a replay shows the page layout and where you clicked, never your figures.
Network requests keep only method, status and size, and query strings are stripped from URLs.

## Selling property

A real-estate account can be sold, fully or partly, at a date or milestone. The money, less selling costs, goes to the account you choose (by default the one that receives surplus money). With the Spanish tax model the gain over its cost goes to the savings base and is paid with the tax return the following June, and if the plan ends before that June, the tax is charged in its last month. A main residence sold by an owner over 65 is exempt; reinvestment relief is not modelled. The flat tax model does not tax the sale.

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

### Monte Carlo market models

Each plan chooses its model on the Chance of Success page. `Random` (the default) draws
independent normal monthly returns with fixed inflation. `Historical blocks` strings together
contiguous 12-month blocks of this data, taking stocks, bonds and inflation from the same
months, with each series shifted so its long-run mean matches the plan's expected return and
inflation.

## Adding translations

Add the key to **both** `internal/i18n/locales/es.json` and `en.json`. A test fails if the catalogs differ.
In templates use `{{t "key"}}`, plus `{{money v}}`, `{{percent v 1}}` and `{{number v 2}}` for locale-aware formatting.

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
Instrument Sans font (SIL Open Font License, see `ui/static/fonts/LICENSE-OFL.txt`). The
historical market data comes from Robert Shiller's published dataset.
