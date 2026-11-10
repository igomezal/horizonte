# Data and privacy

Horizonte keeps your data on your machine. This page says where it is, how it is
backed up and restored, how to move it, and exactly what the optional error
reporting sends.

## Where your data lives

- **The database.** One SQLite file, set with `-db` (default `horizonte.db` in the
  folder you start the server from; `/data/horizonte.db` in the Docker image). It holds
  every plan and its assumptions, the net worth history and baseline, and which plan is
  active. While the app runs, SQLite also keeps `horizonte.db-wal` and `horizonte.db-shm`
  next to it (write-ahead log mode). Copy or move the three files together, or use a
  backup (below), which is always one self-contained file.
- **Backups.** In a `backups` folder next to the database (see below).
- **In your browser.** The language choice is a `lang` cookie; the light/dark choice is
  `localStorage` (`theme`). Neither holds financial data.
- **Git and Docker.** `*.db`, `*.db-wal`, `*.db-shm` and `backups/` are in `.gitignore`
  and `.dockerignore`, so they are never committed or copied into an image.

By default the server listens on `127.0.0.1` only. The app has no login: if you publish
it on a network, put an authenticating reverse proxy in front of it. State-changing
requests from other websites are rejected (same-origin check), and a Content Security
Policy limits the page to its own files (plus your Sentry project when error reporting is
on).

The app makes no outbound requests. The only traffic that leaves the machine is the optional
Sentry reporting (the `-healthcheck` flag only makes a request to the server's own address). (The `cmd/dataset` developer command downloads Shiller's data when you
run it by hand.)

## Automatic backups

While it runs, Horizonte copies the database with SQLite's `VACUUM INTO`, which is safe
with the app in use. Each copy is written under a temporary name and renamed when it is
complete, so a file with the final name is never partial.

- When the app starts, if a copy is due, and then every 24 hours. A copy is due when the
  newest one is older than the interval (or there is none), so an app restarted more often
  than the interval still gets its backups.
- Before applying pending schema migrations to an existing database. If that copy fails,
  the app does not migrate and refuses to start. A brand-new empty database is not copied.
- A failed scheduled copy is logged (and reported to Sentry if enabled); the app keeps serving.

Copies are named `<database>-<UTC time>.db` (for example
`horizonte-20261109T030000.000Z.db`), with `-pre-vNNNN` added for a copy taken before
migrating from schema version NNNN. The last 7 are kept, counting both kinds. A
pre-migration copy replaces earlier pre-migration copies of the same schema version, so
repeated restarts after a failed migration do not push out older backups. Retention only
deletes files with these names.

| Flag | Default | Description |
|---|---|---|
| `-backup-dir` | `backups` next to the database | Where copies are kept |
| `-backup-interval` | `24h` | Time between copies; `0` disables the schedule (the pre-migration copy still happens) |
| `-backup-keep` | `7` | Number of copies to keep, at least 1 |

In Docker the folder is `/data/backups`, inside the volume, so **copy that folder
somewhere else too**: backups protect against mistakes and bad upgrades, not against
losing the disk.

### Restore a backup

Stop the app, replace the database with the copy, delete the old `-wal` and `-shm`
files if there are any, and start the app again. A copy from an older schema version is
migrated on start (and that migration makes its own pre-migration copy first):

```sh
cp backups/horizonte-20261109T030000.000Z.db horizonte.db
rm -f horizonte.db-wal horizonte.db-shm
```

With the Docker volume, while the container is stopped:

```sh
docker run --rm -v horizonte-data:/data alpine sh -c 'cd /data && cp backups/<copy>.db horizonte.db \
  && rm -f horizonte.db-wal horizonte.db-shm && chown 65532:65532 horizonte.db'
```

Migrations only move forward (there are no downgrades), so restore a backup made by the same or an older version of the app.

## Moving data between machines

| | Backup file (`.db`) | Plan export (`.json`) |
|---|---|---|
| What it holds | The whole database: all plans, net worth history and baseline, active plan | One plan, or all plans (see below) |
| How to restore | Replace the database file while the app is stopped | Scenarios page, "Import a plan": adds plans to the existing data |
| Effect on existing data | Replaces everything | Never overwrites: every plan in the file becomes a new plan |
| Between versions | The newer app migrates an older file | The file has a format version; a file from a newer format is rejected |

Use a backup file to move your whole installation. Use JSON to share a plan, edit it by
hand, or merge plans into another installation.

### What a JSON export contains

- **One plan** (the "Export" link on a plan's row): the plan's name, people, milestones,
  accounts, incomes, expenses, debts, early repayments and assumptions. It does **not**
  contain the net worth history, and the plan's ids, parent and active flag are not kept
  (importing creates a new plan).
- **All plans** (the "Export all plans" link): every plan, the scenario links between them
  (which plan was copied from which), which plan is active, and the net worth tracking
  history and saved baseline (`tracking`). The tracking section is only present when
  there is something to save.
- Neither contains the language or theme choice or the random seed of the Chance of
  success page (results are recalculated on import).

Importing creates every plan in the file in one step and activates the plan marked
active (or the first one). Tracking history is merged: months you already have are kept,
only missing months are added, and the file's baseline is used only if you have none.
Files are limited to 2 MiB, 50 plans per file and 500 records per list. The full format
is in [plan-format.md](plan-format.md).

## Error reporting (optional)

Horizonte can report errors and performance data to [Sentry](https://sentry.io), from
both the server and the browser. It is **off by default**: nothing is sent unless
`SENTRY_DSN` is set. Without it the browser Sentry script is not even added to the page.

```sh
SENTRY_DSN=https://<key>@<org>.ingest.de.sentry.io/<project> go run ./cmd/server
```

| Variable | Default | Description |
|---|---|---|
| `SENTRY_DSN` | empty (disabled) | Sentry project DSN |
| `SENTRY_ENVIRONMENT` | `production` (`development` with `-dev`) | Environment tag |
| `SENTRY_RELEASE` | git revision | Release tag, used only when the binary was not built with a release (see below) |

A release set at build time wins over `SENTRY_RELEASE`:
`go build -ldflags "-X main.release=horizonte@1.2.3"`. The Dockerfile always sets one
(`horizonte@<VERSION>`, `horizonte@dev` unless you pass `--build-arg VERSION=...`), so in
the Docker image `SENTRY_RELEASE` has no effect.

Because the app holds personal financial data, collection is restricted: no IP
addresses, user info, cookies, request bodies or query strings, no response headers, and
only these request headers keep their values: `User-Agent`, `Accept-Language`,
`HX-Request` and `HX-Boosted`. The server name is reported as `horizonte`, not your
machine's hostname. Stack traces, route names and timings are sent. Every request is
traced, except static files and `/healthz`.

With Sentry enabled:

- **Logs.** Server logs at info level and above (startup, schema migrations, plan
  created, activated or deleted, imports and exports, backups, blocked cross-origin
  posts, server errors) and a few browser logs (failed htmx requests, broken charts) are
  sent to Sentry Logs, linked to their traces. Logs carry IDs, counts and categories
  only, never amounts or names you typed; the conventions are documented in
  `cmd/server/logging.go`. Per-request access lines stay on stdout.
- **Browser console.** Console output (from the app, htmx and Alpine) goes to Sentry
  Logs only, and only when it can't contain figures: a console message with a currency,
  a decimal or grouped number, four or more digits in a row, HTML or a serialized object
  is not sent at all, and query strings are stripped from the rest. Console output is
  never attached to error events or replays.
- **Metrics.** Monte Carlo and backtest run time (with runs, horizon and CPU count),
  import and export outcomes, rejected forms per route, blocked cross-origin posts,
  database size and plan count at startup, and in the browser the time of htmx requests
  and chart rendering. Plan results such as success rates, ages or amounts are never
  sent; the conventions are in `internal/web/metrics.go`.
- **Session Replay.** 10% of browser sessions are recorded (all of them with `-dev` or
  `SENTRY_ENVIRONMENT=development`), plus the minute before any error. All text and
  input values are masked and all media is blocked, charts included, so a replay shows
  the page layout and where you clicked, never your figures. Network requests keep only
  method, status and size, and query strings are stripped from URLs.

Setting `SENTRY_DSN` also lets the page connect to that Sentry project (the Content
Security Policy adds its origin, and a worker started from a `blob:` URL for replay
compression). Nothing else is added.
