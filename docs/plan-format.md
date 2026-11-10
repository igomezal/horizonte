# Plan file format

Plans are exported and imported as JSON on the Scenarios page. The format is
independent of the database, so you can keep a file, move it to another installation or
edit it by hand. It is defined in `internal/backup/backup.go` (plans) and
`internal/backup/tracking.go` (net worth history); the validation rules come from
`internal/domain` and `internal/engine`.

## Conventions

- **Two file types**, told apart by `format`:
  - `horizonte/plan`: one plan, under `plan`.
  - `horizonte/plans`: several plans, under `plans`, plus optional `tracking`.
- **`version`** is `1`. A file with a higher version, or lower than 1, is rejected. The
  version only changes for incompatible changes.
- **Money** is a plain number in euros (`1250.5`), not cents, and not formatted.
- **Rates and percentages** are ratios: `0.025` is 2.5 %. This applies to inflation,
  returns, volatility, growth, interest rates, fees, tax rates and the sale share.
- **Months** are `"YYYY-MM"` strings (`"1990-04"`). An empty or missing month means "not
  set". Dates of net worth snapshots are `"YYYY-MM-DD"`.
- **Ids** link records inside the file: every record has a positive integer `id`, unique
  within its list, and other records refer to it (`owner_id`, `person_id`,
  `start_milestone_id`, ...). The ids are only labels for the file; importing creates new
  database ids. A link to an id that is not in the file is an error.
- **Optional fields** can be left out. A missing number is `0` (or "not set" for fields
  marked nullable). Unknown fields are ignored.
- **Limits:** a file is at most 2 MiB; at most 500 records in each list (milestones,
  accounts, incomes, expenses, debts, prepayments) and 2 people per plan; at most 50
  plans in one file; names are 1 to 80 characters.
- Import never overwrites: every plan in the file becomes a new plan. See
  [data-and-privacy.md](data-and-privacy.md) for what an export includes.
- An invalid file is rejected as a whole, with the section, record name and field that
  failed.

## Single plan

```json
{
  "format": "horizonte/plan",
  "version": 1,
  "exported_at": "2026-11-10T09:30:00Z",
  "plan": { "name": "Central", "people": [], "milestones": [], "accounts": [],
            "incomes": [], "expenses": [], "debts": [], "prepayments": [],
            "assumptions": { } }
}
```

`exported_at` is optional (UTC, RFC 3339) and ignored on import.

## Several plans

```json
{
  "format": "horizonte/plans",
  "version": 1,
  "exported_at": "2026-11-10T09:30:00Z",
  "plans": [ { "id": 1, "active": true, "name": "Central", "...": "..." },
             { "id": 2, "parent_id": 1, "name": "Cautious", "...": "..." } ],
  "tracking": { "entries": [], "baseline": { } }
}
```

Each entry of `plans` has the fields of a single plan plus:

| Field | Meaning |
|---|---|
| `id` | Required in a bundle: positive, unique among the plans |
| `parent_id` | Optional: the plan this one was copied from. Dropped if that id is not in the file |
| `active` | The plan that is active after import. If none is marked, the first plan is |

### `tracking` (bundle only)

The net worth history belongs to the household, not to a plan, so it only travels in a
bundle. It is optional; a single-plan file never has it. On import only missing months
are added and the baseline is used only if there is none.

| Field | Type | Notes |
|---|---|---|
| `entries[].month` | month | One recorded month. Not in the future, 1970 or later, unique within the file |
| `entries[].balances` | object | Euros by account kind (`cash`, `deposit`, `investment`, `pension`, `realestate`, `other`); kinds with nothing can be left out. Not negative |
| `entries[].debt` | euros | Total debt, not negative |
| `baseline.plan_name` | text | The plan the baseline came from (1 to 80 characters) |
| `baseline.saved_on` | `YYYY-MM-DD` | When it was saved |
| `baseline.points[]` | list | `{ "month": "2026-11", "net_worth": 85000 }`, at least one, months strictly ascending. All amounts are nominal euros. Up to 1200 entries and 1200 points |

## Plan

| Field | Type | Notes |
|---|---|---|
| `name` | text | 1 to 80 characters |
| `people` | list | At most 2 |
| `milestones` | list | |
| `accounts` | list | |
| `incomes` | list | |
| `expenses` | list | |
| `debts` | list | |
| `prepayments` | list | Early repayments of debts |
| `assumptions` | object | Plan-wide settings. Missing fields get the defaults listed below |

### `people[]`

| Field | Notes |
|---|---|
| `id`, `name` | |
| `birth` | Month. Required, not in the future, at most 110 years ago |
| `life_expectancy` | Whole age in years the plan runs to: 50 to 120, and above the person's current age |

The plan ends when the longest-lived person reaches their life expectancy.

### `milestones[]`

| Field | Notes |
|---|---|
| `id`, `name` | |
| `kind` | `age`, `date` or `fi` |
| `person_id`, `age` | For `age`: the person and an age from 1 to 120. For `fi` with `fi_variant` `coast`: the person and a target age from 18 to 110 |
| `date` | For `date`: a month (required) |
| `fi_multiple` | For `fi`: 5 to 100 (the usual rule of thumb is 25). Required for `fi` |
| `fi_variant` | Required for `fi` (import fails without it): `standard`, `lean`, `fat`, `barista` or `coast` |
| `fi_amount` | Euros per year, in today's euros. Required (above 0) for `lean`, `fat` and `barista` |

An `fi` (financial independence) milestone has no fixed date: it happens the first month
savings, excluding property, reach `fi_multiple` times a yearly spending figure that
depends on the variant (the real spending, `fi_amount`, the spending a part-time income
does not cover, or, for `coast`, the spending the savings would reach by the target age
with no more contributions).

### Streams and milestones

Incomes, expenses and prepayments have a start and an end, each either a month or a
milestone (the milestone wins if both are given):

| Field | Notes |
|---|---|
| `start`, `end` | Months. Missing start: from the first projected month. Missing end: no end. With no milestone link, `end` must not be before `start` |
| `start_milestone_id` | Starts when that milestone happens. If the milestone is never reached, the stream never starts |
| `end_milestone_id` | Ends the month before that milestone. If it is never reached, the stream never ends |

### `accounts[]`

| Field | Notes |
|---|---|
| `id`, `name` | |
| `kind` | `cash` (current account), `deposit`, `investment`, `pension` (pension plan), `realestate` or `other` |
| `owner_id` | Person, or leave out for a joint account |
| `balance` | Euros, not negative |
| `monthly_contribution` | Euros per month, not negative. Paid from the month's surplus, so it is capped at what is left; it does not grow with inflation |
| `contribution_end_milestone_id` | Contributions stop when this milestone happens |
| `annual_return` | Nullable. Own return, from -0.5 to 0.5. Missing: the plan's default for the kind (`assumptions.returns`) |
| `cost_basis` | Nullable euros: what the holdings cost, for the gain on `investment`, `other` and `realestate`. Missing: the starting balance counts as the cost |
| `sale` | Planned sale, `realestate` accounts only (below) |

`sale`:

| Field | Notes |
|---|---|
| `date` / `milestone_id` | When it is sold. One of them is required; the milestone wins |
| `share` | Share sold, above 0 and up to 1. Missing: 1 (all of it) |
| `cost_rate` | Selling costs as a share of the price: 0 to 0.5 |
| `destination_account_id` | Account that receives the money. Must be `cash`, `deposit`, `investment` or `other`. Missing: the surplus account |
| `main_residence` | `true`: the gain is exempt for an owner aged 65 or over |

### `incomes[]`

| Field | Notes |
|---|---|
| `id`, `name` | |
| `kind` | `salary`, `selfemployed`, `rental`, `pension` or `other` |
| `owner_id` | Person, or leave out for joint (split equally for taxes) |
| `amount` | Euros per payment, above 0, gross (before tax) |
| `payments_per_year` | `12`, `14` (salaries with two extra payments) or `1` |
| `annual_growth` | Nullable, -0.2 to 0.3. Missing: grows with inflation |
| `start`, `end`, `start_milestone_id`, `end_milestone_id` | See above |

### `expenses[]`

| Field | Notes |
|---|---|
| `id`, `name` | |
| `category` | `housing`, `food`, `transport`, `utilities`, `health`, `leisure`, `education` or `other` |
| `amount` | Euros per payment, above 0 |
| `payments_per_year` | `12` or `1` (14 is not allowed for expenses) |
| `annual_growth` | Nullable, -0.2 to 0.3. Missing: grows with inflation |
| `start`, `end`, `start_milestone_id`, `end_milestone_id` | See above |

### `debts[]`

| Field | Notes |
|---|---|
| `id`, `name` | |
| `kind` | `mortgage`, `car`, `personal`, `student`, `card` or `other` |
| `balance` | Outstanding principal in euros, above 0 |
| `annual_rate` | Nominal annual rate (TIN), 0 to 0.5 |
| `monthly_payment` | Euros, above 0 |
| `redirect_account_id` | Account that receives the instalment once the debt is paid off. Missing: it becomes ordinary surplus |

### `prepayments[]`

| Field | Notes |
|---|---|
| `id` | |
| `debt_id` | Required: a debt in the file |
| `amount` | Euros per payment, above 0 |
| `frequency` | `once`, `monthly` or `yearly`. `once` needs `start` or `start_milestone_id` |
| `mode` | `term` (keep the instalment, the debt ends earlier) or `payment` (keep the end date, lower the instalment) |
| `fee_rate` | Commission on the amount repaid, 0 to 0.05 |
| `redirect_account_id` | Receives payments that fall due after the debt is paid off. Missing: they are not made |
| `start`, `end`, `start_milestone_id`, `end_milestone_id` | See above |

### `assumptions`

| Field | Default if missing | Notes |
|---|---|---|
| `inflation` | `0` | -0.05 to 0.2. Note: a missing value is 0, not the app's default of 0.025, so include it |
| `tax_model` | `es` | `es` (Spanish IRPF) or `flat` |
| `tax_region` | `general` | Used with `es`: `general`, `andalucia`, `aragon`, `asturias`, `baleares`, `canarias`, `cantabria`, `castilla_la_mancha`, `castilla_leon`, `cataluna`, `extremadura`, `galicia`, `madrid`, `murcia`, `rioja` or `valencia` |
| `tax_rate` | `0` | Used with `flat`: share of gross income paid as tax, 0 to 0.6. Missing is 0, not the app's default of 0.20 |
| `returns` | see below | Expected annual return per account kind, -0.5 to 0.5. Object keyed by kind; kinds left out keep the default |
| `volatility` | see below | Annual volatility per account kind for Monte Carlo, 0 to 0.6 |
| `monte_carlo_runs` | `1000` | One of `250`, `500`, `1000`, `2000` |
| `monte_carlo_model` | `parametric` | `parametric` (random) or `bootstrap` (historical blocks) |
| `surplus_account_id` | first current account | Account that receives leftover money. Missing: the first `cash` account, or the first account that is not property |
| `withdrawal` | `fixed` | Object, below. If `withdrawal.strategy` is missing the whole object is ignored |
| `withdrawal_order` | `cash, deposit, investment, other, pension` | Order accounts are drawn down in when income falls short. Must list exactly those five kinds once each (property is never drawn down) |
| `pension_available_milestone_id` | always available | Pension plans cannot be drawn before this milestone |
| `prepayment_reserve` | `0` | Euros, in today's euros, that liquid accounts keep when funding early repayments. Not negative |
| `irpf_brackets_frozen` | `false` | `true`: IRPF scales stay fixed in nominal euros instead of rising with inflation |

Default returns by kind: `cash` 0, `deposit` 0.02, `investment` 0.06, `pension` 0.05,
`realestate` 0.03, `other` 0.02. Default volatilities: `cash` 0, `deposit` 0.01,
`investment` 0.16, `pension` 0.10, `realestate` 0.08, `other` 0.05.

`withdrawal`:

| Field | Notes |
|---|---|
| `strategy` | `fixed` (spend the planned expenses), `percent` (a share of savings each year), `rule4` (a share of savings in the first year, then adjusted for inflation) or `guardrails` (like `rule4`, cut 10 % when the rate rises 20 % above `rate`, raised 10 % when it falls 20 % below) |
| `start_milestone_id` | Milestone from which the strategy applies. Required for `percent`, `rule4` and `guardrails` |
| `rate` | Required for `percent`, `rule4` and `guardrails`: 0.01 to 0.12 (e.g. 0.04) |
| `floor` | Required for `percent`, `rule4` and `guardrails`: minimum spending as a share of planned expenses, 0.3 to 1 |
| `ceiling` | Required for `percent`, `rule4` and `guardrails`: maximum spending as a share of planned expenses, 1 to 3 |

For `fixed`, `rate`, `floor` and `ceiling` are not checked. A missing number is 0, so it fails the range check for the other strategies.

## Minimal example

```json
{
  "format": "horizonte/plan",
  "version": 1,
  "plan": {
    "name": "Example",
    "people": [{ "id": 1, "name": "Ana", "birth": "1990-04", "life_expectancy": 90 }],
    "milestones": [{ "id": 1, "name": "Retirement", "kind": "age", "person_id": 1, "age": 65 }],
    "accounts": [
      { "id": 1, "name": "Current account", "kind": "cash", "balance": 20000 },
      { "id": 2, "name": "Index fund", "kind": "investment", "balance": 50000,
        "monthly_contribution": 500, "contribution_end_milestone_id": 1 }
    ],
    "incomes": [
      { "id": 1, "name": "Salary", "kind": "salary", "amount": 2500,
        "payments_per_year": 14, "end_milestone_id": 1 }
    ],
    "expenses": [
      { "id": 1, "name": "Living costs", "category": "other", "amount": 1500,
        "payments_per_year": 12 }
    ],
    "debts": [],
    "prepayments": [],
    "assumptions": {
      "inflation": 0.025,
      "tax_model": "es",
      "tax_region": "madrid",
      "monte_carlo_runs": 1000
    }
  }
}
```

An export of your own plan is the best template: change a value, import the file, and the
error message tells you which section, record and field was rejected if it does not pass.
