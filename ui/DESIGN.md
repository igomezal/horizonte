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
