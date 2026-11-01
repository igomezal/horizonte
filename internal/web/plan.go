package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
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
}

type assumptionsForm struct {
	V        map[string]string
	E        domain.Errors
	Kinds    []domain.AccountKind
	Accounts []domain.Account // candidates for the surplus account
	Saved    bool
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
	return v
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
		Returns:          map[domain.AccountKind]float64{},
		SurplusAccountID: p.owner("surplus"),
	}
	for _, k := range domain.AccountKinds {
		a.Returns[k] = p.percent("return_" + string(k))
	}
	p.merge(a.Validate())
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
		Form: assumptionsForm{
			V: s.assumptionsValues(fc.lang, in.Assumptions), E: domain.Errors{},
			Kinds: domain.AccountKinds, Accounts: surplusCandidates(in.Accounts),
		},
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
	a, errs := parseAssumptions(newFormParser(fc.lang, r.PostForm))
	if len(errs) == 0 {
		err = s.store.SaveAssumptions(r.Context(), fc.plan, a)
		if errors.Is(err, store.ErrNotFound) {
			errs.Add("surplus", domain.ErrChoice)
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	form := assumptionsForm{E: errs, Kinds: domain.AccountKinds, Accounts: surplusCandidates(accounts)}
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
