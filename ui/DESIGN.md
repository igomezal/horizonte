# Design system: "Monitor"

The visual language of the app. Every phase follows these rules. Tokens live in
`ui/static/css/app.css`; the look is a calm monitoring dashboard (navy surfaces, one blue
accent, small status chips, monospace numbers) applied to a planner, so the monitoring
vocabulary is used **only for states the app really has**.

## Subject

A private planner for one person in Spain who models their whole financial life, year by
year. Figures and the timeline of a life are the content; the chrome steps back.

## Tokens

Defined on `:root` and redefined for dark twice (`:root[data-theme="dark"]` and
`prefers-color-scheme` when no theme is chosen). Pico's own variables are mapped on
`:root:root` so they beat Pico's defaults. Templates and charts use the semantic names
(`--app-*`, `--series-*`, `--chart-*`).

| Token | Light | Dark | Meaning |
|---|---|---|---|
| `--bg` / `--app-bg` | `#F5F7FA` | `#0A0F1E` | Page (never pure white or black) |
| `--surface` | `#FFFFFF` | `#111827` | Cards, sidebar, top bar, charts |
| `--inset` | `#F1F5F9` | `#0B1220` | Metric tiles, chips, inline forms |
| `--border` / `--app-rule` | `#E2E8F0` | `#1F2937` | 1px borders and hairlines |
| `--text` / `--app-text` | `#0F172A` | `#F1F5F9` | Text |
| `--muted` / `--app-muted` | `#475569` | `#94A3B8` | Secondary text |
| `--accent` | `#2563EB` | `#3B82F6` | Focus ring, active nav bar, status dot, chart series 1 |
| `--accent-fill` | `#2563EB` | `#2563EB` | Fill behind white text (primary button, active segment, language switch): 5.17:1; hover `#1D4ED8` |
| `--accent-text` | `#1D4ED8` | `#60A5FA` | Links and accent words (passes AA as text) |
| `--status-critical` (`--critical-color`) | `#DC2626` | `#DC2626` | Critical fills and dots; text `#B91C1C` / `#F87171` |
| `--status-warning` | `#F59E0B` | `#F59E0B` | Warning dots; text `#B45309` / `#F59E0B` |
| `--status-normal` (`--live-indicator`) | `#22C55E` | `#22C55E` | Ready / online dot; text `#15803D` / `#22C55E` |
| updating | accent blue | accent blue | Pulsing dot while a calculation runs |

Required variables: `--pulse-animation: pulse 2s infinite`, `--alert-z-index: 1000`,
`--live-indicator`, `--critical-color`, `--update-interval: 5s` (declared; nothing in the app
auto-refreshes, so it is unused), `--toast-duration: 5s` (read by `app.js`).

- `--app-positive` is the "normal" text colour, `--app-negative` the critical text colour. In
  data tables **only negatives are red** (`negative` class); positives are never green.
- Money you put in is `--app-contributed` (blue, `--series-1`); money your money earned is
  `--app-growth` (teal, `--series-2`). Green, red and orange are status colours and never
  identify data series.
- Gold/yellow is not an accent. Critical uses `#DC2626`, not pure red (saturation cap).
- Layers: base 0, sticky nav 100 (`--z-nav`), overlay 200 (drawer, simulation card), modal
  300, toast 500; the offline toast uses `--alert-z-index`.
- Radius 8px (`--radius`) for buttons, inputs, cards, tiles. Chips are pills.
- Shadows: cards `0 2px 12px rgba(0,0,0,.06)` (stronger in dark), `--shadow-lift` on hover
  of clickable cards and on toasts. No outer glows.

## Type

System UI stack for everything textual (`system-ui, -apple-system, "Segoe UI", Roboto`).
**JetBrains Mono** (self-hosted latin variable subset, `ui/static/fonts/`, OFL licence next to
it) for status chips, metric values, euro hero figures, table numbers and chart labels.

| Use | Size | Weight |
|---|---|---|
| Body | 16px / 1.6 | 400 |
| Labels | 14px | 500 |
| h3 / row titles | 16px | 600 |
| h2 section | 24px | 600 |
| h1 page | 36px, letter-spacing -0.025em | 700 |
| Hero figure (`.figure`) | 48-64px, mono | 700 |
| Status chip (`.chip`) | 12px mono, uppercase, 0.08em | 500 |

- Euro amounts in tables and tiles are mono and tabular; format with `money`/`money2`.
- Sentence case everywhere **except the small mono status chips**, which are uppercase.
  Headings, buttons and body never are. No eyebrow labels above headings.
- Prose lines stay under `--app-measure` (68ch).

## Layout and components

- Left sidebar 232px on `--surface` with a right border (drawer below 860px); sticky top bar
  on `--surface` with the connection chip, language switch and theme toggle. Content max
  1600px; density stays high (no landing-page section gaps).
- **Cards** (`.card`, `.section`, `.fin-summary`, `.plan-summary`, `.cf-year`, result
  blocks): surface, 1px border, 8px radius, card shadow. Cards never scale on hover; only
  clickable cards (`.card-link`, the home "what you can do" links) get `scale(1.03)` and a
  shadow lift, and not under reduced motion.
- **Status chips** (`.chip` + `chip-live` / `chip-updating` / `chip-warning` /
  `chip-critical`): dot plus mono uppercase label.
- **Metric tiles** (`.metrics` > `.metric`): inset tile, mono value coloured by meaning
  (`is-green`, `is-blue`, `is-teal`, `is-orange`), muted label under it.
- **Primary button**: accent fill, white text, 8px radius, 12px/20px padding, weight 600;
  hover darkens and lifts a little; active `translateY(1px)`. **Ghost** (`.btn-ghost`,
  `.add-btn`): 1.5px muted border, accent text, subtle fill on hover. **Destructive
  confirm** (`.confirm-yes`): critical red.
- **Inputs**: label above, 1px border, 2px accent focus ring offset 2px, error below in
  critical red (`.field-error`).
- **Navigation**: the active item has a 3px accent bar, a faint accent fill and weight 500.
- **Skeletons** (`.skel`, `.skel-t`) shimmer; there are no spinners except the tiny one inside
  a busy button.
- **Icons**: inline Lucide-style SVG, `stroke="currentColor"`, `aria-hidden="true"`. No emojis.
- Lists of named things use ledger rows (`.ledger`).
- **Home hero**: text left (chip, headline with one accent phrase, tagline, primary button
  with an arrow icon), chart card right with metric tiles; built from the example the page
  already computes.

## Status, live and motion: what the app really does

This is a planner, not a server monitor: no live data, polling, WebSockets or fake metrics.

| State | Display |
|---|---|
| Calculation running (`.is-pending`, `.htmx-request`) | Floating card with a pulsing blue dot, message and seconds; chip "Calculando…" |
| Result ready | Green chip "Resultado listo"; it becomes "Calculando…" with the pulsing blue dot while its region recalculates |
| Plan fails / shortfall | Red chip "Faltan fondos", red figure and wording |
| Example data (home) | Neutral chip "Ejemplo" (muted dot, no pulse): a fact, not a live state |
| Connection | Top-bar chip: green "En línea" / red "Sin conexión" |

- **Connection indicator** (client-side only, `app.js`): offline when `navigator.onLine`
  is false or an htmx request fails with `htmx:sendError`; online again on the `online` event
  or the next answered request. A fixed toast (`role="status"`, `aria-live="polite"`,
  dismiss button, hides after `--toast-duration`) announces both changes. No server
  involvement, no polling.
- **Result fade** (`app.js`, Web Animations API on `htmx:load`; htmx re-applies attributes
  when it settles a swap, so CSS classes would be undone): a result that replaces a
  *pending placeholder* (`.is-pending`) fades in from opacity 0, 260ms ease-out. A
  *recalculation* replaces a result the reader is looking at, so it crossfades from 0.45 and
  the card never blanks (the old results are already dimmed to 0.3 while it runs).
  `beforeSwap` tells the two apart by whether the swap target was `.is-pending`.
- **Entry motion**: fade + translateY(16px to 0), 480ms ease-out, 100ms stagger between
  top-level blocks, **only** on full page load and boosted navigation (`html.enter`, added by
  `theme-init.js` / `app.js` and removed after 1.4s). Result regions (`.sim-region`,
  `#plan-summary`, `#plan-table`) keep their entry like every other block; when a result is
  swapped in during the entry, `html[data-enter-skip]` (the swapped ids) stops the entry animation on that region
  only, so the swapped result does not replay it and the rest of the page keeps fading in
  (any other swap removes `html.enter`). Only opacity and transform move. Hover transitions
  are 200ms.
- `prefers-reduced-motion`: no entry animation, no result fade, no pulses, no hover scale, no toast
  transition; pending and status messages stay visible.
- No count-up animation.

## Editable lists and forms (phase 1)

- **Entry rows** (`.entry`): name in bold with plain meta facts beside each other underneath
  (no separators), the amount right-aligned and tabular, then text actions ("Editar",
  "Eliminar") as `.link-btn`. Rows are separated by hairlines; the sections holding them are cards.
- **Delete is confirmed inline** in the row ("¿Eliminar? Sí, eliminar / No"), never with a
  browser dialog.
- **Inline forms** (`.entry-form`) replace the row being edited, or open under the list for a
  new record. They sit on an inset tile (`--inset`, 1px border, 8px radius) with a 3px accent bar on the
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
- **Projection chart:** account balances stack in the series palette (in account order, each a 1.5px line over a faint wash),
  debt is a faint red area below zero with a solid 0 line, net worth is a 2px ink line with its
  final value at the end, and the month the money runs out is a dashed red marker. The
  tooltip leads with net worth, then each account top-down like the stack.
- **Data tables** (`table.projection`): tabular figures, right-aligned amounts, sticky
  header, hairline rows, scrolling inside `.table-wrap` (never the page). Negative amounts
  are red; periods with a shortfall get a faint red row tint.
- **Milestones (phase 3)** appear twice on the Plan: as a chronological timeline under the
  verdict (`.timeline`; a 2px left bar in ink for milestones, green for debts paid off, red
  for money running out, a pale bar for milestones never reached) and as vertical marks on
  the chart (dashed ink / dotted green / dashed red). Mark labels sit in a band above the
  plot: marks in the same period share one label, and each label takes the first row
  where it does not collide with its neighbour. Label width is estimated for the monospace
  font (0.62 em per character plus padding). The band has at most 4 rows (2 when the chart is
  under 640px wide, so the plot keeps its height); a
  mark that does not fit keeps its dashed line and loses its label (still in the tooltip). Priority when rows run out: money runs out, age and FI milestones, date milestones and sales, debts paid off last (`chartMark.Pri`). Names are only cut when a
  row cannot hold them (48 characters, 18 on narrow charts, with an ellipsis). The full names
  of a period's milestones are the first lines of the chart tooltip for that period, and the
  timeline above lists them all. A label that would cross the left edge is left-aligned at its
  mark instead of centred.
- **Fields that depend on a choice** (milestone kind, "Desde" a milestone or a date) are
  shown and hidden with Alpine (`x-show`); the server only reads the fields of the chosen
  option, so hidden values never cause errors.
- **Settings that drive a result recalculate live** (`hx-trigger="change"`), with a quiet
  status line ("Guardado. El plan se ha recalculado.") instead of a save button.

## Cash flow (phase 4)

- **One year at a time**, chosen with a slider (previous/next buttons, year labels at both
  ends) or by clicking a bar in any yearly chart; the URL keeps the year.
- **The hero figure is the savings rate** (savings ÷ net income). A year without income shows
  what came out of savings instead, and says so in words.
- **Sankey colours:** income green, money drawn from savings ochre (200 €), gaps and debts
  red, money going into accounts blue, tax neutral ink, and each expense category in its
  `--cat-*` colour (as in the stacked chart). The centre node has its own token,
  `--sankey-center` (slate `#334155` light, `#CBD5E1` dark), so it never reads as "otros"
  (`--series-other`), and no category uses the ochre of "money drawn from savings". There is no gradient: every link takes the colour of its outer node at
  opacity 0.28. The In/Out ledger swatches use the same colours (`dot-cat-<key>`; the
  category key travels in `cfLine.Cat`). Flows under 2,5 % of the total have no label on the diagram; the ledger below always lists
  every flow with its share.
- **Yearly bar charts:** savings rate in blue (`--app-contributed`, money put away) for
  positive years and red for negative ones, the tooltip swatch included (no bar when there is
  no income).
  Years below −100 % are cut at −100 % (lighter, square end) with a note above the chart, so
  one extreme year never flattens the rest; the tooltip keeps the real value. Spending is
  stacked by category, each category in its fixed colour, debts in red on top.
- **Empty states:** with no expenses, "Gastos por categoría" shows a sentence with a link to
  Finanzas actuales (`cf.cats_empty`); with no income the savings-rate card does the same, and
  the Sankey does when it has no flows (`cf.rate_empty`, `cf.sankey_empty`). Each says what is
  missing first, then what to add.

## Chance of success (phase 5)

- **The hero figure is the share of scenarios where the money lasts**, followed by one sentence
  about a bad scenario (1 in 10): what is left, or the age the money runs out. No traffic-light
  colouring of the percentage; the words carry the judgement.
- **Uncertainty is drawn without signal colours:** the fan uses two blue washes (80 % and
  50 % of scenarios) and an ink median labelled with its final value; "Tu plan" is the dashed
  orange line. The survival curve is a blue step line; when every scenario lasts every year it
  is replaced by one sentence (a flat 100 % line says nothing).
- **Results are reproducible** (fixed seed) until the user asks for "otros escenarios"; the
  refresh URL carries the seed so settings changes keep the same scenarios.
- State limits plainly under the results (fixed inflation, correlation used).

## Historical backtest (phase 6)

- **Same verdict pattern:** the share of historical start years that work, then the worst year
  in words. When history is clearly kinder than the plan's assumptions, say so in a `.callout`
  (inset tile, accent bar) right under the verdict: it changes how the number should be read.
- **Crises are a ledger** (`.entry.crisis`): name, start month and a one-line description, the
  outcome on the right (end value, or "se acaba a los N años" in red) with the lowest point.
- **Start-year heat-map:** rows are decades, columns the year within the decade, each cell
  labelled with its two digits. Green = money lasts (stronger = more left), red = money runs
  out (stronger = earlier). Clicking a cell or "Ver" replays that scenario in place.
  Start years whose plan outruns the data are **partial**: paler (half opacity) with a dotted
  muted outline, a tooltip saying how many years were historical, and they are counted on
  their own line, never in the headline percentage.
- **Replayed scenario chart:** the scenario as an ink line with a light ink area and its final
  value at the end, "Tu plan" dashed in orange, a dotted marker where historical data ends and
  a dashed red one where the money runs out. The heat-map is capped at 820px wide so its cells
  stay close to square.

## Scenarios (phase 7)

- **One active plan drives every page.** When there is more than one plan, the top bar shows a
  compact "Escenario" select that switches the active plan and stays on the current page.
- **The comparison is a table first** (`table.scenarios`): one row per plan with its outcome,
  end value, chance of success, historical result and FIRE age; the active row is tinted with
  the accent and carries an "Activo" badge. Row actions (Activar, Renombrar, Eliminar) sit
  under the name so the table never needs horizontal scrolling for them.
- **The comparison chart** draws each plan's net worth on the same axes, each plan in its
  series colour by its place in the list, the active plan's line thicker. With up to four
  plans, each line ends with the plan's name and final value.
- Creating a scenario is a plain form (name, source plan, assumption preset) and the new
  scenario becomes the active one, so the next step is simply editing it.

## Withdrawals (phase 8)

- The strategy lives with the other assumptions on the Plan page and recalculates live; fields
  that only matter for some strategies appear with them (Alpine `x-show`), each strategy has a
  one-line plain explanation under the select.
- **Reordering** uses a numbered list with ↑ ↓ icon buttons (disabled at the ends, labelled for
  screen readers) instead of drag and drop; every move saves.
- The verdict gains one muted sentence with the strategy's effect: the range of spending as a
  share of the plan and the year of the lowest point.

## Charts

- **Palette** `--series-1` ... `--series-7`: blue, teal, orange, violet, ochre, pink, indigo
  (dark values are separate steps). Series take slots in order and never cycle: past seven
  they are `--series-other` (gray). Blue and teal come first so "money you put in" and "money
  it earned" differ for colour-blind readers and are never status colours.
- **Validated** with the dataviz palette checker (`validate_palette.js`) on the card surfaces
  (`#FFFFFF` light, `#111827` and `#0A0F1E` dark), adjacent pairs; lightness band, chroma
  floor, CVD separation, normal-vision floor and contrast all PASS:
  - light `#2563EB #0D9488 #C2410C #7C3AED #B58105 #DB2777 #4F46E5`: worst adjacent CVD pair
    pink/ochre dE 11.8 (deutan; tritan 6.4); worst normal-vision pair blue/teal dE 22.6.
  - dark `#3B82F6 #13A8A0 #D9692A #8B7CF0 #BC8A0A #D9669A #6F78E8`: worst adjacent CVD pair
    indigo/pink dE 12.7 (protan; tritan 5.2, in the 4-6 warn band, covered by legends, direct
    labels and tooltips); worst normal-vision pair blue/teal dE 18.9.
  Blue next to violet was tried and rejected (dE 1.3 under protan simulation). Re-run the
  checker if a value changes.
- **Green and red mean good and bad** (income, debt, money running out), never identity. The
  savings rate is blue because it is money you put away, not a verdict.
- **Expense categories have their own palette** (`--cat-*`, not the series slots), fixed
  wherever they appear (stack, Sankey, ledger). Any category can be absent from a plan, so any
  two can sit next to each other, and each can touch the debt red: the palette was searched and
  validated over **all 36 pairs** of the seven categories, "otros" and the debt red (not just
  stack neighbours), in normal vision and under protan, deutan and tritan simulation. It has
  no red, orange, gold/ochre, green or pink (status colours, the debt red, the money-drawn-from-
  savings ochre, and a hue the user rejected); it uses cool hues plus lightness steps. Stack
  order = slot order:

  | Slot | Category | Light | Dark |
  |---|---|---|---|
  | 1 | vivienda | `#0A51AE` navy | `#465EC4` |
  | 2 | alimentación | `#2291F8` azure | `#4C9CC6` |
  | 3 | transporte | `#8A1A86` plum | `#B40EAE` |
  | 4 | suministros | `#4318E0` indigo | `#6440FD` |
  | 5 | salud | `#AC74CC` lavender | `#9E68AE` |
  | 6 | ocio | `#08908C` teal | `#1DAA8D` |
  | 7 | educación | `#D65CF9` orchid | `#C153E0` |
  | - | otros | `--series-other` gray (`#8D97A8` / `#64748B`) | |

  The order was chosen over all 5040 permutations to maximise the worst stack-neighbour pair
  (including otros and the debt red on top). Debts stay `--app-negative`.

  **Checker results, all pairs** (OKLab dE x100; the dataviz thresholds are CVD >= 8 and normal
  vision >= 15, so the full set cannot pass them: nine colours with these hue limits do not
  fit, and the numbers below are the best found):

  | | Light | Dark |
  |---|---|---|
  | Lightness band, chroma floor, contrast vs surface | pass | pass |
  | Worst normal-vision pair | 11.0 salud/educación | 10.9 salud/educación |
  | Worst protan / deutan pair | 5.7 / 6.2 | 5.7 / 5.6 |
  | Worst tritan pair | 4.1 suministros/transporte | 2.2 alimentación/ocio |
  | Pairs below normal 15 or CVD 8 | 6 of 36 | 16 of 36 |
  | Worst stack neighbour (min of normal, protan, deutan) | 9.1 (salud/ocio) | 10.0 |
  | Every category vs debt red, worst of normal/protan/deutan | 14.4 (ocio, deutan) | 6.9 (ocio, protan) |
  | educación vs debt red (normal/protan/deutan) | 31 / 35 / 30 | 22 / 22 / 23 |

  Because the all-pairs numbers are below the floors, **colour never carries identity alone**:
  the legend, the Sankey and ledger labels and the tooltips name every category. Pairs to keep
  in mind (light): ocio/otros (12.6 normal, protan 5.7), salud/educación (11.0, deutan 6.5),
  alimentación/educación (deutan 6.2, protan 5.9), vivienda/suministros (tritan 4.1),
  salud/otros (deutan 7.5), alimentación/salud (protan 7.3). Dark has more (ocio's teal is
  6.9 from the debt red under protan; it is never on top of the stack, but is next to
  nothing red either). The earlier near-identical purples (vivienda/ocio, 4.4 normal vision)
  are gone: the closest purple pair now is salud/educación, 11.0 normal and not neighbours.
  vivienda and alimentación sit 11-21 from the accent blue (alimentación 11.6 light, 11.3
  dark; labelled in the Sankey). Re-run the checker over all pairs if a value changes.
- **"Tu plan"** (fixed-return projection) is always a dashed violet line (`--series-4`).
- **Marks**: thin lines (1.5-2px); areas are faint washes (opacity <= 0.18) under a line in the
  series colour; uncertainty bands stay washes; bars <= 18px with a 4px rounded end,
  separated by a surface-coloured gap. Gridlines are solid hairlines in `--chart-grid`; a solid
  0 line appears when values go negative.
- **Text in charts is JetBrains Mono** at 11px (axes, labels, tooltips). Charts wait for the
  font before drawing (ECharts caches text widths) and keep a few px of left slack.
- Legends mirror the mark; a single series has no legend. Direct labels are selective
  (final value of the headline line, plan names when there are few).
- Time axes show the age under the year when the plan knows it. Tooltips list every series,
  totals first.
- Charts re-render from the CSS tokens on `themechange` and `prefers-color-scheme` changes.
- A chart that would be flat and empty is replaced by a sentence. A chart card header reads
  "Title / Subtitle" in muted mono, with a status chip on the right where a state applies.

## Writing

- Name things by what the user does: "Añadir cuenta", not "Nuevo registro".
- Buttons say exactly what happens ("Guardar cambios"), and the confirmation reuses the verb
  ("Cambios guardados").
- Empty states say what to do next. Errors say what went wrong and how to fix it, without
  apologizing.
- Every string goes in both `locales/es.json` and `locales/en.json`.

## Quality floor

Works at phone width (no horizontal page scroll), visible keyboard focus (`:focus-visible`,
2px accent ring), AA contrast for text in both themes (status text uses the darker or lighter
`-text` steps), and both themes and both languages checked with screenshots before a phase
is committed. Input borders are at least 3:1 against their surface (`#78869B` light, `#64748B` dark).
