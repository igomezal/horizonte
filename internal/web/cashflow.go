package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/i18n"
)

// sankeyNode is a node of the cash-flow diagram. Name is a stable id
// (ECharts needs unique names); Label is what the user sees.
type sankeyNode struct {
	Name  string  `json:"name"`
	Label string  `json:"label"`
	Kind  string  `json:"kind"` // income, savings, shortfall, center, tax, expense, debt, saving
	Value float64 `json:"value"`
}

type sankeyLink struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Value  float64 `json:"value"`
}

type sankeyChart struct {
	Type  string       `json:"type"`
	Nodes []sankeyNode `json:"nodes"`
	Links []sankeyLink `json:"links"`
}

// yearBarsChart is the JSON contract for "savings-rate" and "stacked-bars".
type yearBarsChart struct {
	Type       string         `json:"type"`
	Categories []string       `json:"categories"`
	Series     []kindedSeries `json:"series"`
	Percent    bool           `json:"percent,omitempty"`
	SelectYear bool           `json:"selectYear"` // clicking a bar selects that year
}

type kindedSeries struct {
	Name string     `json:"name"`
	Kind string     `json:"kind,omitempty"` // "debt" draws in red
	Data []*float64 `json:"data"`           // nil = no value (e.g. no income that year)
}

// cfLine is one row of the year's ledger.
type cfLine struct {
	Label string
	Kind  string
	Value float64
	Share float64 // of the side's total
}

type cashflowYear struct {
	Year      int
	Opts      planOpts
	Ages      string
	Partial   bool
	From, To  domain.Month
	HasIncome bool

	Income, Tax, NetIncome, Savings, Rate float64
	Withdrawals, Shortfall                float64
	In, Out                               []cfLine
	InTotal, OutTotal                     float64
	Sankey                                sankeyChart
	IRPF                                  *irpfView
}

// irpfView is the Spanish income tax of the selected year.
type irpfView struct {
	People     []irpfPerson
	Withheld   float64
	Settlement float64 // paid (refunded if negative) in June of NextYear
	NextYear   int
	Partial    bool
	PaidNow    float64 // last year's settlement, paid in June of this year
	HasPaidNow bool
}

type irpfPerson struct {
	Name                       string
	Gross, SocialSecurity      float64
	GeneralBase, SavingsBase   float64
	Minimum, GeneralTax        float64
	SavingsTax, Tax, Effective float64
}

type cashflowPage struct {
	HasPeople bool
	Opts      planOpts
	MinYear   int
	MaxYear   int
	Year      cashflowYear
	RateChart yearBarsChart
	CatChart  yearBarsChart
}

func round0(v float64) float64 { return float64(int64(v + 0.5*sign(v))) }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// cfYears runs the detailed projection and returns its yearly roll-up.
func cfYears(in engine.Input) (engine.Result, []engine.YearRow) {
	in.Detail = true
	res := engine.Simulate(in)
	return res, res.Years()
}

// pickYear clamps the requested year to the plan; by default it is the first
// full year (the current one usually has only a few months left).
func pickYear(q string, years []engine.YearRow) int {
	if y, err := strconv.Atoi(q); err == nil {
		if y < years[0].Year {
			return years[0].Year
		}
		if last := years[len(years)-1].Year; y > last {
			return last
		}
		return y
	}
	if len(years) > 1 && years[0].Months < 12 {
		return years[1].Year
	}
	return years[0].Year
}

func (s *Server) buildCashflowYear(lang i18n.Lang, in engine.Input, res engine.Result, y engine.YearRow, opts planOpts) cashflowYear {
	d := 1.0
	if opts.Real {
		d = y.Deflator
	}
	f := y.Flow
	cy := cashflowYear{
		Year: y.Year, Opts: opts, Partial: y.Months < 12,
		Income: y.Income / d, Tax: y.Tax / d, Withdrawals: y.Withdrawals / d, Shortfall: y.Shortfall / d,
		Savings: y.Savings() / d, HasIncome: y.Income > 0.5,
		To: y.Last.Month, From: y.Last.Month.AddMonths(1 - y.Months),
	}
	cy.NetIncome = cy.Income - cy.Tax
	if cy.NetIncome > 0.5 {
		cy.Rate = cy.Savings / cy.NetIncome
	}
	if len(in.People) > 0 {
		cy.Ages = ages(in.People, y.Last.Month)
	}

	ch := sankeyChart{Type: "sankey"}
	center := sankeyNode{Name: "center", Label: s.catalog.T(lang, "cf.center"), Kind: "center"}
	var rawIn, rawOut float64 // unrounded totals, so both sides match exactly
	add := func(node sankeyNode, inflow bool) {
		raw := node.Value / d
		node.Value = round0(raw)
		if node.Value < 1 {
			return
		}
		if inflow {
			rawIn += raw
		} else {
			rawOut += raw
		}
		ch.Nodes = append(ch.Nodes, node)
		line := cfLine{Label: node.Label, Kind: node.Kind, Value: node.Value}
		if inflow {
			ch.Links = append(ch.Links, sankeyLink{Source: node.Name, Target: center.Name, Value: node.Value})
			cy.In = append(cy.In, line)
			cy.InTotal += node.Value
		} else {
			ch.Links = append(ch.Links, sankeyLink{Source: center.Name, Target: node.Name, Value: node.Value})
			cy.Out = append(cy.Out, line)
			cy.OutTotal += node.Value
		}
	}

	// Inflows: income streams, money taken from savings, and any gap.
	for k, inc := range in.Incomes {
		add(sankeyNode{Name: fmt.Sprintf("inc%d", k), Label: inc.Name, Kind: "income", Value: f.Income[k]}, true)
	}
	for j, acc := range in.Accounts {
		add(sankeyNode{Name: fmt.Sprintf("wd%d", j), Label: s.catalog.T(lang, "cf.from", acc.Name),
			Kind: "savings", Value: f.Withdrawals[j]}, true)
	}
	add(sankeyNode{Name: "gap", Label: s.catalog.T(lang, "cf.uncovered"), Kind: "shortfall", Value: y.Shortfall}, true)
	if y.Tax < 0 {
		add(sankeyNode{Name: "refund", Label: s.catalog.T(lang, "cf.tax_refund"), Kind: "income", Value: -y.Tax}, true)
	}
	ch.Nodes = append(ch.Nodes, center)

	// Outflows: tax, expenses by category, debts, money going into accounts.
	taxLabel := "cf.tax"
	if len(res.TaxYears) > 0 {
		taxLabel = "cf.tax_ss"
	}
	add(sankeyNode{Name: "tax", Label: s.catalog.T(lang, taxLabel), Kind: "tax", Value: y.Tax}, false)
	byCat := map[domain.ExpenseCategory]float64{}
	for k, e := range in.Expenses {
		byCat[e.Category] += f.Expenses[k]
	}
	for _, c := range domain.ExpenseCategories {
		add(sankeyNode{Name: "cat-" + string(c), Label: s.catalog.T(lang, "expense.category."+string(c)),
			Kind: "expense", Value: byCat[c]}, false)
	}
	for i, debt := range in.Debts {
		add(sankeyNode{Name: fmt.Sprintf("debt%d", i), Label: debt.Name, Kind: "debt", Value: f.Debts[i]}, false)
	}
	for j, acc := range in.Accounts {
		v := f.Contributions[j]
		if j == res.SurplusAccount {
			v += y.SurplusSaved
		}
		add(sankeyNode{Name: fmt.Sprintf("acc%d", j), Label: s.catalog.T(lang, "cf.to", acc.Name), Kind: "saving", Value: v}, false)
	}
	if res.SurplusAccount < 0 {
		add(sankeyNode{Name: "spent", Label: s.catalog.T(lang, "cf.no_account"), Kind: "expense", Value: y.SurplusSaved}, false)
	}

	cy.InTotal, cy.OutTotal = round0(rawIn), round0(rawOut)
	for i := range cy.In {
		cy.In[i].Share = cy.In[i].Value / cy.InTotal
	}
	for i := range cy.Out {
		cy.Out[i].Share = cy.Out[i].Value / cy.OutTotal
	}
	sort.SliceStable(cy.Out, func(i, j int) bool { return cy.Out[i].Value > cy.Out[j].Value })
	sort.SliceStable(cy.In, func(i, j int) bool { return cy.In[i].Value > cy.In[j].Value })
	cy.Sankey = ch
	cy.IRPF = buildIRPF(res.TaxYears, y.Year, d)
	return cy
}

// buildIRPF summarises the year's IRPF (nil when the plan uses a flat rate).
func buildIRPF(years []engine.TaxYear, year int, d float64) *irpfView {
	var v *irpfView
	for _, ty := range years {
		if ty.Year != year {
			continue
		}
		v = &irpfView{Withheld: ty.Withheld / d, Settlement: ty.Settlement / d, NextYear: year + 1, Partial: ty.Partial}
		for _, p := range ty.People {
			in, b := p.Income, p.Breakdown
			gross := in.Salary + in.Pension + in.PlanWithdrawal + in.SelfEmployed + in.Rental + in.Other + in.Savings
			ip := irpfPerson{Name: p.Name, Gross: gross / d, SocialSecurity: b.SocialSecurity / d,
				GeneralBase: b.GeneralBase / d, SavingsBase: b.SavingsBase / d, Minimum: b.Minimum / d,
				GeneralTax: b.GeneralTax / d, SavingsTax: b.SavingsTax / d, Tax: b.Tax / d}
			if gross > 0.5 {
				ip.Effective = b.Tax / gross
			}
			v.People = append(v.People, ip)
		}
	}
	if v != nil {
		for _, ty := range years {
			if ty.Year == year-1 && !ty.Partial {
				v.PaidNow, v.HasPaidNow = ty.Settlement/d, true
			}
		}
	}
	return v
}

func (s *Server) buildCashflowCharts(lang i18n.Lang, in engine.Input, years []engine.YearRow, opts planOpts) (yearBarsChart, yearBarsChart) {
	rate := yearBarsChart{Type: "savings-rate", Percent: true, SelectYear: true}
	rateSeries := kindedSeries{Name: s.catalog.T(lang, "cf.rate")}
	cat := yearBarsChart{Type: "stacked-bars", SelectYear: true}

	present := map[domain.ExpenseCategory]bool{}
	for _, e := range in.Expenses {
		present[e.Category] = true
	}
	var cats []domain.ExpenseCategory
	for _, c := range domain.ExpenseCategories {
		if present[c] {
			cats = append(cats, c)
			cat.Series = append(cat.Series, kindedSeries{Name: s.catalog.T(lang, "expense.category."+string(c))})
		}
	}
	debtIdx := -1
	if len(in.Debts) > 0 {
		debtIdx = len(cat.Series)
		cat.Series = append(cat.Series, kindedSeries{Name: s.catalog.T(lang, "cf.debts"), Kind: "debt"})
	}

	for _, y := range years {
		d := 1.0
		if opts.Real {
			d = y.Deflator
		}
		label := strconv.Itoa(y.Year)
		rate.Categories = append(rate.Categories, label)
		cat.Categories = append(cat.Categories, label)
		if net := y.Income - y.Tax; net > 0.5 {
			r := round4(y.Savings() / net)
			rateSeries.Data = append(rateSeries.Data, &r)
		} else {
			rateSeries.Data = append(rateSeries.Data, nil)
		}
		byCat := map[domain.ExpenseCategory]float64{}
		for k, e := range in.Expenses {
			byCat[e.Category] += y.Flow.Expenses[k]
		}
		for i, c := range cats {
			v := round0(byCat[c] / d)
			cat.Series[i].Data = append(cat.Series[i].Data, &v)
		}
		if debtIdx >= 0 {
			v := round0(y.DebtPayments / d)
			cat.Series[debtIdx].Data = append(cat.Series[debtIdx].Data, &v)
		}
	}
	rate.Series = []kindedSeries{rateSeries}
	return rate, cat
}

func round4(v float64) float64 { return float64(int64(v*10000+0.5*sign(v))) / 10000 }

// cashflowURL is the shareable address of a year and unit choice.
func cashflowURL(year int, opts planOpts) string {
	v := url.Values{}
	v.Set("y", strconv.Itoa(year))
	if opts.Real {
		v.Set("real", "1")
	} else {
		v.Set("real", "0")
	}
	return "/cashflow?" + v.Encode()
}

// GET /cashflow
func (s *Server) handleCashflow(w http.ResponseWriter, r *http.Request) {
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
	opts := parsePlanOpts(r.URL.Query())
	page := cashflowPage{HasPeople: len(in.People) > 0, Opts: opts}
	if page.HasPeople {
		res, years := cfYears(in)
		if len(years) > 0 {
			page.MinYear, page.MaxYear = years[0].Year, years[len(years)-1].Year
			sel := pickYear(r.URL.Query().Get("y"), years)
			for _, y := range years {
				if y.Year == sel {
					page.Year = s.buildCashflowYear(fc.lang, in, res, y, opts)
				}
			}
			page.RateChart, page.CatChart = s.buildCashflowCharts(fc.lang, in, years, opts)
		}
	}
	s.page(w, r, http.StatusOK, "cashflow", "cashflow.title", page)
}

// GET /cashflow/year: the selected year's summary, diagram and ledger.
func (s *Server) handleCashflowYear(w http.ResponseWriter, r *http.Request) {
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
	if len(in.People) == 0 {
		s.handleNotFound(w, r)
		return
	}
	res, years := cfYears(in)
	if len(years) == 0 {
		s.handleNotFound(w, r)
		return
	}
	opts := parsePlanOpts(r.URL.Query())
	sel := pickYear(r.URL.Query().Get("y"), years)
	for _, y := range years {
		if y.Year == sel {
			w.Header().Set("HX-Push-Url", cashflowURL(sel, opts))
			cy := s.buildCashflowYear(fc.lang, in, res, y, opts)
			if err := s.render.render(w, r, http.StatusOK, "cashflow", "cf-year", cy); err != nil {
				s.serverError(w, r, err)
			}
			return
		}
	}
}
