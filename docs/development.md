# Development guide

Horizonte is a Go web app with server-rendered HTML (`html/template`), htmx and Alpine.js.
There is no JavaScript build step: templates, CSS and JS are plain files that the binary
embeds. The visual rules are in [../ui/DESIGN.md](../ui/DESIGN.md); what the simulation
models is in [assumptions.md](assumptions.md).

## Run in development mode

Start the server from the repository root (it reads `./ui` relative to where you start it):

```sh
go run ./cmd/server -dev
```

With `-dev`:

- Templates are parsed again on every render, so edits to `ui/templates/**` show up on a
  reload.
- Static files are read from `./ui/static` on every request, unminified, uncompressed and
  with `Cache-Control: no-cache`, and their URLs have no version suffix.
- `SENTRY_ENVIRONMENT` defaults to `development` and Session Replay records every session
  (only matters when `SENTRY_DSN` is set).
- Go code is not reloaded: restart the server after changing it. After a restart the app
  warms its simulation cache before the port opens (up to about 60 s).

Without `-dev` the files come from the copy embedded in the binary (`ui/ui.go`,
`//go:embed templates static`), so a release needs a rebuild after any change under `ui/`.

Use a scratch database while developing: `go run ./cmd/server -dev -db /tmp/dev.db`.

## Templates and static files

- `ui/templates/layout.html` is the page shell. `ui/templates/partials/*.html` hold shared
  blocks (sidebar and top bar in `nav.html`, forms, simulation placeholders).
  `ui/templates/pages/<name>.html` is one page; its name (without `.html`) is how handlers
  refer to it. Every page is parsed together with the layout and all partials.
- A page file defines `{{define "content"}}`. It can define more named blocks for the
  fragments htmx swaps in.
- `s.page(w, r, status, "<name>", "<title key>", data)` (`internal/web/server.go`) renders
  the whole layout for a normal or boosted request, and only the `content` block for other
  htmx requests. Fragments are rendered with `s.render.render(w, r, status, "<name>",
  "<block>", data)`. Output is buffered, so a template error never sends half a page.
- Template functions (`internal/web/render.go`): `t` (translate), `money`, `money2`, `eur`,
  `eur2`, `percent`, `pct`, `number`, `month` (locale-aware formatting), `asset` (versioned
  static URL), `hasPrefix` and others.
- **Asset versioning.** In production `{{asset "/static/css/app.css"}}` becomes
  `/static/css/app.css?v=<12 hex characters>`, a hash of the file as served (after
  minification). A request whose `v` matches is cached for a year (`immutable`); other
  requests revalidate with an ETag. `url(/static/...)` references inside the CSS are
  rewritten with their hashes too. Always link static files with `asset`.
- Files are minified (CSS, JS, SVG), and gzip and brotli versions are made at startup
  (brotli in the background). Pages and fragments are compressed on the fly.
- **Reload after a deploy.** All static hashes combine into one version, sent as the
  `X-Horizonte-Assets` header and in `<meta name="horizonte-assets">`. If a boosted request
  answers with a different version, `app.js` does a real page load, so an open tab never
  mixes new HTML with old CSS and JS.
- **Content Security Policy.** Scripts must be files: no inline `<script>` and no `onclick`
  style attributes (`TestTemplatesHaveNoInlineScripts` fails on them). Use Alpine
  directives or code in `ui/static/js`. `<script type="application/json">` blocks (chart
  data) are allowed. External hosts are not: third-party libraries are vendored in
  `ui/static/vendor` (minified files, updated by hand).
- Charts: a `<div class="chart" data-chart="ID">` plus a JSON script with the same id;
  `ui/static/js/charts.js` has a builder for each chart `type`.

## Translations

The app is Spanish by default and English on request (`lang` cookie).

1. Add the key to **both** `internal/i18n/locales/es.json` and `en.json`. Keys are flat
   (`area.name`).
2. Use it in a template as `{{t "key"}}`; arguments are passed to `fmt.Sprintf`
   (`{{t "key" .Name .Age}}`), so use `%s` and `%d`. In strings that take arguments, a
   literal percent sign is written `%%`.
3. Use `money`, `percent`, `number`, `month` and friends for numbers and dates, never
   manual formatting: Spanish writes `1.234,56 €`, English `€1,234.56`.
4. Go code translates with `s.catalog.T(lang, key, args...)`.

Tests that enforce this (`internal/i18n/i18n_test.go`): `TestCatalogParity` (both catalogs
have exactly the same keys) and `TestFormatVerbs` (both languages use the same
placeholders in the same order). A key missing from English falls back to Spanish, and a
key missing from both is rendered as the key itself, so a typo in a template shows up as
text like `plan.titel` on the page, not as a failing test.

Form errors use shared keys (`form.error.required`, `.range`, `.choice`, ...) returned by
`Validate` methods in `internal/domain`.

## Adding a page

1. Create `ui/templates/pages/<name>.html` with `{{define "content"}}`.
2. Write the handler in `internal/web` and call `s.page(...)`. Get the active plan and
   language with `s.finCtx(r)`; build engine input with `s.loadInput`.
3. Register the route in `internal/web/server.go` (`mux.HandleFunc("GET /<name>", ...)`).
   Unknown paths fall through to the not-found page.
4. Add the sidebar link and icon in `ui/templates/partials/nav.html`, with a `nav.<name>`
   key in both locales. The active item is decided from `.Path`.
5. If it belongs on the home page, add a card in `ui/templates/pages/home.html`
   (`.offer-grid` is three columns wide, so keep the number of cards a multiple of three).
6. Anything that runs a long simulation should go through the simulation cache and
   placeholder pattern the Chance of success and Backtest pages use (`internal/web/simrunner.go`,
   `warmup.go`), so slow results do not block the page.
7. Add the keys in both locales, a handler test (see below), and update the README
   feature table and, for visual changes, `ui/DESIGN.md`.

## Tests

```sh
go test ./...                      # everything
go test ./internal/engine          # one package
go test ./internal/web -run Home   # tests by name
go test ./... -count=1             # without the test cache, as CI does
go vet ./...
```

CI (`.github/workflows/ci.yml`) also runs staticcheck and govulncheck at pinned versions
(`go run honnef.co/go/tools/cmd/staticcheck@<version> ./...`, the versions are in the
workflow's `env`).

- `internal/engine`, `internal/domain`, `internal/tax/es`, `internal/pension/es` are pure
  and unit-tested with tables; a pension test checks the cap table against the BOE text of
  disposición transitoria 34ª kept in `internal/pension/es/testdata`.
- `internal/web` tests build a real server on a temporary SQLite file
  (`newTestServer`, `do` in `server_test.go`) and issue HTTP requests against it.
- `internal/store` tests use temporary databases (opening one applies every migration).
- `internal/history` checks the embedded market data against well-known years.
- Load tests with k6 are separate: see [../perf/README.md](../perf/README.md).

## Code layout and conventions

- `internal/domain`: plan records and validation, amounts as `domain.Cents` (integer cents)
  and months as `domain.Month`. No I/O.
- `internal/engine`: `Simulate(Input) -> Result` is pure (no I/O, no globals); the
  projection, Monte Carlo and backtests all reuse it, with amounts in float euros.
- `internal/store`: SQLite through `modernc.org/sqlite` (pure Go, no CGO), a single
  connection in WAL mode. Schema changes are new files in `internal/store/migrations`
  named `NNNN_name.sql`, applied in order, each in its own transaction, and only forward.
  An existing database is backed up before pending migrations run.
- `internal/web`: routes, handlers, middleware (compression, security headers, same-origin
  check on writes, panic recovery), the simulation cache and rendering.
- `internal/backup`: the JSON file format ([plan-format.md](plan-format.md)). Changing it
  incompatibly means bumping `Version`.
- Logging and Sentry: logs use short stable messages with attributes, namespaced
  `horizonte.<domain>.<field>`, and never contain amounts, names or anything a person
  typed (conventions in `cmd/server/logging.go` and `internal/web/metrics.go`).
- The server code is the single source of truth for rules and constants: tax tables in
  `internal/tax/es/tables.go`, pension limits and tables in `internal/pension/es`. Update
  [assumptions.md](assumptions.md) when a rule changes.
- Keep comments for the why (a legal reading, a trade-off), not the what.

## Historical data

`go run ./cmd/dataset update` regenerates `internal/history/shiller_monthly.csv`; see the
README for its options. Rebuild afterwards, since the CSV is embedded.
