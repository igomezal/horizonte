# Assumptions and limitations

Horizonte projects a plan month by month and estimates how it might turn out. **Every
figure is an estimate for planning, not financial or tax advice.** The rules it applies
are simplified, the future is uncertain, and laws change. Check important decisions with
a qualified adviser and, for pensions and taxes, with the official sources (Seguridad
Social, Agencia Tributaria).

This page lists what the model does and what it leaves out. The code is the final word:
`internal/engine` (projection, Monte Carlo, backtest), `internal/tax/es` (IRPF) and
`internal/pension/es` (public pension).

## The projection

- **Monthly steps.** The engine (`internal/engine`) is a pure function from a plan to a
  list of months. The projection starts in the current month, with the balances you
  entered, and ends when the longest-lived person reaches their life expectancy. Yearly
  views are roll-ups of the months.
- **Real or nominal.** Pages show today's euros by default (amounts divided by the
  cumulative inflation) with a switch to nominal euros; labels say which is shown.
  Net worth tracking is only in nominal euros, because the app has no record of actual
  inflation.
- **Inflation** is one constant rate per plan (default 2.5 %). Incomes and expenses with
  no growth of their own rise with inflation, stepping up once a year; a stream with its
  own `annual_growth` follows that rate instead. Account contributions and debt
  instalments are fixed nominal amounts and do not rise with inflation. Backtests replace
  the constant rate with the historical CPI.
- **Returns.** Each account kind has an expected annual return (defaults: cash 0 %,
  deposit 2 %, investment 6 %, pension plan 5 %, property 3 %, other 2 %), applied as the
  equivalent monthly compound rate. An account can have its own return. In the
  deterministic projection returns are the same every year; there is no volatility.
  Property growth is shown as appreciation, the other accounts' as returns.
- **Each month**, income arrives (gross), tax and Social Security are paid, expenses and
  debt instalments are paid, and what is left (the surplus) goes first to each account's
  monthly contribution (limited to the surplus, until the contribution's end milestone)
  and then to the surplus account. Instalments freed by a paid-off debt go to the account
  chosen for it, otherwise they are ordinary surplus.
- **Shortfalls.** When a month's money is not enough, accounts are drawn down in the
  plan's withdrawal order (default: cash, deposits, investments, other, pension plans).
  Property is never drawn down (it can only be sold on a planned date). Pension plans can
  be locked until a milestone. If everything is empty the missing amount is a shortfall,
  and the plan "fails" from that month: this is what "the money runs out" means in every
  page.
- **Milestones.** Age and date milestones are fixed. Financial independence (FIRE)
  milestones happen the first month savings, excluding property, reach a multiple of a
  yearly spending figure. The variants are standard (actual spending), lean and fat (a
  budget you enter), barista (the spending a part-time income does not cover) and coast
  (savings that would grow to the target by a given age with no more contributions). A
  milestone that is never reached within the plan never happens.
- **Withdrawal strategies**, from a milestone on: `fixed` (spend the planned expenses),
  `percent` (a share of savings each year), 4 % rule (a share of savings the first year,
  then adjusted for inflation) and guardrails (the same, cut 10 % when the withdrawal
  rate rises 20 % above the target and raised 10 % when it falls 20 % below). Spending is
  scaled so that it matches income after tax plus the withdrawal, within a floor and a
  ceiling expressed as a share of the planned expenses.
- **Debts and early repayments.** Debts are amortised with their nominal annual rate
  (TIN) and a fixed instalment. Early repayments can shorten the term or lower the
  instalment, may carry a fee, are limited to the month's surplus and to liquid money above a reserve
  you choose, and one-off repayments dated before the plan starts never happen.
- **Selling property.** A real-estate account can be sold fully or partly, at a date or
  milestone. The money, less selling costs, goes to the account you choose (by default the
  surplus account). With the Spanish tax model the gain over its cost goes to the savings
  base and is paid with the tax return the following June; if the plan ends before that
  June, the tax is charged in its last month. A main residence sold by an owner aged 65 or
  over is exempt; reinvestment relief is not modelled. The flat tax model does not tax the
  sale.
- **Inheritance.** The Plan page shows what is left at the end, by account, less debts.
  Inheritance tax is not included (it depends on the region and the relationship), and
  the page notes that pension plans pay income tax when heirs cash them and that
  property may pay the municipal capital gains tax (plusvalía).

## Taxes

Taxes are a rough simulation, not an official calculation: the scales and rules may be
out of date or contain mistakes, and your real situation can change the result a lot.
For important decisions, check the figures against the Tax Agency's simulator or a tax
adviser. Two models, chosen per plan:

- **Flat rate.** Tax is the chosen share of gross income each month. Withdrawals, interest,
  investment gains and property sales are not taxed.
- **Spanish IRPF** (the default for a new plan). The rules are the 2026 ones
  (`internal/tax/es/tables.go`), applied each year to each person on their own
  (individual filing; joint accounts, incomes and expenses are split equally between the
  people of the plan). Monthly withholding is estimated from the year's expected income,
  and the difference with the year's real tax is settled the following June (a refund if
  negative). The first calendar year of a plan that starts after January is not settled,
  except for the tax on a property sale.

What the IRPF model covers:

- **General base:** salary (less the employee's Social Security, 6.5 % up to the maximum
  base), public pensions and money withdrawn from pension plans (all as work income, less
  the €2,000 art. 19.2.f expenses and the art. 20 reduction for low work income),
  self-employed profit (5 % hard-to-document expenses, up to €2,000), rental income (with
  the 50 % reduction for housing) and other income. Pension plan contributions reduce the
  base up to €1,500 and 30 % of work and business income.
- **Savings base:** deposit interest every year, and gains on investment and other accounts
  when they are withdrawn (only the gain, using the account's cost basis; the starting
  balance counts as the cost unless you enter one), plus property gains. Taxed with the
  savings scale (19 % to 30 %). Losses are not offset against gains.
- **Scales:** the State scale plus the regional scale of the chosen region: general (the
  State's scale doubled), Andalucía, Aragón, Asturias, Illes Balears, Canarias, Cantabria,
  Castilla-La Mancha, Castilla y León, Cataluña, Extremadura, Galicia, Madrid, Murcia,
  La Rioja and C. Valenciana (2026 scales). País Vasco and Navarra (own tax systems) are
  not modelled.
- **Personal minimum:** the State's (€5,550, plus €1,150 from age 66 and €1,400 from age
  76) applied to the State half, and the region's own minimum applied to the regional half
  for the seven regions that set one in the data: Madrid, Galicia, Andalucía, Asturias,
  C. Valenciana, Canarias and Illes Balears. The other regions use the State amounts.
  Monthly withholding uses the State amounts only, as the tax agency's withholding
  algorithm does, so a region's own minimum shows up in the June settlement.
- **DA 61 deduction** for obtaining work income (the amount raised for 2026): up to
  €590.89 off the tax for gross employee income under about €20,048 when other income is
  at most €6,500, and never more than the tax itself. How the legal limit is measured is
  a reading of the text (explained in `internal/tax/es/irpf.go`).
- **Bracket indexing.** By default scales, minimums and thresholds rise with inflation,
  so tax is worked out in today's euros. The "freeze brackets" option keeps them fixed
  in nominal euros, so inflation pushes income into higher brackets (only the Social
  Security maximum base still rises).

What it leaves out: deductions (mortgage, regional and others), family situation
(children, dependants, disability), joint filing, regional rules other than the scales and
minimums above, Social Security of the self-employed, wealth and inheritance taxes, and
tax on dividends or interest inside investment funds beyond the gain on withdrawal. The
C. Valenciana scale of 2027 is not included.

## Public pension estimate

The Public pension page estimates a Spanish contributory retirement pension for a
person, in today's euros (2026 bases and limits; pensions are revalued with inflation),
with the rules that apply from 2027, and can add it to the plan as an income in 14
payments that grows with inflation. It is a rough estimate, not an official calculation
(`internal/pension/es/pension.go`).

What it covers:

- **Entitlement:** at least 15 years contributed, 2 of them in the 15 years before the
  pension starts.
- **Legal retirement age:** 65 with 38 years and 6 months contributed, otherwise 67. If
  the 38 years and 6 months are reached between 65 and 67, the legal age is read as the
  month they are reached. This reading of arts. 205.1.a and 208.2 is an interpretation:
  no statement from the Social Security on those cases was found.
- **Amount:** the base reguladora is the contribution bases of the last 25 years (300
  months) divided by 350, times a percentage that is 50 % at 15 years and rises with the
  months contributed up to 100 %. The contribution base is assumed constant in real terms,
  limited to the 2026 minimum and maximum bases, and past contributions are assumed
  continuous up to today. For employees, months without contributions in the window are
  filled with the minimum base (the first 48), then 80 % of it (the next 12) and then 50 %;
  for the self-employed they are not.
- **Early retirement** (art. 208): up to 24 months before the legal age, with at least 35
  years contributed and a pension above the minimum pension (without a dependent spouse).
  The reduction comes from the art. 208.2 monthly table, by months of advance and
  contribution period. A request that does not qualify is calculated at the legal age.
- **Maximum pension and early retirement:** when the pension is above the maximum, the
  reduction applies to the maximum pension, phased in by year of retirement until 2033
  (disposición transitoria trigésima cuarta). The guarantee in apartado 1 of that
  provision (never less than under the 2021 rules) is read as a condition on how the
  maximum pension is revalued, not as a floor for each person, and is not modelled.
- **Late retirement bonus** (art. 210.2.a): 4 % for each complete year contributed after the
  legal age, plus 2 % for a further period of more than 6 months and under a year from the
  second year of delay.
- **Minimum pension top-up:** optional, up to the non-contributory limit, from age 65.

Left out on purpose: the extra annual amount for those who delay retirement and are at the
maximum pension (art. 210.2.a), the transitional ages of disposición transitoria séptima
for 2026, the transitional base reguladora window (disposición transitoria cuadragésima),
the transitional gap-filling rules (disposición transitoria cuadragésima primera), the
solidarity contribution on bases above the maximum, the higher contribution rate of
fixed-term contracts, special schemes, partial retirement and the transitional choice of
the best 27 of the last 29 years.

## Health costs

The Health costs page proposes rough Spanish figures in today's euros: private insurance
premiums that rise by age band, out-of-pocket costs that public healthcare does not cover
(dentist, glasses, pharmacy co-payments) and a private care home late in life (off by
default). You adjust them and add them to the plan as expenses by age band. They are
planning figures, not quotes.

## Chance of success (Monte Carlo)

- Runs the plan 250, 500, 1,000 (default) or 2,000 times, each with a different random
  path of returns, and reports the share of runs where the money lasts to the end of the
  plan (no shortfall month). A fixed seed makes results reproducible until you ask for
  other scenarios.
- **Random** model: independent normal monthly returns per account kind, with the expected
  return as the median outcome and the volatility you set (defaults: cash 0 %, deposit 1 %,
  investment 16 %, pension 10 %, property 8 %, other 5 %), all tied to a common market factor
  with a correlation of 0.6. Inflation stays fixed.
- **Historical blocks** model: strings together contiguous 12-month blocks of US history
  since 1871 (stocks, bonds and inflation from the same months), shifted so their long-run
  means match your expected returns and inflation. It does not use the volatilities above.
- Results are shown in today's euros: percentile bands (10, 25, 50, 75, 90), survival by
  year, a bad case (the 10th-percentile scenario) and when milestones are reached.

## Historical backtest

- Replays the plan as if the markets of history started in each January since 1871, and
  from the start of named crises (1929, 1973, 2000, 2007 and 2020). Investment accounts
  follow US stocks, pension plans a 60/40 mix of stocks and bonds, cash earns nothing,
  and deposits, property and other accounts keep their expected real return on top of
  historical inflation. Incomes and expenses that follow inflation use historical CPI.
- Once the data ends, the plan continues with your expected returns and inflation; those
  start years are marked partial and are not counted in the headline percentage.
- The data is **US** stocks, bonds and inflation, applied to a plan in euros with no
  currency adjustment: it says how your plan would have fared with US market returns, not
  Spanish or European ones. Past results do not predict future ones.

## Other limits

- One household per installation, in euros. Up to two people per plan.
- No bank links: every number is typed in or imported.
- The app does not know about changes in law after the 2026 tables it contains.
