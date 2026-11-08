// Package backup reads and writes a plan as a JSON file, so it can be kept
// outside the database, moved to another instance or edited by hand.
//
// The file format is independent of the database and of the engine's types:
// fields have stable snake_case names, money is in euros, rates are ratios
// (0.025 = 2,5 %) and months are "YYYY-MM". Records refer to each other by
// the "id" they have in the file.
package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/store"
)

// Format identifies a single-plan file and BundleFormat a file with several
// plans; Version is bumped on incompatible changes.
const (
	Format       = "horizonte/plan"
	BundleFormat = "horizonte/plans"
	Version      = 1
)

// maxPlans caps the plans in a bundle.
const maxPlans = 50

// MaxSize is the largest file accepted on import.
const MaxSize = 2 << 20

// maxRecords caps each list, to keep a malformed file from creating
// thousands of rows.
const maxRecords = 500

// File is the JSON document.
type File struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	ExportedAt string `json:"exported_at,omitempty"`
	Plan       Plan   `json:"plan"`
}

// Bundle is the JSON document with every plan.
type Bundle struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	ExportedAt string `json:"exported_at,omitempty"`
	Plans      []Plan `json:"plans"`
}

// Plan is a plan's records and assumptions. In a bundle, ID identifies the
// plan, ParentID the plan it was copied from and Active the plan in use.
type Plan struct {
	ID          int64        `json:"id,omitempty"`
	ParentID    *int64       `json:"parent_id,omitempty"`
	Active      bool         `json:"active,omitempty"`
	Name        string       `json:"name"`
	People      []Person     `json:"people"`
	Milestones  []Milestone  `json:"milestones"`
	Accounts    []Account    `json:"accounts"`
	Incomes     []Income     `json:"incomes"`
	Expenses    []Expense    `json:"expenses"`
	Debts       []Debt       `json:"debts"`
	Prepayments []Prepayment `json:"prepayments"`
	Assumptions Assumptions  `json:"assumptions"`
}

type Person struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Birth          string `json:"birth"`
	LifeExpectancy int    `json:"life_expectancy"`
}

type Milestone struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	PersonID   *int64  `json:"person_id,omitempty"`
	Age        int     `json:"age,omitempty"`
	Date       string  `json:"date,omitempty"`
	FIMultiple float64 `json:"fi_multiple,omitempty"`
	FIVariant  string  `json:"fi_variant,omitempty"`
	FIAmount   float64 `json:"fi_amount,omitempty"`
}

type Account struct {
	ID                  int64    `json:"id"`
	Name                string   `json:"name"`
	Kind                string   `json:"kind"`
	OwnerID             *int64   `json:"owner_id,omitempty"`
	Balance             float64  `json:"balance"`
	MonthlyContribution float64  `json:"monthly_contribution,omitempty"`
	AnnualReturn        *float64 `json:"annual_return,omitempty"`
	ContributionEndMS   *int64   `json:"contribution_end_milestone_id,omitempty"`
	CostBasis           *float64 `json:"cost_basis,omitempty"`
}

// span is the start and end shared by streams and repayments.
type span struct {
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	StartMS *int64 `json:"start_milestone_id,omitempty"`
	EndMS   *int64 `json:"end_milestone_id,omitempty"`
}

type Income struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	OwnerID         *int64   `json:"owner_id,omitempty"`
	Amount          float64  `json:"amount"`
	PaymentsPerYear int      `json:"payments_per_year"`
	AnnualGrowth    *float64 `json:"annual_growth,omitempty"`
	span
}

type Expense struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Category        string   `json:"category"`
	Amount          float64  `json:"amount"`
	PaymentsPerYear int      `json:"payments_per_year"`
	AnnualGrowth    *float64 `json:"annual_growth,omitempty"`
	span
}

type Debt struct {
	ID                int64   `json:"id"`
	Name              string  `json:"name"`
	Kind              string  `json:"kind"`
	Balance           float64 `json:"balance"`
	AnnualRate        float64 `json:"annual_rate"`
	MonthlyPayment    float64 `json:"monthly_payment"`
	RedirectAccountID *int64  `json:"redirect_account_id,omitempty"`
}

type Prepayment struct {
	ID                int64   `json:"id"`
	DebtID            int64   `json:"debt_id"`
	Amount            float64 `json:"amount"`
	Frequency         string  `json:"frequency"`
	Mode              string  `json:"mode"`
	FeeRate           float64 `json:"fee_rate,omitempty"`
	RedirectAccountID *int64  `json:"redirect_account_id,omitempty"`
	span
}

type Assumptions struct {
	Inflation          float64            `json:"inflation"`
	TaxModel           string             `json:"tax_model"`
	TaxRegion          string             `json:"tax_region,omitempty"`
	TaxRate            float64            `json:"tax_rate"`
	Returns            map[string]float64 `json:"returns"`
	Volatility         map[string]float64 `json:"volatility"`
	MCRuns             int                `json:"monte_carlo_runs"`
	SurplusAccountID   *int64             `json:"surplus_account_id,omitempty"`
	Withdrawal         Withdrawal         `json:"withdrawal"`
	Order              []string           `json:"withdrawal_order,omitempty"`
	PensionAvailableMS *int64             `json:"pension_available_milestone_id,omitempty"`
	PrepaymentReserve  float64            `json:"prepayment_reserve,omitempty"`
	FreezeIRPF         bool               `json:"irpf_brackets_frozen,omitempty"`
}

type Withdrawal struct {
	Strategy string  `json:"strategy"`
	StartMS  *int64  `json:"start_milestone_id,omitempty"`
	Rate     float64 `json:"rate"`
	Floor    float64 `json:"floor"`
	Ceiling  float64 `json:"ceiling"`
}

// ---------- export ----------

func month(m domain.Month) string {
	if m.IsZero() {
		return ""
	}
	return m.String()
}

func eur(c domain.Cents) float64 { return c.Euros() }

func optEur(c *domain.Cents) *float64 {
	if c == nil {
		return nil
	}
	v := c.Euros()
	return &v
}

func optCents(e *float64) *domain.Cents {
	if e == nil {
		return nil
	}
	v := domain.FromEuros(*e)
	return &v
}

func mkSpan(start, end domain.Month, sms, ems *int64) span {
	return span{Start: month(start), End: month(end), StartMS: sms, EndMS: ems}
}

// Entry is one plan of a bundle, on export or import.
type Entry struct {
	ID       int64
	ParentID *int64
	Active   bool
	Name     string
	Snapshot store.Snapshot
}

// FromSnapshot builds the file for a plan.
func FromSnapshot(name string, snap store.Snapshot, now time.Time) File {
	return File{Format: Format, Version: Version, ExportedAt: now.UTC().Format(time.RFC3339), Plan: planFrom(name, snap)}
}

// FromEntries builds the file with every plan.
func FromEntries(entries []Entry, now time.Time) Bundle {
	b := Bundle{Format: BundleFormat, Version: Version, ExportedAt: now.UTC().Format(time.RFC3339), Plans: []Plan{}}
	for _, e := range entries {
		p := planFrom(e.Name, e.Snapshot)
		p.ID, p.ParentID, p.Active = e.ID, e.ParentID, e.Active
		b.Plans = append(b.Plans, p)
	}
	return b
}

func planFrom(name string, snap store.Snapshot) Plan {
	p := Plan{Name: name, People: []Person{}, Milestones: []Milestone{}, Accounts: []Account{}, Incomes: []Income{},
		Expenses: []Expense{}, Debts: []Debt{}, Prepayments: []Prepayment{}}
	for _, x := range snap.People {
		p.People = append(p.People, Person{ID: x.ID, Name: x.Name, Birth: month(x.Birth), LifeExpectancy: x.LifeExpectancy})
	}
	for _, x := range snap.Milestones {
		p.Milestones = append(p.Milestones, Milestone{ID: x.ID, Name: x.Name, Kind: string(x.Kind), PersonID: x.PersonID,
			Age: x.Age, Date: month(x.Date), FIMultiple: x.FIMultiple, FIVariant: string(x.FIVariant), FIAmount: eur(x.FIAmount)})
	}
	for _, x := range snap.Accounts {
		p.Accounts = append(p.Accounts, Account{ID: x.ID, Name: x.Name, Kind: string(x.Kind), OwnerID: x.OwnerID,
			Balance: eur(x.Balance), MonthlyContribution: eur(x.MonthlyContribution), AnnualReturn: x.AnnualReturn,
			ContributionEndMS: x.ContributionEndMS, CostBasis: optEur(x.CostBasis)})
	}
	for _, x := range snap.Incomes {
		p.Incomes = append(p.Incomes, Income{ID: x.ID, Name: x.Name, Kind: string(x.Kind), OwnerID: x.OwnerID,
			Amount: eur(x.Amount), PaymentsPerYear: x.PaymentsPerYear, AnnualGrowth: x.AnnualGrowth,
			span: mkSpan(x.Start, x.End, x.StartMS, x.EndMS)})
	}
	for _, x := range snap.Expenses {
		p.Expenses = append(p.Expenses, Expense{ID: x.ID, Name: x.Name, Category: string(x.Category),
			Amount: eur(x.Amount), PaymentsPerYear: x.PaymentsPerYear, AnnualGrowth: x.AnnualGrowth,
			span: mkSpan(x.Start, x.End, x.StartMS, x.EndMS)})
	}
	for _, x := range snap.Debts {
		p.Debts = append(p.Debts, Debt{ID: x.ID, Name: x.Name, Kind: string(x.Kind), Balance: eur(x.Balance),
			AnnualRate: x.AnnualRate, MonthlyPayment: eur(x.MonthlyPayment), RedirectAccountID: x.RedirectAccountID})
	}
	for _, x := range snap.Prepayments {
		p.Prepayments = append(p.Prepayments, Prepayment{ID: x.ID, DebtID: x.DebtID, Amount: eur(x.Amount),
			Frequency: string(x.Frequency), Mode: string(x.Mode), FeeRate: x.FeeRate, RedirectAccountID: x.RedirectAccountID,
			span: mkSpan(x.Start, x.End, x.StartMS, x.EndMS)})
	}
	a := snap.Assumptions
	p.Assumptions = Assumptions{Inflation: a.Inflation, TaxModel: a.TaxModel, TaxRegion: a.TaxRegion, TaxRate: a.TaxRate,
		Returns: kindMap(a.Returns), Volatility: kindMap(a.Volatility), MCRuns: a.MCRuns, SurplusAccountID: a.SurplusAccountID,
		Withdrawal: Withdrawal{Strategy: string(a.Withdrawal.Strategy), StartMS: a.Withdrawal.StartMS,
			Rate: a.Withdrawal.Rate, Floor: a.Withdrawal.Floor, Ceiling: a.Withdrawal.Ceiling},
		PensionAvailableMS: a.PensionAvailableMS, PrepaymentReserve: eur(a.PrepaymentReserve), FreezeIRPF: a.FreezeIRPF}
	for _, k := range a.Order {
		p.Assumptions.Order = append(p.Assumptions.Order, string(k))
	}
	return p
}

func kindMap(m map[domain.AccountKind]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// Write encodes a File or Bundle as indented JSON.
func Write(w io.Writer, f any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(f)
}

// ---------- import ----------

// Import errors. InvalidError carries which record is wrong.
var (
	ErrNotPlan  = errors.New("backup: not a plan file")
	ErrVersion  = errors.New("backup: file from a newer version")
	ErrTooLarge = errors.New("backup: file too large")
)

// InvalidError reports a record that does not pass validation.
type InvalidError struct {
	Plan    string // in a bundle, the plan the record belongs to
	Section string // people, milestones, accounts, incomes, expenses, debts, prepayments, assumptions, plan
	Name    string // the record's name, when it has one
	Field   string // field that failed
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("backup: invalid %s %q: %s", e.Section, e.Name, e.Field)
}

func invalid(section, name string, errs domain.Errors) error {
	for f := range errs {
		return &InvalidError{Section: section, Name: name, Field: f}
	}
	return nil
}

func parseMonth(section, name, field, s string) (domain.Month, error) {
	if s == "" {
		return domain.Month{}, nil
	}
	m, err := domain.ParseMonth(s)
	if err != nil {
		return m, &InvalidError{Section: section, Name: name, Field: field}
	}
	return m, nil
}

// Read decodes and validates a single-plan file or a bundle. now is the
// current month (birth dates in the future are rejected). Snapshots keep the
// file's ids. In a bundle, a parent outside the file is dropped.
func Read(r io.Reader, now domain.Month) ([]Entry, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSize {
		return nil, ErrTooLarge
	}
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, ErrNotPlan
	}
	var plans []Plan
	bundle := head.Format == BundleFormat
	switch head.Format {
	case Format:
		var f File
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, ErrNotPlan
		}
		plans = []Plan{f.Plan}
	case BundleFormat:
		var b Bundle
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, ErrNotPlan
		}
		plans = b.Plans
	default:
		return nil, ErrNotPlan
	}
	if head.Version > Version || head.Version < 1 {
		return nil, ErrVersion
	}
	if len(plans) == 0 {
		return nil, &InvalidError{Section: "plan", Field: "plans"}
	}
	if len(plans) > maxPlans {
		return nil, ErrTooLarge
	}

	ids := map[int64]bool{}
	var out []Entry
	for _, p := range plans {
		if bundle {
			if p.ID <= 0 || ids[p.ID] {
				return nil, &InvalidError{Plan: p.Name, Section: "plan", Field: "id"}
			}
			ids[p.ID] = true
		}
		name, snap, err := readPlan(p, now)
		if err != nil {
			var ie *InvalidError
			if bundle && errors.As(err, &ie) {
				ie.Plan = p.Name
			}
			return nil, err
		}
		e := Entry{Name: name, Snapshot: snap}
		if bundle {
			e.ID, e.ParentID, e.Active = p.ID, p.ParentID, p.Active
		}
		out = append(out, e)
	}
	for i := range out {
		if out[i].ParentID != nil && (!ids[*out[i].ParentID] || *out[i].ParentID == out[i].ID) {
			out[i].ParentID = nil
		}
	}
	return out, nil
}

// readPlan validates one plan and converts it to a snapshot.
func readPlan(p Plan, now domain.Month) (string, store.Snapshot, error) {
	var snap store.Snapshot
	if n := len([]rune(p.Name)); n == 0 || n > 80 {
		return "", snap, &InvalidError{Section: "plan", Field: "name"}
	}
	for _, n := range []int{len(p.Milestones), len(p.Accounts), len(p.Incomes), len(p.Expenses), len(p.Debts), len(p.Prepayments)} {
		if n > maxRecords {
			return "", snap, ErrTooLarge
		}
	}
	if len(p.People) > domain.MaxPeople {
		return "", snap, &InvalidError{Section: "people", Field: "count"}
	}

	// ids seen per section, to check links.
	people, mss, accs, debts := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	unique := func(section, name string, id int64, seen map[int64]bool) error {
		if id <= 0 || seen[id] {
			return &InvalidError{Section: section, Name: name, Field: "id"}
		}
		seen[id] = true
		return nil
	}
	link := func(section, name, field string, id *int64, seen map[int64]bool) error {
		if id != nil && !seen[*id] {
			return &InvalidError{Section: section, Name: name, Field: field}
		}
		return nil
	}

	for _, x := range p.People {
		if err := unique("people", x.Name, x.ID, people); err != nil {
			return "", snap, err
		}
		birth, err := parseMonth("people", x.Name, "birth", x.Birth)
		if err != nil {
			return "", snap, err
		}
		d := domain.Person{ID: x.ID, Name: x.Name, Birth: birth, LifeExpectancy: x.LifeExpectancy}
		if err := invalid("people", x.Name, d.Validate(now)); err != nil {
			return "", snap, err
		}
		snap.People = append(snap.People, d)
	}
	for _, x := range p.Milestones {
		if err := unique("milestones", x.Name, x.ID, mss); err != nil {
			return "", snap, err
		}
		if err := link("milestones", x.Name, "person_id", x.PersonID, people); err != nil {
			return "", snap, err
		}
		date, err := parseMonth("milestones", x.Name, "date", x.Date)
		if err != nil {
			return "", snap, err
		}
		d := domain.Milestone{ID: x.ID, Name: x.Name, Kind: domain.MilestoneKind(x.Kind), PersonID: x.PersonID, Age: x.Age,
			Date: date, FIMultiple: x.FIMultiple, FIVariant: domain.FIVariant(x.FIVariant), FIAmount: domain.FromEuros(x.FIAmount)}
		if err := invalid("milestones", x.Name, d.Validate()); err != nil {
			return "", snap, err
		}
		snap.Milestones = append(snap.Milestones, d)
	}
	for _, x := range p.Accounts {
		if err := unique("accounts", x.Name, x.ID, accs); err != nil {
			return "", snap, err
		}
		if err := link("accounts", x.Name, "owner_id", x.OwnerID, people); err != nil {
			return "", snap, err
		}
		if err := link("accounts", x.Name, "contribution_end_milestone_id", x.ContributionEndMS, mss); err != nil {
			return "", snap, err
		}
		d := domain.Account{ID: x.ID, Name: x.Name, Kind: domain.AccountKind(x.Kind), OwnerID: x.OwnerID,
			Balance: domain.FromEuros(x.Balance), MonthlyContribution: domain.FromEuros(x.MonthlyContribution),
			AnnualReturn: x.AnnualReturn, ContributionEndMS: x.ContributionEndMS, CostBasis: optCents(x.CostBasis)}
		if err := invalid("accounts", x.Name, d.Validate()); err != nil {
			return "", snap, err
		}
		snap.Accounts = append(snap.Accounts, d)
	}
	spanOf := func(section, name string, s span) (domain.Month, domain.Month, error) {
		if err := link(section, name, "start_milestone_id", s.StartMS, mss); err != nil {
			return domain.Month{}, domain.Month{}, err
		}
		if err := link(section, name, "end_milestone_id", s.EndMS, mss); err != nil {
			return domain.Month{}, domain.Month{}, err
		}
		start, err := parseMonth(section, name, "start", s.Start)
		if err != nil {
			return start, start, err
		}
		end, err := parseMonth(section, name, "end", s.End)
		return start, end, err
	}
	incIDs := map[int64]bool{}
	for _, x := range p.Incomes {
		if err := unique("incomes", x.Name, x.ID, incIDs); err != nil {
			return "", snap, err
		}
		if err := link("incomes", x.Name, "owner_id", x.OwnerID, people); err != nil {
			return "", snap, err
		}
		start, end, err := spanOf("incomes", x.Name, x.span)
		if err != nil {
			return "", snap, err
		}
		d := domain.Income{ID: x.ID, Name: x.Name, Kind: domain.IncomeKind(x.Kind), OwnerID: x.OwnerID,
			Amount: domain.FromEuros(x.Amount), PaymentsPerYear: x.PaymentsPerYear, AnnualGrowth: x.AnnualGrowth,
			Start: start, End: end, StartMS: x.StartMS, EndMS: x.EndMS}
		if err := invalid("incomes", x.Name, d.Validate()); err != nil {
			return "", snap, err
		}
		snap.Incomes = append(snap.Incomes, d)
	}
	expIDs := map[int64]bool{}
	for _, x := range p.Expenses {
		if err := unique("expenses", x.Name, x.ID, expIDs); err != nil {
			return "", snap, err
		}
		start, end, err := spanOf("expenses", x.Name, x.span)
		if err != nil {
			return "", snap, err
		}
		d := domain.Expense{ID: x.ID, Name: x.Name, Category: domain.ExpenseCategory(x.Category),
			Amount: domain.FromEuros(x.Amount), PaymentsPerYear: x.PaymentsPerYear, AnnualGrowth: x.AnnualGrowth,
			Start: start, End: end, StartMS: x.StartMS, EndMS: x.EndMS}
		if err := invalid("expenses", x.Name, d.Validate()); err != nil {
			return "", snap, err
		}
		snap.Expenses = append(snap.Expenses, d)
	}
	for _, x := range p.Debts {
		if err := unique("debts", x.Name, x.ID, debts); err != nil {
			return "", snap, err
		}
		if err := link("debts", x.Name, "redirect_account_id", x.RedirectAccountID, accs); err != nil {
			return "", snap, err
		}
		d := domain.Debt{ID: x.ID, Name: x.Name, Kind: domain.DebtKind(x.Kind), Balance: domain.FromEuros(x.Balance),
			AnnualRate: x.AnnualRate, MonthlyPayment: domain.FromEuros(x.MonthlyPayment), RedirectAccountID: x.RedirectAccountID}
		if err := invalid("debts", x.Name, d.Validate()); err != nil {
			return "", snap, err
		}
		snap.Debts = append(snap.Debts, d)
	}
	ppIDs := map[int64]bool{}
	for _, x := range p.Prepayments {
		name := fmt.Sprint(x.ID)
		if err := unique("prepayments", name, x.ID, ppIDs); err != nil {
			return "", snap, err
		}
		if !debts[x.DebtID] {
			return "", snap, &InvalidError{Section: "prepayments", Name: name, Field: "debt_id"}
		}
		if err := link("prepayments", name, "redirect_account_id", x.RedirectAccountID, accs); err != nil {
			return "", snap, err
		}
		start, end, err := spanOf("prepayments", name, x.span)
		if err != nil {
			return "", snap, err
		}
		d := domain.Prepayment{ID: x.ID, DebtID: x.DebtID, Amount: domain.FromEuros(x.Amount),
			Frequency: domain.PrepaymentFrequency(x.Frequency), Mode: domain.PrepaymentMode(x.Mode), FeeRate: x.FeeRate,
			Start: start, End: end, StartMS: x.StartMS, EndMS: x.EndMS, RedirectAccountID: x.RedirectAccountID}
		if err := invalid("prepayments", name, d.Validate()); err != nil {
			return "", snap, err
		}
		snap.Prepayments = append(snap.Prepayments, d)
	}

	// Assumptions: start from the defaults so a file written by an older
	// version (or edited by hand) gets sensible values for missing fields.
	x := p.Assumptions
	a := engine.DefaultAssumptions()
	a.Inflation, a.TaxRate = x.Inflation, x.TaxRate
	if x.TaxModel != "" {
		a.TaxModel = x.TaxModel
	}
	if x.TaxRegion != "" {
		a.TaxRegion = x.TaxRegion
	}
	for _, k := range domain.AccountKinds {
		if v, ok := x.Returns[string(k)]; ok {
			a.Returns[k] = v
		}
		if v, ok := x.Volatility[string(k)]; ok {
			a.Volatility[k] = v
		}
	}
	if x.MCRuns > 0 {
		a.MCRuns = x.MCRuns
	}
	if x.Withdrawal.Strategy != "" {
		a.Withdrawal = engine.WithdrawalSettings{Strategy: engine.WithdrawalStrategy(x.Withdrawal.Strategy),
			StartMS: x.Withdrawal.StartMS, Rate: x.Withdrawal.Rate, Floor: x.Withdrawal.Floor, Ceiling: x.Withdrawal.Ceiling}
	}
	if len(x.Order) > 0 {
		a.Order = nil
		for _, k := range x.Order {
			a.Order = append(a.Order, domain.AccountKind(k))
		}
	}
	a.SurplusAccountID, a.PensionAvailableMS = x.SurplusAccountID, x.PensionAvailableMS
	a.PrepaymentReserve = domain.FromEuros(x.PrepaymentReserve)
	a.FreezeIRPF = x.FreezeIRPF
	for _, l := range []struct {
		field string
		id    *int64
		seen  map[int64]bool
	}{
		{"surplus_account_id", a.SurplusAccountID, accs},
		{"withdrawal.start_milestone_id", a.Withdrawal.StartMS, mss},
		{"pension_available_milestone_id", a.PensionAvailableMS, mss},
	} {
		if err := link("assumptions", "", l.field, l.id, l.seen); err != nil {
			return "", snap, err
		}
	}
	if err := invalid("assumptions", "", a.Validate()); err != nil {
		return "", snap, err
	}
	snap.Assumptions = a
	return p.Name, snap, nil
}
