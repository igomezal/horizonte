package domain

import (
	"math"
	"slices"
	"strings"
)

// Cents is an amount of money in euro cents. Money is stored as integers to
// avoid floating-point rounding drift.
type Cents int64

// Euros returns the amount in euros.
func (c Cents) Euros() float64 { return float64(c) / 100 }

// FromEuros converts euros to cents, rounding to the nearest cent.
func FromEuros(e float64) Cents { return Cents(math.Round(e * 100)) }

// Errors maps a form field name to an i18n message key.
type Errors map[string]string

// Add records msgKey for field unless the field already has an error.
func (e Errors) Add(field, msgKey string) {
	if _, ok := e[field]; !ok {
		e[field] = msgKey
	}
}

// Validation message keys (translated in the locale files).
const (
	ErrRequired     = "form.error.required"
	ErrTooLong      = "form.error.too_long"
	ErrNegative     = "form.error.negative"
	ErrRange        = "form.error.range"
	ErrChoice       = "form.error.choice"
	ErrEndBeforeStr = "form.error.end_before_start"
	ErrFuture       = "form.error.future"
)

const maxNameLen = 80

func validateName(name string, errs Errors) {
	switch n := len([]rune(strings.TrimSpace(name))); {
	case n == 0:
		errs.Add("name", ErrRequired)
	case n > maxNameLen:
		errs.Add("name", ErrTooLong)
	}
}

func validateRate(field string, r *float64, min, max float64, errs Errors) {
	if r != nil && (*r < min || *r > max) {
		errs.Add(field, ErrRange)
	}
}

func validateNonNegative(field string, c Cents, errs Errors) {
	if c < 0 {
		errs.Add(field, ErrNegative)
	}
}

func validateSpan(start, end Month, errs Errors) {
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		errs.Add("end", ErrEndBeforeStr)
	}
}

func oneOf[T comparable](v T, allowed []T) bool {
	return slices.Contains(allowed, v)
}

// ---------- People ----------

// MaxPeople is the number of people a plan can model (a single person or a couple).
const MaxPeople = 2

// Person is someone whose finances the plan models.
type Person struct {
	ID             int64
	PlanID         int64
	Name           string
	Birth          Month
	LifeExpectancy int // age in years until which the plan runs
}

// AgeAt returns the person's age in whole years in month m.
func (p Person) AgeAt(m Month) int {
	return p.Birth.MonthsUntil(m) / 12
}

// Validate checks the person against now (the current month).
func (p Person) Validate(now Month) Errors {
	errs := Errors{}
	validateName(p.Name, errs)
	if p.Birth.IsZero() {
		errs.Add("birth", ErrRequired)
	} else if now.Before(p.Birth) {
		errs.Add("birth", ErrFuture)
	} else if p.AgeAt(now) > 110 {
		errs.Add("birth", ErrRange)
	}
	if p.LifeExpectancy < 50 || p.LifeExpectancy > 120 {
		errs.Add("life_expectancy", ErrRange)
	} else if !p.Birth.IsZero() && p.LifeExpectancy <= p.AgeAt(now) {
		errs.Add("life_expectancy", ErrRange)
	}
	return errs
}

// ---------- Accounts ----------

// AccountKind classifies an account. The kind decides default returns and,
// in phase 9, how Spanish taxes apply.
type AccountKind string

const (
	AccountCash       AccountKind = "cash"       // current account
	AccountDeposit    AccountKind = "deposit"    // savings account / fixed-term deposit
	AccountInvestment AccountKind = "investment" // brokerage, funds, shares
	AccountPension    AccountKind = "pension"    // plan de pensiones (tax-deferred)
	AccountRealEstate AccountKind = "realestate" // property value
	AccountOther      AccountKind = "other"
)

// AccountKinds lists account kinds in display order.
var AccountKinds = []AccountKind{AccountCash, AccountDeposit, AccountInvestment, AccountPension, AccountRealEstate, AccountOther}

// HasCostBasis reports whether gains on this kind are worked out from a cost.
func (k AccountKind) HasCostBasis() bool {
	return k == AccountInvestment || k == AccountOther || k == AccountRealEstate
}

// Liquid reports whether money in this kind of account can be spent freely,
// which excludes pension plans and property.
func (k AccountKind) Liquid() bool {
	return k == AccountCash || k == AccountDeposit || k == AccountInvestment || k == AccountOther
}

// PropertySale is the planned sale of part or all of a real-estate account.
type PropertySale struct {
	Month Month  // month of the sale; a milestone overrides it
	MS    *int64 // sold when this milestone happens
	Share float64
	// CostRate is the selling costs as a share of the sale price.
	CostRate float64
	// DestinationID receives the money. Nil means the surplus account.
	DestinationID *int64
	// MainResidence makes the gain exempt for an owner over 65 (art. 33.4 LIRPF).
	MainResidence bool
}

// DueIn returns whether the sale happens in month now. Like one-off early
// repayments, sales dated before the plan starts never happen.
func (s PropertySale) DueIn(r Resolved, now, planStart Month) bool {
	start, _, ok := r.Window(s.Month, Month{}, s.MS, nil)
	return ok && !start.IsZero() && start == now && !now.Before(planStart)
}

// Lapsed reports whether the sale's month is before the plan starts, so it
// will never happen.
func (s PropertySale) Lapsed(r Resolved, planStart Month) bool {
	start, _, ok := r.Window(s.Month, Month{}, s.MS, nil)
	return ok && !start.IsZero() && start.Before(planStart)
}

// Account is something you own that has a balance.
type Account struct {
	ID                  int64
	PlanID              int64
	Name                string
	Kind                AccountKind
	OwnerID             *int64 // nil = joint
	Balance             Cents
	MonthlyContribution Cents
	AnnualReturn        *float64 // nil = use the plan's default for this kind
	ContributionEndMS   *int64   // contributions stop when this milestone happens
	// CostBasis is what the holdings cost, for the gain on investment, other
	// and real-estate accounts. Nil counts the starting balance as the cost.
	CostBasis *Cents
	Sale      *PropertySale // real-estate accounts only; nil = never sold
}

// Validate checks the account.
func (a Account) Validate() Errors {
	errs := Errors{}
	validateName(a.Name, errs)
	if !oneOf(a.Kind, AccountKinds) {
		errs.Add("kind", ErrChoice)
	}
	validateNonNegative("balance", a.Balance, errs)
	validateNonNegative("contribution", a.MonthlyContribution, errs)
	if a.CostBasis != nil {
		validateNonNegative("cost_basis", *a.CostBasis, errs)
	}
	validateRate("return", a.AnnualReturn, -0.5, 0.5, errs)
	if s := a.Sale; s != nil {
		if a.Kind != AccountRealEstate {
			errs.Add("sale_enabled", ErrChoice)
		}
		if s.Month.IsZero() && s.MS == nil {
			errs.Add("sale_month", ErrRequired)
		}
		if s.Share <= 0 || s.Share > 1 {
			errs.Add("sale_share", ErrRange)
		}
		if s.CostRate < 0 || s.CostRate > 0.5 {
			errs.Add("sale_cost", ErrRange)
		}
	}
	return errs
}

// ---------- Income ----------

// IncomeKind classifies an income stream.
type IncomeKind string

const (
	IncomeSalary       IncomeKind = "salary"
	IncomeSelfEmployed IncomeKind = "selfemployed"
	IncomeRental       IncomeKind = "rental"
	IncomePension      IncomeKind = "pension"
	IncomeOther        IncomeKind = "other"
)

// IncomeKinds lists income kinds in display order.
var IncomeKinds = []IncomeKind{IncomeSalary, IncomeSelfEmployed, IncomeRental, IncomePension, IncomeOther}

// IncomePayments are the allowed payments per year. 14 covers Spanish salaries
// paid in 14 instalments (two extra payments in summer and December).
var IncomePayments = []int{12, 14, 1}

// Income is a stream of money coming in. Amounts are gross (before tax).
type Income struct {
	ID              int64
	PlanID          int64
	Name            string
	Kind            IncomeKind
	OwnerID         *int64
	Amount          Cents // per payment
	PaymentsPerYear int
	AnnualGrowth    *float64 // nil = grows with inflation
	Start, End      Month    // zero = from now / open-ended
	StartMS, EndMS  *int64   // milestones that override Start / End
}

// Annual returns the yearly gross amount.
func (i Income) Annual() Cents { return i.Amount * Cents(i.PaymentsPerYear) }

// MonthlyAverage returns the yearly amount spread over 12 months.
func (i Income) MonthlyAverage() Cents { return i.Annual() / 12 }

// Validate checks the income.
func (i Income) Validate() Errors {
	errs := Errors{}
	validateName(i.Name, errs)
	if !oneOf(i.Kind, IncomeKinds) {
		errs.Add("kind", ErrChoice)
	}
	if i.Amount <= 0 {
		errs.Add("amount", ErrRequired)
	}
	if !oneOf(i.PaymentsPerYear, IncomePayments) {
		errs.Add("payments", ErrChoice)
	}
	validateRate("growth", i.AnnualGrowth, -0.2, 0.3, errs)
	if i.StartMS == nil && i.EndMS == nil {
		validateSpan(i.Start, i.End, errs)
	}
	return errs
}

// ---------- Expenses ----------

// ExpenseCategory groups expenses for the cash-flow views.
type ExpenseCategory string

const (
	ExpenseHousing   ExpenseCategory = "housing"
	ExpenseFood      ExpenseCategory = "food"
	ExpenseTransport ExpenseCategory = "transport"
	ExpenseUtilities ExpenseCategory = "utilities"
	ExpenseHealth    ExpenseCategory = "health"
	ExpenseLeisure   ExpenseCategory = "leisure"
	ExpenseEducation ExpenseCategory = "education"
	ExpenseOther     ExpenseCategory = "other"
)

// ExpenseCategories lists categories in display order.
var ExpenseCategories = []ExpenseCategory{ExpenseHousing, ExpenseFood, ExpenseTransport, ExpenseUtilities, ExpenseHealth, ExpenseLeisure, ExpenseEducation, ExpenseOther}

// ExpensePayments are the allowed payments per year.
var ExpensePayments = []int{12, 1}

// Expense is a stream of money going out. Debt repayments are not expenses;
// they belong to the debt.
type Expense struct {
	ID              int64
	PlanID          int64
	Name            string
	Category        ExpenseCategory
	Amount          Cents
	PaymentsPerYear int
	AnnualGrowth    *float64 // nil = grows with inflation
	Start, End      Month
	StartMS, EndMS  *int64 // milestones that override Start / End
}

// Annual returns the yearly amount.
func (e Expense) Annual() Cents { return e.Amount * Cents(e.PaymentsPerYear) }

// MonthlyAverage returns the yearly amount spread over 12 months.
func (e Expense) MonthlyAverage() Cents { return e.Annual() / 12 }

// Validate checks the expense.
func (e Expense) Validate() Errors {
	errs := Errors{}
	validateName(e.Name, errs)
	if !oneOf(e.Category, ExpenseCategories) {
		errs.Add("category", ErrChoice)
	}
	if e.Amount <= 0 {
		errs.Add("amount", ErrRequired)
	}
	if !oneOf(e.PaymentsPerYear, ExpensePayments) {
		errs.Add("payments", ErrChoice)
	}
	validateRate("growth", e.AnnualGrowth, -0.2, 0.3, errs)
	if e.StartMS == nil && e.EndMS == nil {
		validateSpan(e.Start, e.End, errs)
	}
	return errs
}

// ---------- Debts ----------

// DebtKind classifies a debt.
type DebtKind string

const (
	DebtMortgage DebtKind = "mortgage"
	DebtCar      DebtKind = "car"
	DebtPersonal DebtKind = "personal"
	DebtStudent  DebtKind = "student"
	DebtCard     DebtKind = "card"
	DebtOther    DebtKind = "other"
)

// DebtKinds lists debt kinds in display order.
var DebtKinds = []DebtKind{DebtMortgage, DebtCar, DebtPersonal, DebtStudent, DebtCard, DebtOther}

// Debt is money owed, repaid with a fixed monthly payment (French amortisation,
// the usual scheme for Spanish mortgages and loans).
type Debt struct {
	ID             int64
	PlanID         int64
	Name           string
	Kind           DebtKind
	Balance        Cents   // outstanding principal
	AnnualRate     float64 // nominal annual rate (TIN)
	MonthlyPayment Cents
	// RedirectAccountID receives the freed instalment once the debt is paid
	// off. Nil: it becomes unallocated money like any other surplus.
	RedirectAccountID *int64
}

// Validate checks the debt.
func (d Debt) Validate() Errors {
	errs := Errors{}
	validateName(d.Name, errs)
	if !oneOf(d.Kind, DebtKinds) {
		errs.Add("kind", ErrChoice)
	}
	if d.Balance <= 0 {
		errs.Add("balance", ErrRequired)
	}
	rate := d.AnnualRate
	validateRate("rate", &rate, 0, 0.5, errs)
	if d.MonthlyPayment <= 0 {
		errs.Add("payment", ErrRequired)
	}
	return errs
}

// MonthsToPayoff returns how many monthly payments clear the debt, or -1 when
// the payment does not even cover the interest (the debt never shrinks).
func (d Debt) MonthsToPayoff() int {
	b, p := d.Balance.Euros(), d.MonthlyPayment.Euros()
	if b <= 0 {
		return 0
	}
	if p <= 0 {
		return -1
	}
	r := d.AnnualRate / 12
	if r == 0 {
		return int(math.Ceil(b / p))
	}
	if p <= b*r {
		return -1
	}
	n := -math.Log(1-r*b/p) / math.Log(1+r)
	// Guard against floating-point noise just above a whole number.
	return int(math.Ceil(n - 1e-9))
}

// PayoffMonth returns the month of the last payment counting from now, or the
// zero Month when the debt is never repaid.
func (d Debt) PayoffMonth(now Month) Month {
	n := d.MonthsToPayoff()
	if n < 0 {
		return Month{}
	}
	if n == 0 {
		return now
	}
	return now.AddMonths(n - 1)
}

// ---------- Summary ----------

// Summary is the snapshot shown at the top of Current Finances. Monthly figures
// only include streams active in the current month and are before tax.
type Summary struct {
	Assets, Debts, NetWorth Cents

	MonthlyIncome        Cents
	MonthlyExpenses      Cents
	MonthlyDebtPayments  Cents
	MonthlyContributions Cents
	// MonthlyUnallocated is what is left after expenses, debt payments and
	// account contributions. Negative means the plan spends more than it earns.
	MonthlyUnallocated Cents
}

// Summarize computes the snapshot for month now. r resolves the milestones
// streams are bound to (conditional milestones count as not reached).
func Summarize(now Month, r Resolved, accounts []Account, incomes []Income, expenses []Expense, debts []Debt) Summary {
	var s Summary
	for _, a := range accounts {
		s.Assets += a.Balance
		if r.ContributingIn(a, now) {
			s.MonthlyContributions += a.MonthlyContribution
		}
	}
	for _, d := range debts {
		s.Debts += d.Balance
		s.MonthlyDebtPayments += d.MonthlyPayment
	}
	for _, i := range incomes {
		if r.ActiveIn(i.Start, i.End, i.StartMS, i.EndMS, now) {
			s.MonthlyIncome += i.MonthlyAverage()
		}
	}
	for _, e := range expenses {
		if r.ActiveIn(e.Start, e.End, e.StartMS, e.EndMS, now) {
			s.MonthlyExpenses += e.MonthlyAverage()
		}
	}
	s.NetWorth = s.Assets - s.Debts
	s.MonthlyUnallocated = s.MonthlyIncome - s.MonthlyExpenses - s.MonthlyDebtPayments - s.MonthlyContributions
	return s
}
