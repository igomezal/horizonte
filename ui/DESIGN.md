# Design system: "Billete"

The visual language of the app. Every phase follows these rules. Tokens live in
`ui/static/css/app.css`.

## Subject

A private planner for one person in Spain who models their whole financial life, year by
year. Figures and the timeline of a life are the content; everything else steps back.

## Color: the euro banknotes

Each color has **one fixed meaning**. Never use a color for decoration.

| Token | Light | Dark | Meaning |
|---|---|---|---|
| `--papel` / `--app-bg` | `#f4f6f8` | `#0f1a2b` | Page background (cool paper / deep ink) |
| `--tinta` / `--app-text` | `#16233a` | `#e3e8f0` | Text; sidebar background (passbook cover) |
| `--azul-20` / `--app-accent` | `#2f5da8` | `#7fa6e6` | UI accent (links, focus, active); **money you put in** |
| `--naranja-50` / `--app-growth` | `#e39a2d` | `#f0b357` | **Money your money earned** (growth). Data only, never a UI accent |
| `--verde-100` / `--app-positive` | `#3e8e5a` | `#6cc28b` | Positive values, goals reached |
| `--rojo-10` / `--app-negative` | `#b8433d` | `#e57b74` | Negative values, shortfalls, debt |

- Chart series follow the banknote order `--chart-1` … `--chart-5`: 20 € blue, 50 € orange,
  100 € green, 10 € red, 200 € ochre.
- When a chart has two or three series, explain colors with **swatches inside the sentence**
  (`.swatch`) and hide the ECharts legend (`hideLegend: true`).
- No gradients, no shadows on content. The only shadow is the mobile drawer.

## Type: Instrument Sans (one family)

Self-hosted variable font (`ui/static/fonts/`, weights 400–700, width 75–100 %).

| Use | Size | Weight / width |
|---|---|---|
| Body | 16px | 400 |
| Small, meta | 14px | 400 |
| h3 / row titles | 16px | 600 |
| h2 section | 21px | 600 |
| h1 page | 28px | 600 |
| Lead sentence above a figure | 21px | 500, muted |
| **Hero figure** (`.figure`) | 48–64px | 650, **width 75 %**, tabular |

- **Euro amounts are the one bold element.** Big amounts use `.figure`; amounts in text
  or tables use `.amount` (tabular, 600). Always format with `money`/`money2`, never by hand.
- Sentence case everywhere. **No ALL-CAPS labels**, no eyebrow labels above headings.
- Prose lines stay under `--app-measure` (68ch).

## Layout

- Left-aligned content, max width 1100px, sidebar 232px (drawer below 860px).
- **No card kit.** Separate sections with space plus one rule (`.section`), not boxes.
- Lists of named things use ledger rows (`.ledger` / `.ledger-row`): name on the left,
  description or amount on the right, a hairline between rows.
- Radius 6px on controls only (buttons, inputs, switches). Content blocks have no radius.

## Editable lists and forms (phase 1)

- **Entry rows** (`.entry`): name in bold with plain meta facts beside each other underneath
  (no separators), the amount right-aligned and tabular, then text actions ("Editar",
  "Eliminar") as `.link-btn`. Rows are separated by hairlines.
- **Delete is confirmed inline** in the row ("¿Eliminar? Sí, eliminar / No"), never with a
  browser dialog.
- **Inline forms** (`.entry-form`) replace the row being edited, or open under the list for a
  new record. They are the one raised surface: `--papel-raised` with a 3px accent bar on the
  left. Fields sit in an auto-fitting grid; the label goes above, then help text or the error
  below.
- **Money and percentages** are text inputs with `inputmode="decimal"` and a unit inside the
  field (€, %). Users type their own format ("1.250,50"); the server parses it.
- The submit button of a new form repeats the add action ("Añadir cuenta"); editing says
  "Guardar cambios". Errors appear next to their field and the first invalid field gets focus.
- Each section has a one-line description and an empty state that says what to add first.

## Projections (phase 2)

- **The verdict leads.** A plan page opens with one sentence that answers the question
  ("Con estas hipótesis, tu dinero llega hasta los 90 años") above the hero figure. When the
  plan fails, the figure becomes the age the money runs out, in `--app-negative`.
- **Today's euros by default.** Long horizons are shown deflated; nominal values are one
  toggle away and every label says which unit is shown.
- **View toggles** are `.segmented` link groups (`aria-current="true"` marks the active one).
  They update the URL with `hx-push-url`, so a reload or a shared link keeps the view.
- **Projection chart:** account balances stack in the non-signal banknote colors
  (20 € → 50 € → 100 € → 200 €), debt is a red area below zero, net worth is a 2.5px line
  in the text color, and the month the money runs out is a dashed red marker.
- **Data tables** (`table.projection`): tabular figures, right-aligned amounts, sticky
  header, hairline rows, scrolling inside `.table-wrap` (never the page). Negative amounts
  are red; periods with a shortfall get a faint red row tint.
- **Settings that drive a result recalculate live** (`hx-trigger="change"`), with a quiet
  status line ("Guardado. El plan se ha recalculado.") instead of a save button.

## Motion

- No entrance animations or hover transitions on content.
- Motion only answers an action: drawer open/close, expanding a row, confirming a save.
- Respect `prefers-reduced-motion`.

## Writing

- Name things by what the user does: "Añadir cuenta", not "Nuevo registro".
- Buttons say exactly what happens ("Guardar cambios"), and the confirmation reuses the verb
  ("Cambios guardados").
- Empty states say what to do next. Errors say what went wrong and how to fix it, without
  apologizing.
- Every string goes in both `locales/es.json` and `locales/en.json`.

## Quality floor

Works at phone width, visible keyboard focus (`:focus-visible`), AA contrast in both themes,
and both themes checked with screenshots before a phase is committed.
