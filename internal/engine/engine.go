// Package engine projects a plan month by month. It is pure: no I/O, no
// globals, so the deterministic projection, Monte Carlo (phase 5) and
// historical backtests (phase 6) can all reuse Simulate.
package engine

import (
	"math"

	"github.com/igomezal/horizonte/internal/domain"
)

// Assumptions are the plan-wide settings the projection depends on.
type Assumptions struct {
	Inflation float64 // annual, e.g. 0.025
	TaxRate   float64 // flat effective rate on gross income (TaxModel "flat")
	// TaxModel is TaxES (Spanish IRPF) or TaxFlat; TaxRegion picks the
	// regional IRPF scale.
	TaxModel  string
	TaxRegion string
	// Returns is the default annual return per account kind, used when an
	// account has no return of its own.
	Returns map[domain.AccountKind]float64
	// SurplusAccountID receives money left over each month. Nil means the
	// first current account (or the first account that is not property).
	SurplusAccountID *int64
	// Volatility is the annual volatility per account kind (Monte Carlo).
	Volatility map[domain.AccountKind]float64
	// MCRuns is the number of Monte Carlo scenarios.
	MCRuns int
	// Withdrawal is the retirement withdrawal strategy.
	Withdrawal WithdrawalSettings
	// Order is the order accounts are drawn down in (DefaultOrder when nil).
	Order []domain.AccountKind
	// PensionAvailableMS: pension plans cannot be drawn before this milestone
	// (nil = always available).
	PensionAvailableMS *int64
	// PrepaymentReserve is the money, in today's euros, that the liquid
	// accounts keep when funding early repayments. It rises with inflation.
	PrepaymentReserve domain.Cents
}

// DefaultAssumptions are reasonable long-run nominal values for Spain.
func DefaultAssumptions() Assumptions {
	return Assumptions{
		Inflation: 0.025,
		TaxRate:   0.20,
		TaxModel:  TaxES,
		TaxRegion: "general",
		Returns: map[domain.AccountKind]float64{
			domain.AccountCash:       0,
			domain.AccountDeposit:    0.02,
			domain.AccountInvestment: 0.06,
			domain.AccountPension:    0.05,
			domain.AccountRealEstate: 0.03,
			domain.AccountOther:      0.02,
		},
		Volatility: DefaultVolatility(),
		MCRuns:     1000,
		Withdrawal: DefaultWithdrawal(),
		Order:      append([]domain.AccountKind(nil), DefaultOrder...),
	}
}

// Validate checks ranges. Field names match the assumptions form.
func (a Assumptions) Validate() domain.Errors {
	errs := domain.Errors{}
	if a.Inflation < -0.05 || a.Inflation > 0.2 {
		errs.Add("inflation", domain.ErrRange)
	}
	if a.TaxModel != "" && a.TaxModel != TaxFlat && a.TaxModel != TaxES {
		errs.Add("tax_model", domain.ErrChoice)
	}
	if a.TaxModel == TaxES && !validTaxRegion(a.TaxRegion) {
		errs.Add("tax_region", domain.ErrChoice)
	}
	if a.TaxRate < 0 || a.TaxRate > 0.6 {
		errs.Add("tax_rate", domain.ErrRange)
	}
	for _, k := range domain.AccountKinds {
		if r := a.Returns[k]; r < -0.5 || r > 0.5 {
			errs.Add("return_"+string(k), domain.ErrRange)
		}
		if v := a.Volatility[k]; v < 0 || v > 0.6 {
			errs.Add("vol_"+string(k), domain.ErrRange)
		}
	}
	a.Withdrawal.validate(errs)
	if a.Order != nil && ParseOrder(FormatOrder(a.Order)) == nil {
		errs.Add("wd_order", domain.ErrChoice)
	}
	runsOK := false
	for _, r := range MCRunsOptions {
		runsOK = runsOK || r == a.MCRuns
	}
	if !runsOK {
		errs.Add("mc_runs", domain.ErrChoice)
	}
	if a.PrepaymentReserve < 0 {
		errs.Add("prepay_reserve", domain.ErrNegative)
	}
	return errs
}

// AnnualReturn returns the expected annual return of an account.
func (a Assumptions) AnnualReturn(acc domain.Account) float64 {
	if acc.AnnualReturn != nil {
		return *acc.AnnualReturn
	}
	return a.Returns[acc.Kind]
}

// Input is everything a projection needs.
type Input struct {
	People      []domain.Person
	Accounts    []domain.Account
	Incomes     []domain.Income
	Expenses    []domain.Expense
	Debts       []domain.Debt
	Prepayments []domain.Prepayment
	Milestones  []domain.Milestone
	Assumptions Assumptions
	Start       domain.Month // first projected month (normally the current month)
	// Detail records a per-stream breakdown of every month (MonthRow.Flow),
	// used by the cash-flow view. Off for bulk runs such as Monte Carlo.
	Detail bool
	// InflationPath, when set, gives the inflation of each projected month
	// (index t = month Start+t); months beyond it use Assumptions.Inflation.
	// Historical backtests use it so expenses and income follow real CPI.
	InflationPath []float64
}

// EndMonth is the last projected month: when the longest-lived person
// reaches their life expectancy. Zero when there are no people.
func (in Input) EndMonth() domain.Month {
	var end domain.Month
	for _, p := range in.People {
		m := p.Birth.AddMonths(p.LifeExpectancy * 12)
		if end.IsZero() || end.Before(m) {
			end = m
		}
	}
	return end
}

// MonthRow is the state at the end of one projected month. Amounts are
// nominal euros; divide by Deflator for today's euros.
type MonthRow struct {
	Month    domain.Month
	Deflator float64 // cumulative inflation since the start (1 in the first month)

	Income       float64 // gross
	Tax          float64
	Expenses     float64
	DebtPayments float64 // instalments plus early repayments (and their fees)
	Prepayments  float64 // early repayments and fees, included in DebtPayments
	// Redirected is early-repayment money sent to an account because its debt
	// was already paid off. It is part of Contributions.
	Redirected float64
	// SpendFactor is the share of planned expenses actually spent this month
	// under the withdrawal strategy (1 before it starts or with "fixed").
	SpendFactor   float64
	Contributions float64 // moved into accounts from that month's surplus
	Withdrawals   float64 // taken from accounts to cover a deficit
	Shortfall     float64 // deficit nothing could cover this month

	// Returns is what the accounts other than property earned this month
	// (interest and growth, negative in a bad month); Appreciation is the
	// change in value of property.
	Returns      float64
	Appreciation float64

	// SurplusSaved is money left over after contributions that went to the
	// surplus account (or was spent when there is none).
	SurplusSaved float64
	// Flow is the per-stream breakdown, only when Input.Detail is set.
	Flow *Flow

	Balances    []float64 // per account, in Result.Accounts order
	DebtBalance float64
	NetWorth    float64
}

// Flow breaks a month (or year) down by stream. Slices are indexed like the
// matching Input slice. For every period:
//
//	sum(Income) + sum(Withdrawals) + Shortfall =
//	    Tax + sum(Expenses) + sum(Debts) + sum(Contributions) + SurplusSaved
type Flow struct {
	Income        []float64 // per income stream
	Expenses      []float64 // per expense
	Debts         []float64 // per debt: instalments plus early repayments and fees
	Contributions []float64 // per account: contributions, freed instalments, redirects
	Withdrawals   []float64 // per account
}

func newFlow(in Input) *Flow {
	return &Flow{
		Income:        make([]float64, len(in.Incomes)),
		Expenses:      make([]float64, len(in.Expenses)),
		Debts:         make([]float64, len(in.Debts)),
		Contributions: make([]float64, len(in.Accounts)),
		Withdrawals:   make([]float64, len(in.Accounts)),
	}
}

func addSlice(dst, src []float64) {
	for i := range src {
		dst[i] += src[i]
	}
}

func (f *Flow) add(o *Flow) {
	addSlice(f.Income, o.Income)
	addSlice(f.Expenses, o.Expenses)
	addSlice(f.Debts, o.Debts)
	addSlice(f.Contributions, o.Contributions)
	addSlice(f.Withdrawals, o.Withdrawals)
}

// Savings is what was left after tax, expenses and debt payments (negative
// when the month ran a deficit).
func (r MonthRow) Savings() float64 { return r.Income - r.Tax - r.Expenses - r.DebtPayments }

// Assets is the sum of all account balances.
func (r MonthRow) Assets() float64 {
	var t float64
	for _, b := range r.Balances {
		t += b
	}
	return t
}

// MilestoneHit is when a milestone happens in the projection.
type MilestoneHit struct {
	Milestone domain.Milestone
	Month     domain.Month // zero when it is never reached within the plan
}

// Reached reports whether the milestone happens within the plan.
func (h MilestoneHit) Reached() bool { return !h.Month.IsZero() }

// DebtPaidOff is an event the engine detects: a debt's last payment.
type DebtPaidOff struct {
	Debt  domain.Debt
	Month domain.Month
}

// Result is a full projection.
type Result struct {
	Accounts       []domain.Account
	Months         []MonthRow
	Milestones     []MilestoneHit // in the order given in Input
	PaidOff        []DebtPaidOff  // in payoff order
	SurplusAccount int            // index of the account receiving surplus money, -1 if none
	WithdrawalFrom domain.Month   // month the withdrawal strategy started (zero if never)
	TaxYears       []TaxYear      // IRPF per calendar year (TaxModel "es")
	FailMonth      domain.Month   // first month with a shortfall; zero if the money lasts
	Shortfall      float64        // total uncovered deficit, nominal euros
}

// Failed reports whether the money ran out at some point.
func (r Result) Failed() bool { return !r.FailMonth.IsZero() }

// monthlyRate converts an annual rate into the equivalent compound monthly rate.
func monthlyRate(annual float64) float64 { return math.Pow(1+annual, 1.0/12) - 1 }

// growth returns the yearly-stepped growth factor after t months.
func growth(annual float64, t int) float64 { return math.Pow(1+annual, float64(t/12)) }

// Simulate runs the deterministic projection with each account's expected return.
func Simulate(in Input) Result {
	rates := make([]float64, len(in.Accounts))
	for i, a := range in.Accounts {
		rates[i] = monthlyRate(in.Assumptions.AnnualReturn(a))
	}
	return simulate(in, func(t, i int) float64 { return rates[i] })
}

// simulate runs the projection with rate(t, i) giving account i's return in month t.
func simulate(in Input, rate func(t, i int) float64) Result {
	end := in.EndMonth()
	res := Result{Accounts: in.Accounts, SurplusAccount: -1}
	if end.IsZero() || end.Before(in.Start) {
		return res
	}
	n := in.Start.MonthsUntil(end) + 1
	a := in.Assumptions
	// priceIndex[t] is the cumulative inflation from the start to month t.
	// Inflation-linked streams step up once a year (priceIndex[12·⌊t/12⌋]);
	// the deflator to today's euros follows it monthly.
	fixedInfl := monthlyRate(a.Inflation)
	priceIndex := make([]float64, n+1)
	priceIndex[0] = 1
	for t := range n {
		mi := fixedInfl
		if t < len(in.InflationPath) {
			mi = in.InflationPath[t]
		}
		priceIndex[t+1] = priceIndex[t] * (1 + mi)
	}

	balances := make([]float64, len(in.Accounts))
	for i, acc := range in.Accounts {
		balances[i] = acc.Balance.Euros()
	}
	debts := make([]float64, len(in.Debts))
	payment := make([]float64, len(in.Debts)) // current instalment ("reducir cuota" lowers it)
	lastT := make([]int, len(in.Debts))       // month index of the scheduled last payment, -1 if never
	debtIdx := make(map[int64]int, len(in.Debts))
	accIdx := make(map[int64]int, len(in.Accounts))
	for i, acc := range in.Accounts {
		accIdx[acc.ID] = i
	}
	target := func(id *int64) (int, bool) {
		if id == nil {
			return 0, false
		}
		i, ok := accIdx[*id]
		return i, ok
	}
	freed := make([]float64, len(in.Debts)) // instalment freed by a paid-off debt, for its redirect account
	for i, d := range in.Debts {
		debts[i] = d.Balance.Euros()
		payment[i] = d.MonthlyPayment.Euros()
		lastT[i] = d.MonthsToPayoff() - 1
		debtIdx[d.ID] = i
	}
	surplusIdx := surplusAccount(in.Accounts, a.SurplusAccountID)
	res.SurplusAccount = surplusIdx
	order := drawdownOrder(in.Accounts, a.Order)
	// Age and date milestones are known up front; financial independence is
	// added the month the projection reaches it.
	resolved := domain.ResolveFixed(in.Milestones, in.People)
	paidOff := make([]bool, len(in.Debts))
	// Pension plans may only be drawn from a milestone on.
	pensionLocked := func(m domain.Month) bool {
		if a.PensionAvailableMS == nil {
			return false
		}
		from, ok := resolved[*a.PensionAvailableMS]
		return !ok || m.Before(from)
	}
	policy := &spendPolicy{s: a.Withdrawal, factor: 1}
	if policy.s.Strategy == "" {
		policy.s = DefaultWithdrawal()
	}

	// Purchase cost of each account, to tax only the gains of a withdrawal.
	// Starting balances count as cost (existing unrealised gains are unknown).
	basis := append([]float64(nil), balances...)
	var tx *taxer
	if a.TaxModel == TaxES && len(in.People) > 0 {
		tx = newTaxer(in)
	}
	// deposit adds money to an account: it raises the cost basis and, for
	// pension plans, counts as a deductible contribution.
	deposit := func(i int, amount float64) {
		balances[i] += amount
		basis[i] += amount
		if tx != nil && in.Accounts[i].Kind == domain.AccountPension {
			tx.addPlanContrib(in.Accounts[i].OwnerID, amount)
		}
	}
	effTax := a.TaxRate // share of gross income lost to tax, for withdrawal strategies

	res.Months = make([]MonthRow, 0, n)
	deflator := 1.0
	for t := range n {
		m := in.Start.AddMonths(t)
		if t > 0 {
			deflator = priceIndex[t]
		}
		row := MonthRow{Month: m, Deflator: deflator}
		var flow *Flow
		if in.Detail {
			flow = newFlow(in)
			row.Flow = flow
		}

		if tx != nil && (t == 0 || m.Month == 1) {
			tx.beginYear(m, t == 0, deflator, tx.expectedYear(in, resolved, m, t, priceIndex[12*(t/12)]))
			if tx.rate > 0 {
				effTax = tx.rate
			}
		}

		// 1. Investment growth on the balances carried in. Deposit interest is
		// savings income every year; other gains are taxed when withdrawn.
		for i := range balances {
			g := balances[i] * rate(t, i)
			if tx != nil && g > 0 && in.Accounts[i].Kind == domain.AccountDeposit {
				tx.addSavings(in.Accounts[i].OwnerID, g)
			}
			balances[i] += g
			if in.Accounts[i].Kind == domain.AccountRealEstate {
				row.Appreciation += g
			} else {
				row.Returns += g
			}
		}

		// 2. Income, tax and expenses.
		var spendingRate float64 // average monthly spending, for financial independence
		for k, inc := range in.Incomes {
			v := incomeThisMonth(inc, resolved, m, t, in.Start, priceIndex[12*(t/12)])
			row.Income += v
			if tx != nil {
				tx.addIncome(inc.Kind, inc.OwnerID, v)
			}
			if flow != nil {
				flow.Income[k] = v
			}
		}
		if tx != nil {
			row.Tax = tx.monthCharge(m)
		} else {
			row.Tax = row.Income * a.TaxRate
		}

		// Withdrawal strategy: scale planned expenses to what it allows.
		savings := 0.0
		for i, acc := range in.Accounts {
			if acc.Kind != domain.AccountRealEstate {
				savings += balances[i]
			}
		}
		factor := policy.update(t, m, resolved, savings, priceIndex, func() yearInputs {
			var y yearInputs
			infl := priceIndex[12*(t/12)]
			for _, inc := range in.Incomes {
				if resolved.ActiveIn(inc.Start, inc.End, inc.StartMS, inc.EndMS, m) {
					y.netIncome += inc.Annual().Euros() * streamGrowth(inc.AnnualGrowth, t, infl) * (1 - effTax)
				}
			}
			for _, e := range in.Expenses {
				if resolved.ActiveIn(e.Start, e.End, e.StartMS, e.EndMS, m) {
					y.expenses += e.Annual().Euros() * streamGrowth(e.AnnualGrowth, t, infl)
				}
			}
			for i := range in.Debts {
				if debts[i] > 0 {
					y.debt += payment[i] * 12
				}
			}
			return y
		})
		row.SpendFactor = factor
		if policy.started && res.WithdrawalFrom.IsZero() {
			res.WithdrawalFrom = m
		}

		for k, e := range in.Expenses {
			amount, avg := expenseThisMonth(e, resolved, m, t, in.Start, priceIndex[12*(t/12)])
			amount *= factor
			row.Expenses += amount
			spendingRate += avg
			if flow != nil {
				flow.Expenses[k] = amount
			}
		}

		// 3. Debt payments (fixed instalment, French amortisation).
		for i, d := range in.Debts {
			if debts[i] <= 0 {
				continue
			}
			interest := debts[i] * d.AnnualRate / 12
			pay := math.Min(payment[i], debts[i]+interest)
			debts[i] += interest - pay
			if debts[i] < 0.005 {
				debts[i] = 0
			}
			row.DebtPayments += pay
			spendingRate += pay
			if flow != nil {
				flow.Debts[i] += pay
			}
			if debts[i] == 0 && !paidOff[i] {
				paidOff[i] = true
				res.PaidOff = append(res.PaidOff, DebtPaidOff{Debt: d, Month: m})
				freed[i] = -payment[i] // negative: starts next month
			}
		}

		// 3b. Early repayments (amortizaciones anticipadas). They are optional,
		// so together they are capped to this month's surplus plus what the
		// liquid accounts hold above the reserve once any deficit this month is
		// covered. Pension plans and property never fund them. A repayment
		// therefore never causes a shortfall in the month it is made, though
		// the money it spends is no longer there to cover later months.
		before := row.Savings()
		liquidNow := 0.0
		for i, acc := range in.Accounts {
			if fundsPrepayments(acc.Kind) {
				liquidNow += balances[i]
			}
		}
		available := math.Max(0, before) +
			math.Max(0, liquidNow+math.Min(0, before)-a.PrepaymentReserve.Euros()*priceIndex[t])
		locked := pensionLocked(m)
		for _, pp := range in.Prepayments {
			i, ok := debtIdx[pp.DebtID]
			if !ok || !pp.DueIn(resolved, m, in.Start) {
				continue
			}
			if debts[i] <= 0 {
				// The debt is gone: send the payment to the chosen account, if any.
				if to, ok := target(pp.RedirectAccountID); ok {
					amount := math.Min(pp.Amount.Euros(), available)
					if amount > 0 {
						available -= amount
						deposit(to, amount)
						row.Redirected += amount
						if flow != nil {
							flow.Contributions[to] += amount
						}
					}
				}
				continue
			}
			amount := math.Min(pp.Amount.Euros(), debts[i])
			cost := amount * (1 + pp.FeeRate)
			if cost > available {
				amount, cost = available/(1+pp.FeeRate), available
			}
			if amount <= 0 {
				continue
			}
			available -= cost
			debts[i] -= amount
			row.DebtPayments += cost
			row.Prepayments += cost
			if flow != nil {
				flow.Debts[i] += cost
			}
			if debts[i] < 0.005 {
				debts[i] = 0
				if !paidOff[i] {
					paidOff[i] = true
					res.PaidOff = append(res.PaidOff, DebtPaidOff{Debt: in.Debts[i], Month: m})
					freed[i] = -payment[i]
				}
				continue
			}
			// "Reducir cuota": same end date, recalculated instalment.
			// "Reducir plazo": same instalment, so the debt simply ends sooner.
			if pp.Mode == domain.ReducePayment && lastT[i] > t {
				payment[i] = domain.Annuity(debts[i], in.Debts[i].AnnualRate, lastT[i]-t)
			}
		}

		// 4. Surplus funds contributions, then the surplus account; a deficit
		// draws accounts down in order.
		cash := row.Savings() - row.Redirected
		row.Contributions += row.Redirected
		if cash > 0 {
			for i, acc := range in.Accounts {
				if !resolved.ContributingIn(acc, m) {
					continue
				}
				c := math.Min(acc.MonthlyContribution.Euros(), cash)
				if c <= 0 {
					continue
				}
				deposit(i, c)
				row.Contributions += c
				cash -= c
				if flow != nil {
					flow.Contributions[i] += c
				}
			}
			// Instalments freed by paid-off debts go to their chosen account.
			for i, d := range in.Debts {
				to, ok := target(d.RedirectAccountID)
				if !ok || freed[i] <= 0 || cash <= 0 {
					continue
				}
				c := math.Min(freed[i], cash)
				deposit(to, c)
				row.Contributions += c
				cash -= c
				if flow != nil {
					flow.Contributions[to] += c
				}
			}
			if cash > 0 {
				row.SurplusSaved = cash
				if surplusIdx >= 0 {
					deposit(surplusIdx, cash)
				} else {
					// No account to hold it: the money is spent.
					cash = 0
				}
			}
		} else if cash < 0 {
			// draw takes need from the accounts in withdrawal order and returns
			// what they could not cover.
			draw := func(need float64, liquidOnly bool) float64 {
				for _, i := range order {
					if need <= 0 {
						break
					}
					kind := in.Accounts[i].Kind
					if (locked && kind == domain.AccountPension) || (liquidOnly && !fundsPrepayments(kind)) {
						continue
					}
					take := math.Min(balances[i], need)
					if take <= 0 {
						continue
					}
					if tx != nil {
						acc := in.Accounts[i]
						switch acc.Kind {
						case domain.AccountPension: // taxed as work income
							tx.addPlanWithdrawal(acc.OwnerID, take)
						case domain.AccountInvestment, domain.AccountOther: // only the gain is taxed
							if gain := take * math.Max(0, 1-basis[i]/balances[i]); gain > 0 {
								tx.addSavings(acc.OwnerID, gain)
							}
						}
					}
					if balances[i] > 0 {
						basis[i] -= take * basis[i] / balances[i]
					}
					balances[i] -= take
					row.Withdrawals += take
					if flow != nil {
						flow.Withdrawals[i] += take
					}
					need -= take
				}
				return need
			}
			// The repayments' share of the deficit comes from liquid accounts
			// only, whatever the withdrawal order says about pension plans.
			need := -cash
			prepaid := math.Min(need, math.Max(0, row.Prepayments+row.Redirected-math.Max(0, before)))
			need += draw(prepaid, true) - prepaid
			need = draw(need, false)
			if need > 0.005 {
				row.Shortfall = need
				res.Shortfall += need
				if res.FailMonth.IsZero() {
					res.FailMonth = m
				}
			}
		}

		for i := range freed {
			if freed[i] < 0 {
				freed[i] = -freed[i]
			}
		}
		row.Balances = append([]float64(nil), balances...)
		for _, d := range debts {
			row.DebtBalance += d
		}
		row.NetWorth = row.Assets() - row.DebtBalance
		res.Months = append(res.Months, row)
		if tx != nil && (m.Month == 12 || t == n-1) {
			tx.endYear()
		}

		// 5. Financial independence (FIRE variants). Streams bound to it change
		// from next month.
		var liquid, weighted float64
		for i, acc := range in.Accounts {
			if acc.Kind != domain.AccountRealEstate {
				liquid += balances[i]
				weighted += balances[i] * a.AnnualReturn(acc)
			}
		}
		for _, ms := range in.Milestones {
			if _, done := resolved[ms.ID]; done || !ms.Conditional() {
				continue
			}
			if fiReached(ms, in.People, m, liquid, weighted, spendingRate*12, deflator, a.Inflation) {
				resolved[ms.ID] = m.AddMonths(1)
			}
		}
	}

	if tx != nil {
		res.TaxYears = tx.years
	}
	end = res.Months[len(res.Months)-1].Month
	for _, ms := range in.Milestones {
		hit := MilestoneHit{Milestone: ms}
		if month, ok := resolved[ms.ID]; ok && !end.Before(month) {
			hit.Month = month
		}
		res.Milestones = append(res.Milestones, hit)
	}
	return res
}

// fiReached evaluates a financial-independence milestone at the end of month m.
// liquid is the savings outside property, weighted the sum of balance ×
// expected return (so weighted/liquid is the portfolio's expected return),
// yearlySpending the current spending rate (expenses + debt payments, nominal)
// and deflator the inflation since the start (today's euros → nominal).
func fiReached(ms domain.Milestone, people []domain.Person, m domain.Month,
	liquid, weighted, yearlySpending, deflator, inflation float64) bool {
	if liquid <= 0 {
		return false
	}
	switch ms.FIVariant {
	case domain.FILean, domain.FIFat:
		return liquid >= ms.FIMultiple*ms.FIAmount.Euros()*deflator
	case domain.FIBarista:
		uncovered := yearlySpending - ms.FIAmount.Euros()*deflator
		return uncovered <= 0 || liquid >= ms.FIMultiple*uncovered
	case domain.FICoast:
		if yearlySpending <= 0 {
			return false
		}
		target := domain.Month{}
		for _, p := range people {
			if ms.PersonID != nil && p.ID == *ms.PersonID {
				target = p.Birth.AddMonths(ms.Age * 12)
			}
		}
		if target.IsZero() {
			return false
		}
		// Grow today's savings, with no more contributions, until the target
		// age, and compare with the spending then (which rises with inflation).
		years := math.Max(0, float64(m.MonthsUntil(target))/12)
		growth := weighted / liquid
		return liquid*math.Pow(1+growth, years) >= ms.FIMultiple*yearlySpending*math.Pow(1+inflation, years)
	default: // classic FIRE
		return yearlySpending > 0 && liquid >= ms.FIMultiple*yearlySpending
	}
}

// streamGrowth is how much a stream has grown by month t: its own yearly rate,
// or the year-stepped price index when it follows inflation.
func streamGrowth(own *float64, t int, inflationIndex float64) float64 {
	if own != nil {
		return growth(*own, t)
	}
	return inflationIndex
}

func incomeThisMonth(inc domain.Income, r domain.Resolved, m domain.Month, t int, start domain.Month, inflationIndex float64) float64 {
	from, to, ok := r.Window(inc.Start, inc.End, inc.StartMS, inc.EndMS)
	if !ok || !domain.Active(from, to, m) {
		return 0
	}
	amount := inc.Amount.Euros() * streamGrowth(inc.AnnualGrowth, t, inflationIndex)
	switch inc.PaymentsPerYear {
	case 14: // Spanish salaries: two extra payments, in June and December.
		if m.Month == 6 || m.Month == 12 {
			return 2 * amount
		}
		return amount
	case 1:
		if m.Month == anchorMonth(from, start) {
			return amount
		}
		return 0
	default:
		return amount
	}
}

// expenseThisMonth returns the amount paid in month m and the stream's average
// monthly cost while active (yearly payments spread over 12 months).
func expenseThisMonth(e domain.Expense, r domain.Resolved, m domain.Month, t int, start domain.Month, inflationIndex float64) (amount, monthlyAvg float64) {
	from, to, ok := r.Window(e.Start, e.End, e.StartMS, e.EndMS)
	if !ok || !domain.Active(from, to, m) {
		return 0, 0
	}
	payment := e.Amount.Euros() * streamGrowth(e.AnnualGrowth, t, inflationIndex)
	if e.PaymentsPerYear == 1 {
		if m.Month == anchorMonth(from, start) {
			return payment, payment / 12
		}
		return 0, payment / 12
	}
	return payment, payment
}

// anchorMonth is the calendar month a yearly payment falls in.
func anchorMonth(streamStart, planStart domain.Month) int {
	if !streamStart.IsZero() {
		return streamStart.Month
	}
	return planStart.Month
}

// fundsPrepayments reports whether money in an account of this kind can pay
// for an early repayment. Pension plans and property never can.
func fundsPrepayments(k domain.AccountKind) bool {
	return k == domain.AccountCash || k == domain.AccountDeposit || k == domain.AccountInvestment || k == domain.AccountOther
}

// surplusAccount picks the index of the account that receives surplus cash.
func surplusAccount(accounts []domain.Account, chosen *int64) int {
	if chosen != nil {
		for i, a := range accounts {
			if a.ID == *chosen {
				return i
			}
		}
	}
	for i, a := range accounts {
		if a.Kind == domain.AccountCash {
			return i
		}
	}
	for i, a := range accounts {
		if a.Kind != domain.AccountRealEstate {
			return i
		}
	}
	return -1
}

// drawdownOrder lists account indexes in withdrawal order (kinds in the given
// order, DefaultOrder when it is not a valid permutation).
func drawdownOrder(accounts []domain.Account, kinds []domain.AccountKind) []int {
	if ParseOrder(FormatOrder(kinds)) == nil {
		kinds = DefaultOrder
	}
	var order []int
	for _, k := range kinds {
		for i, a := range accounts {
			if a.Kind == k {
				order = append(order, i)
			}
		}
	}
	return order
}

// YearRow aggregates one calendar year of a projection: flows are summed,
// balances are taken from the year's last month.
type YearRow struct {
	Year     int
	Last     MonthRow // last projected month of the year (balances, net worth)
	Deflator float64  // of the last month

	Income, Tax, Expenses, DebtPayments, Prepayments, Contributions, Withdrawals, Shortfall float64
	SurplusSaved                                                                            float64
	Returns, Appreciation                                                                   float64
	Months                                                                                  int   // projected months in the year (12 except the first and last years)
	Flow                                                                                    *Flow // summed breakdown, when the months have one
}

// Savings is income after tax, expenses and debt payments for the year.
func (y YearRow) Savings() float64 { return y.Income - y.Tax - y.Expenses - y.DebtPayments }

// Years groups the monthly rows by calendar year.
func (r Result) Years() []YearRow {
	var out []YearRow
	for _, m := range r.Months {
		if len(out) == 0 || out[len(out)-1].Year != m.Month.Year {
			out = append(out, YearRow{Year: m.Month.Year})
		}
		y := &out[len(out)-1]
		y.Income += m.Income
		y.Tax += m.Tax
		y.Expenses += m.Expenses
		y.DebtPayments += m.DebtPayments
		y.Prepayments += m.Prepayments
		y.Contributions += m.Contributions
		y.Withdrawals += m.Withdrawals
		y.Shortfall += m.Shortfall
		y.SurplusSaved += m.SurplusSaved
		y.Returns += m.Returns
		y.Appreciation += m.Appreciation
		y.Months++
		if m.Flow != nil {
			if y.Flow == nil {
				y.Flow = &Flow{
					Income:        make([]float64, len(m.Flow.Income)),
					Expenses:      make([]float64, len(m.Flow.Expenses)),
					Debts:         make([]float64, len(m.Flow.Debts)),
					Contributions: make([]float64, len(m.Flow.Contributions)),
					Withdrawals:   make([]float64, len(m.Flow.Withdrawals)),
				}
			}
			y.Flow.add(m.Flow)
		}
		y.Last = m
		y.Deflator = m.Deflator
	}
	return out
}

// Peak returns the month with the highest net worth in today's euros.
func (r Result) Peak() (MonthRow, bool) {
	if len(r.Months) == 0 {
		return MonthRow{}, false
	}
	best := r.Months[0]
	for _, m := range r.Months[1:] {
		if m.NetWorth/m.Deflator > best.NetWorth/best.Deflator {
			best = m
		}
	}
	return best, true
}
