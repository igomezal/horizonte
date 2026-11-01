package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

const evPlanChanged = "plan-changed"

// planOpts are the view toggles, kept in the URL.
type planOpts struct {
	Monthly bool // monthly rows instead of yearly
	Real    bool // today's euros instead of nominal
}

func parsePlanOpts(q url.Values) planOpts {
	return planOpts{Monthly: q.Get("view") == "month", Real: q.Get("real") != "0"}
}

// Query renders the options as a query string. It is a template.URL so
// html/template does not re-escape "=" and "&" inside href / hx-push-url.
// Safe: it is built only from fixed keys and values.
func (o planOpts) Query() template.URL {
	v := url.Values{}
	if o.Monthly {
		v.Set("view", "month")
	} else {
		v.Set("view", "year")
	}
	if o.Real {
		v.Set("real", "1")
	} else {
		v.Set("real", "0")
	}
	return template.URL(v.Encode())
}

func (o planOpts) WithMonthly(m bool) planOpts { o.Monthly = m; return o }
func (o planOpts) WithReal(r bool) planOpts    { o.Real = r; return o }

// planRow is one line of the projection table, already in display units.
type planRow struct {
	Label                                                  string
	Ages                                                   string
	Income, Tax, Expenses, DebtPayments, Savings, NetWorth float64
	Short                                                  bool // money ran out in this period
}

// projectionChart is the JSON contract for the "projection" chart in charts.js.
type projectionChart struct {
	Type       string        `json:"type"`
	Categories []string      `json:"categories"`
	Ages       []string      `json:"ages"`
	AgeFormat  string        `json:"ageFormat"`
	Accounts   []chartSeries `json:"accounts"`
	Debt       *chartSeries  `json:"debt,omitempty"`
	NetWorth   chartSeries   `json:"netWorth"`
	FailIndex  int           `json:"failIndex"`
	FailLabel  string        `json:"failLabel,omitempty"`
	Marks      []chartMark   `json:"marks,omitempty"`
}

// chartMark is a vertical marker on the projection chart.
type chartMark struct {
	Index int    `json:"index"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // "milestone" or "debt"
}

// timelineItem is one entry of the milestone timeline under the verdict.
type timelineItem struct {
	Label   string
	Kind    string // "milestone", "debt" or "fail"
	Month   domain.Month
	Age     int
	Reached bool // false: a conditional milestone the plan never reaches
	Past    bool // happened before the plan starts
}

type planView struct {
	HasPeople bool
	Opts      planOpts
	OOB       bool // render the table as an out-of-band swap

	Failed     bool
	FailMonth  domain.Month
	FailAge    int
	EndAge     int
	EndYear    int
	EndValue   float64 // net worth at the end, display units
	Shortfall  float64 // total uncovered deficit, display units
	PeakValue  float64
	PeakAge    int
	PeakYear   int
	ShowPeak   bool
	Chart      projectionChart
	Rows       []planRow
	NoAccounts bool
	Timeline   []timelineItem
	// Withdrawal strategy outcome: spending as a share of the plan once it starts.
	WdActive     bool
	WdStrategy   string
	WdMin, WdMax float64
	WdMinYear    int
}

type assumptionsForm struct {
	V          map[string]string
	E          domain.Errors
	Kinds      []domain.AccountKind
	Accounts   []domain.Account // candidates for the surplus account
	Milestones []domain.Milestone
	Strategies []engine.WithdrawalStrategy
	Regions    []string
	// OrderLabels names the account kinds for the withdrawal-order list.
	OrderLabels map[string]string
	Saved       bool
}

type planPage struct {
	View planView
	Form assumptionsForm
}

func (s *Server) loadInput(ctx context.Context, fc finCtx) (engine.Input, error) {
	in := engine.Input{Start: fc.now}
	var err error
	if in.People, err = s.store.ListPeople(ctx, fc.plan); err != nil {
		return in, err
	}
	if in.Accounts, err = s.store.ListAccounts(ctx, fc.plan); err != nil {
		return in, err
	}
	if in.Incomes, err = s.store.ListIncomes(ctx, fc.plan); err != nil {
		return in, err
	}
	if in.Expenses, err = s.store.ListExpenses(ctx, fc.plan); err != nil {
		return in, err
	}
	if in.Debts, err = s.store.ListDebts(ctx, fc.plan); err != nil {
		return in, err
	}
	if in.Milestones, err = s.store.ListMilestones(ctx, fc.plan); err != nil {
		return in, err
	}
	if in.Prepayments, err = s.store.ListPrepayments(ctx, fc.plan); err != nil {
		return in, err
	}
	in.Assumptions, err = s.store.GetAssumptions(ctx, fc.plan)
	return in, err
}

// ages formats every person's age in month m: "36" or "36 / 34".
func ages(people []domain.Person, m domain.Month) string {
	parts := make([]string, len(people))
	for i, p := range people {
		parts[i] = strconv.Itoa(p.AgeAt(m))
	}
	return strings.Join(parts, " / ")
}

func (s *Server) buildPlanView(lang i18n.Lang, in engine.Input, opts planOpts) planView {
	v := planView{HasPeople: len(in.People) > 0, Opts: opts, NoAccounts: len(in.Accounts) == 0}
	if !v.HasPeople {
		return v
	}
	res := engine.Simulate(in)
	if len(res.Months) == 0 {
		return v
	}
	main := in.People[0]
	unit := func(nominal, deflator float64) float64 {
		if opts.Real {
			return nominal / deflator
		}
		return nominal
	}

	last := res.Months[len(res.Months)-1]
	v.EndAge = main.AgeAt(last.Month)
	v.EndYear = last.Month.Year
	v.EndValue = unit(last.NetWorth, last.Deflator)
	if peak, ok := res.Peak(); ok && peak.Month != last.Month {
		v.ShowPeak = true
		v.PeakValue = unit(peak.NetWorth, peak.Deflator)
		v.PeakAge = main.AgeAt(peak.Month)
		v.PeakYear = peak.Month.Year
	}
	if res.Failed() {
		v.Failed = true
		v.FailMonth = res.FailMonth
		v.FailAge = main.AgeAt(res.FailMonth)
		for _, m := range res.Months {
			v.Shortfall += unit(m.Shortfall, m.Deflator)
		}
	}

	if !res.WithdrawalFrom.IsZero() {
		v.WdActive, v.WdStrategy, v.WdMin, v.WdMax = true, string(in.Assumptions.Withdrawal.Strategy), math.Inf(1), math.Inf(-1)
		for _, m := range res.Months {
			if m.Month.Before(res.WithdrawalFrom) {
				continue
			}
			if m.SpendFactor < v.WdMin {
				v.WdMin, v.WdMinYear = m.SpendFactor, m.Month.Year
			}
			v.WdMax = math.Max(v.WdMax, m.SpendFactor)
		}
	}

	// Rows and chart points share the same periods.
	type period struct {
		label     string
		last      engine.MonthRow
		deflator  float64
		inc, tax  float64
		exp, debt float64
		short     float64
	}
	var periods []period
	if opts.Monthly {
		for _, m := range res.Months {
			periods = append(periods, period{
				label: i18n.FormatMonth(lang, m.Month.Year, m.Month.Month), last: m, deflator: m.Deflator,
				inc: m.Income, tax: m.Tax, exp: m.Expenses, debt: m.DebtPayments, short: m.Shortfall,
			})
		}
	} else {
		for _, y := range res.Years() {
			periods = append(periods, period{
				label: strconv.Itoa(y.Year), last: y.Last, deflator: y.Deflator,
				inc: y.Income, tax: y.Tax, exp: y.Expenses, debt: y.DebtPayments, short: y.Shortfall,
			})
		}
	}

	ch := projectionChart{
		Type:      "projection",
		AgeFormat: s.catalog.T(lang, "plan.chart.age"),
		NetWorth:  chartSeries{Name: s.catalog.T(lang, "plan.net_worth")},
		FailIndex: -1,
	}
	for _, a := range res.Accounts {
		ch.Accounts = append(ch.Accounts, chartSeries{Name: a.Name})
	}
	hasDebt := len(in.Debts) > 0
	if hasDebt {
		ch.Debt = &chartSeries{Name: s.catalog.T(lang, "plan.debts")}
	}
	for i, p := range periods {
		d := p.deflator
		if !opts.Real {
			d = 1
		}
		ch.Categories = append(ch.Categories, p.label)
		ch.Ages = append(ch.Ages, strconv.Itoa(main.AgeAt(p.last.Month)))
		for j, b := range p.last.Balances {
			ch.Accounts[j].Data = append(ch.Accounts[j].Data, round2(b/d))
		}
		if hasDebt {
			ch.Debt.Data = append(ch.Debt.Data, round2(-p.last.DebtBalance/d))
		}
		ch.NetWorth.Data = append(ch.NetWorth.Data, round2(p.last.NetWorth/d))
		if p.short > 0 && ch.FailIndex < 0 {
			ch.FailIndex = i
			ch.FailLabel = s.catalog.T(lang, "plan.chart.fail")
		}

		v.Rows = append(v.Rows, planRow{
			Label: p.label, Ages: ages(in.People, p.last.Month),
			Income: p.inc / d, Tax: p.tax / d, Expenses: p.exp / d, DebtPayments: p.debt / d,
			Savings: (p.inc - p.tax - p.exp - p.debt) / d, NetWorth: p.last.NetWorth / d,
			Short: p.short > 0,
		})
	}
	// Milestones and detected events: a timeline, and marks on the chart.
	periodOf := func(m domain.Month) int {
		for i, p := range periods {
			if (opts.Monthly && p.last.Month == m) || (!opts.Monthly && p.last.Month.Year == m.Year) {
				return i
			}
		}
		return -1
	}
	addMark := func(m domain.Month, label, kind string) {
		if i := periodOf(m); i >= 0 {
			ch.Marks = append(ch.Marks, chartMark{Index: i, Label: label, Kind: kind})
		}
	}
	var unreached []timelineItem
	for _, h := range res.Milestones {
		if !h.Reached() {
			unreached = append(unreached, timelineItem{Label: h.Milestone.Name, Kind: "milestone"})
			continue
		}
		item := timelineItem{Label: h.Milestone.Name, Kind: "milestone", Month: h.Month,
			Age: main.AgeAt(h.Month), Reached: true, Past: h.Month.Before(in.Start)}
		v.Timeline = append(v.Timeline, item)
		if !item.Past {
			addMark(h.Month, h.Milestone.Name, "milestone")
		}
	}
	for _, d := range res.PaidOff {
		label := s.catalog.T(lang, "plan.event.paid_off", d.Debt.Name)
		v.Timeline = append(v.Timeline, timelineItem{Label: label, Kind: "debt", Month: d.Month,
			Age: main.AgeAt(d.Month), Reached: true})
		addMark(d.Month, label, "debt")
	}
	if res.Failed() {
		v.Timeline = append(v.Timeline, timelineItem{Label: s.catalog.T(lang, "plan.chart.fail"), Kind: "fail",
			Month: res.FailMonth, Age: main.AgeAt(res.FailMonth), Reached: true})
	}
	sort.SliceStable(v.Timeline, func(i, j int) bool { return v.Timeline[i].Month.Before(v.Timeline[j].Month) })
	v.Timeline = append(v.Timeline, unreached...)

	v.Chart = ch
	return v
}

// ---------- assumptions form ----------

func (s *Server) assumptionsValues(lang i18n.Lang, a engine.Assumptions) map[string]string {
	v := map[string]string{
		"inflation": inPercent(lang, a.Inflation),
		"tax_rate":  inPercent(lang, a.TaxRate),
		"surplus":   inOwner(a.SurplusAccountID),
	}
	for _, k := range domain.AccountKinds {
		v["return_"+string(k)] = inPercent(lang, a.Returns[k])
	}
	w := a.Withdrawal
	if w.Strategy == "" {
		w = engine.DefaultWithdrawal()
	}
	v["wd_strategy"] = string(w.Strategy)
	v["wd_start"] = inOwner(w.StartMS)
	v["wd_rate"] = inPercent(lang, w.Rate)
	v["wd_floor"] = inPercent(lang, w.Floor)
	v["wd_ceiling"] = inPercent(lang, w.Ceiling)
	order := a.Order
	if engine.ParseOrder(engine.FormatOrder(order)) == nil {
		order = engine.DefaultOrder
	}
	v["wd_order"] = engine.FormatOrder(order)
	v["pension_from"] = inOwner(a.PensionAvailableMS)
	v["tax_model"], v["tax_region"] = a.TaxModel, a.TaxRegion
	if v["tax_model"] == "" {
		v["tax_model"] = engine.TaxES
	}
	if v["tax_region"] == "" {
		v["tax_region"] = "general"
	}
	return v
}

// newAssumptionsForm fills the choices the form needs.
func (s *Server) newAssumptionsForm(lang i18n.Lang, v map[string]string, e domain.Errors, accounts []domain.Account, ms []domain.Milestone) assumptionsForm {
	labels := map[string]string{}
	for _, k := range engine.DefaultOrder {
		labels[string(k)] = s.catalog.T(lang, "account.kind."+string(k))
	}
	return assumptionsForm{V: v, E: e, Kinds: domain.AccountKinds, Accounts: surplusCandidates(accounts),
		Milestones: ms, Strategies: engine.WithdrawalStrategies, OrderLabels: labels, Regions: engine.TaxRegions()}
}

func surplusCandidates(accounts []domain.Account) []domain.Account {
	var out []domain.Account
	for _, a := range accounts {
		if a.Kind != domain.AccountRealEstate {
			out = append(out, a)
		}
	}
	return out
}

func parseAssumptions(p *formParser) (engine.Assumptions, domain.Errors) {
	a := engine.Assumptions{
		Inflation:        p.percent("inflation"),
		TaxRate:          p.percent("tax_rate"),
		TaxModel:         p.str("tax_model"),
		TaxRegion:        p.str("tax_region"),
		Returns:          map[domain.AccountKind]float64{},
		SurplusAccountID: p.owner("surplus"),
	}
	for _, k := range domain.AccountKinds {
		a.Returns[k] = p.percent("return_" + string(k))
	}
	// The withdrawal fields are optional in the request: without them the
	// handler keeps the plan's current withdrawal settings.
	if _, ok := p.v["wd_strategy"]; ok {
		a.Withdrawal = engine.WithdrawalSettings{
			Strategy: engine.WithdrawalStrategy(p.str("wd_strategy")),
			StartMS:  p.owner("wd_start"),
			Rate:     p.percent("wd_rate"),
			Floor:    p.percent("wd_floor"),
			Ceiling:  p.percent("wd_ceiling"),
		}
		if a.Order = engine.ParseOrder(p.str("wd_order")); a.Order == nil {
			p.errs.Add("wd_order", domain.ErrChoice)
		}
		a.PensionAvailableMS = p.owner("pension_from")
	} else {
		a.Withdrawal, a.Order = engine.DefaultWithdrawal(), engine.DefaultOrder
	}
	// Validated with the defaults for the settings this form does not edit.
	check := a
	check.Volatility, check.MCRuns = engine.DefaultVolatility(), 1000
	p.merge(check.Validate())
	return a, p.errs
}

// ---------- handlers ----------

// GET /plan
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	in, err := s.loadInput(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := planPage{
		View: s.buildPlanView(fc.lang, in, parsePlanOpts(r.URL.Query())),
		Form: s.newAssumptionsForm(fc.lang, s.assumptionsValues(fc.lang, in.Assumptions), domain.Errors{},
			in.Accounts, in.Milestones),
	}
	s.page(w, r, http.StatusOK, "plan", "plan.title", page)
}

// GET /plan/results: the summary plus the table as an out-of-band swap.
func (s *Server) handlePlanResults(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	in, err := s.loadInput(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := s.buildPlanView(fc.lang, in, parsePlanOpts(r.URL.Query()))
	v.OOB = true
	if err := s.render.render(w, r, http.StatusOK, "plan", "plan-results", v); err != nil {
		s.serverError(w, r, err)
	}
}

// POST /plan/assumptions: saves and returns the form; the results refresh
// themselves on the plan-changed event.
func (s *Server) handleSaveAssumptions(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	accounts, err := s.store.ListAccounts(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	milestones, err := s.store.ListMilestones(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	a, errs := parseAssumptions(newFormParser(fc.lang, r.PostForm))
	if len(errs) == 0 {
		// The Plan form does not edit the Monte Carlo settings: keep them.
		current, err := s.store.GetAssumptions(r.Context(), fc.plan)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		a.Volatility, a.MCRuns = current.Volatility, current.MCRuns
		if _, ok := r.PostForm["wd_strategy"]; !ok {
			a.Withdrawal, a.Order, a.PensionAvailableMS = current.Withdrawal, current.Order, current.PensionAvailableMS
		}
		if _, ok := r.PostForm["tax_model"]; !ok {
			a.TaxModel, a.TaxRegion = current.TaxModel, current.TaxRegion
		}
		err = s.store.SaveAssumptions(r.Context(), fc.plan, a)
		if errors.Is(err, store.ErrNotFound) {
			errs.Add("_form", "form.error.stale")
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	form := s.newAssumptionsForm(fc.lang, nil, errs, accounts, milestones)
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusUnprocessableEntity
		form.V = formValuesFromRequest(r.PostForm)
	} else {
		form.V = s.assumptionsValues(fc.lang, a)
		form.Saved = true
		w.Header().Set("HX-Trigger", evPlanChanged)
	}
	if err := s.render.render(w, r, status, "plan", "assumptions-form", form); err != nil {
		s.serverError(w, r, fmt.Errorf("render assumptions: %w", err))
	}
}

// DOMID is the form's element id (also the prefix of its field ids).
func (assumptionsForm) DOMID() string { return "assumptions-form" }
