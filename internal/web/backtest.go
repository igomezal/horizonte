package web

import (
	"context"
	"math"
	"net/http"
	"strconv"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/history"
	"github.com/igomezal/horizonte/internal/i18n"
)

// heatChart is the JSON contract for the "start-years" heat-map.
type heatChart struct {
	Type        string     `json:"type"`
	Decades     []string   `json:"decades"`
	Cells       []heatCell `json:"cells"`
	OK          string     `json:"ok"`      // tooltip format for a start that lasts ("%s" = amount)
	Fail        string     `json:"fail"`    // tooltip format when the money runs out ("%s" = age)
	PartialNote string     `json:"partial"` // tooltip line for partial starts ("%s" = years of data)
	URL         string     `json:"url"`     // clicking a year loads URL + year
}

type heatCell struct {
	Year    int     `json:"year"`
	Partial bool    `json:"partial,omitempty"`
	Years   int     `json:"years,omitempty"` // years of historical data (partial starts)
	Col     int     `json:"col"`             // year within its decade
	Row     int     `json:"row"`             // decade index
	Failed  bool    `json:"failed"`
	FailAge int     `json:"failAge,omitempty"`
	End     float64 `json:"end"`
}

// scenarioChart is the JSON contract for one replayed scenario.
type scenarioChart struct {
	Type       string    `json:"type"`
	Categories []string  `json:"categories"`
	Ages       []string  `json:"ages"`
	AgeFormat  string    `json:"ageFormat"`
	Scenario   []float64 `json:"scenario"`
	Plan       []float64 `json:"plan"`
	Labels     struct {
		Scenario string `json:"scenario"`
		Plan     string `json:"plan"`
		Fail     string `json:"fail"`
		DataEnd  string `json:"dataEnd"`
	} `json:"labels"`
	FailIndex    int `json:"failIndex"`
	DataEndIndex int `json:"dataEndIndex"`
}

type btCrisis struct {
	Key, Label, Desc string
	Start            domain.Month
	Failed           bool
	FailMonth        domain.Month
	FailAge          int
	MinReal, EndReal float64
}

type btScenario struct {
	Title        string
	Start        domain.Month
	Failed       bool
	FailMonth    domain.Month
	FailAge      int
	EndAge       int
	EndReal      float64
	MinReal      float64
	MinMonth     domain.Month
	HistoryYears int
	Partial      bool // history ran out before the end of the plan
	Chart        scenarioChart
	ID           string // for the chart element id
}

type btView struct {
	HasPeople        bool
	DataFrom, DataTo domain.Month
	EndAge           int
	Crises           []btCrisis
	Scenario         btScenario
	// Rolling holds what comes from replaying every start year: the slow
	// part, loaded separately (/backtest/summary, /backtest/heat) unless the
	// result is already cached.
	Rolling btRolling
}

// btRolling is the result of replaying the plan from every start year.
type btRolling struct {
	// Pending: not computed yet; the page shows placeholders that load it.
	Pending           bool
	DataFrom, DataTo  domain.Month
	Starts, Successes int // fully historical starts
	Rate              float64
	FirstYear         int
	LastYear          int
	// Partial starts (history runs out before the plan ends), counted apart.
	PartialStarts, PartialSuccesses int
	PartialFirst, PartialLast       int
	EndAge                          int
	Worst                           engine.RollingStart
	WorstFailAge                    int
	Heat                            heatChart
	// HistReal and PlanReal compare the historical real stock return with the
	// plan's expected real return for investments (shown when history is
	// clearly better, which explains why backtests beat the plan).
	HistReal, PlanReal float64
	ShowHistNote       bool
}

// realPath returns each month's net worth in today's euros.
func realPath(res engine.Result) map[domain.Month]float64 {
	out := make(map[domain.Month]float64, len(res.Months))
	for _, m := range res.Months {
		out[m.Month] = m.NetWorth / m.Deflator
	}
	return out
}

func (s *Server) buildScenario(lang i18n.Lang, in engine.Input, hs *history.Series, from domain.Month, title string) btScenario {
	res, covered := engine.Backtest(in, hs, from)
	main := in.People[0]
	sc := btScenario{Title: title, Start: from, HistoryYears: covered / 12, ID: from.String()}
	if len(res.Months) == 0 {
		return sc
	}
	last := res.Months[len(res.Months)-1]
	sc.EndAge, sc.EndReal = main.AgeAt(last.Month), last.NetWorth/last.Deflator
	sc.Partial = covered < len(res.Months)
	if res.Failed() {
		sc.Failed, sc.FailMonth, sc.FailAge = true, res.FailMonth, main.AgeAt(res.FailMonth)
	}
	sc.MinReal = math.Inf(1)
	for _, m := range res.Months {
		if v := m.NetWorth / m.Deflator; v < sc.MinReal {
			sc.MinReal, sc.MinMonth = v, m.Month
		}
	}

	plan := realPath(engine.Simulate(in))
	ch := scenarioChart{Type: "scenario", AgeFormat: s.catalog.T(lang, "plan.chart.age"), FailIndex: -1, DataEndIndex: -1}
	ch.Labels.Scenario = s.catalog.T(lang, "bt.scenario_line", title)
	ch.Labels.Plan = s.catalog.T(lang, "mc.plan_line")
	ch.Labels.Fail = s.catalog.T(lang, "plan.chart.fail")
	ch.Labels.DataEnd = s.catalog.T(lang, "bt.data_end")
	for _, y := range res.Years() {
		ch.Categories = append(ch.Categories, strconv.Itoa(y.Year))
		ch.Ages = append(ch.Ages, strconv.Itoa(main.AgeAt(y.Last.Month)))
		ch.Scenario = append(ch.Scenario, round0(y.Last.NetWorth/y.Last.Deflator))
		ch.Plan = append(ch.Plan, round0(plan[y.Last.Month]))
		if y.Shortfall > 0 && ch.FailIndex < 0 {
			ch.FailIndex = len(ch.Categories) - 1
		}
	}
	if sc.Partial {
		endMonth := in.Start.AddMonths(covered - 1)
		for i, c := range ch.Categories {
			if c == strconv.Itoa(endMonth.Year) {
				ch.DataEndIndex = i
			}
		}
	}
	sc.Chart = ch
	return sc
}

func (s *Server) crisisLabel(lang i18n.Lang, key string) string {
	return s.catalog.T(lang, "bt.crisis."+key)
}

func endAge(in engine.Input) int {
	if end := in.EndMonth(); !end.IsZero() && len(in.People) > 0 {
		return in.People[0].AgeAt(end)
	}
	return 0
}

// pendingRolling is the placeholder for the rolling backtest, or its result
// when already cached.
func (s *Server) pendingRolling(lang i18n.Lang, in engine.Input, hs *history.Series) btRolling {
	if all, ok := cachedSim[[]engine.RollingStart](s.sims, rollingKey(in)); ok {
		return s.rollingFrom(lang, in, hs, all)
	}
	return btRolling{Pending: true, DataFrom: hs.Start, DataTo: hs.End(), EndAge: endAge(in)}
}

// buildRolling replays the plan from every start year (or takes it from the
// cache). The error is ctx's when the person left before it finished.
func (s *Server) buildRolling(ctx context.Context, lang i18n.Lang, in engine.Input, hs *history.Series) (btRolling, error) {
	all, err := s.rolling(ctx, "backtest", in, hs)
	if err != nil {
		return btRolling{}, err
	}
	return s.rollingFrom(lang, in, hs, all), nil
}

func (s *Server) rollingFrom(lang i18n.Lang, in engine.Input, hs *history.Series, all []engine.RollingStart) btRolling {
	v := btRolling{DataFrom: hs.Start, DataTo: hs.End(), EndAge: endAge(in)}
	if len(in.People) == 0 {
		return v
	}
	main := in.People[0]
	var starts []engine.RollingStart // fully historical: the headline result
	for _, st := range all {
		if st.Partial {
			if v.PartialStarts == 0 {
				v.PartialFirst = st.Year
			}
			v.PartialStarts++
			v.PartialLast = st.Year
			if !st.Failed {
				v.PartialSuccesses++
			}
			continue
		}
		starts = append(starts, st)
	}
	v.Starts = len(starts)
	if len(starts) > 0 {
		v.FirstYear, v.LastYear = starts[0].Year, starts[len(starts)-1].Year
		worst := starts[0]
		for _, st := range starts {
			if !st.Failed {
				v.Successes++
			}
			// Earliest failure is worst; among successes, the lowest end value.
			switch {
			case st.Failed && (!worst.Failed || st.FailMonth.Before(worst.FailMonth)):
				worst = st
			case !st.Failed && !worst.Failed && st.EndReal < worst.EndReal:
				worst = st
			}
		}
		v.Rate = float64(v.Successes) / float64(len(starts))
		v.Worst = worst
		if worst.Failed {
			v.WorstFailAge = main.AgeAt(worst.FailMonth)
		}

	}
	if len(all) > 0 {
		v.Heat = heatChart{Type: "start-years", OK: s.catalog.T(lang, "bt.heat_ok"), Fail: s.catalog.T(lang, "bt.heat_fail"),
			PartialNote: s.catalog.T(lang, "bt.heat_partial"), URL: "/backtest/scenario?year="}
		firstDecade := all[0].Year / 10
		for _, st := range all {
			row := st.Year/10 - firstDecade
			for len(v.Heat.Decades) <= row {
				v.Heat.Decades = append(v.Heat.Decades, strconv.Itoa((firstDecade+len(v.Heat.Decades))*10)+"s")
			}
			cell := heatCell{Year: st.Year, Col: st.Year % 10, Row: row, Failed: st.Failed, End: round0(st.EndReal),
				Partial: st.Partial}
			if st.Partial {
				cell.Years = st.Covered / 12
			}
			if st.Failed {
				cell.FailAge = main.AgeAt(st.FailMonth)
			}
			v.Heat.Cells = append(v.Heat.Cells, cell)
		}
	}

	a := in.Assumptions
	v.HistReal = hs.RealStockReturn()
	v.PlanReal = (1+a.Returns[domain.AccountInvestment])/(1+a.Inflation) - 1
	v.ShowHistNote = v.HistReal-v.PlanReal > 0.01
	return v
}

// buildBacktestView builds the quick part of the page: the crises and the
// replayed scenario. The rolling backtest is a placeholder unless cached.
func (s *Server) buildBacktestView(lang i18n.Lang, in engine.Input, hs *history.Series, sel func() (domain.Month, string)) btView {
	v := btView{HasPeople: len(in.People) > 0, DataFrom: hs.Start, DataTo: hs.End()}
	if !v.HasPeople {
		return v
	}
	main := in.People[0]
	v.EndAge = endAge(in)
	v.Rolling = s.pendingRolling(lang, in, hs)

	for _, c := range engine.Crises {
		res, _ := engine.Backtest(in, hs, c.Start)
		row := btCrisis{Key: c.Key, Label: s.crisisLabel(lang, c.Key), Desc: s.catalog.T(lang, "bt.crisis_desc."+c.Key), Start: c.Start}
		sum := struct{ min float64 }{math.Inf(1)}
		for _, m := range res.Months {
			sum.min = math.Min(sum.min, m.NetWorth/m.Deflator)
		}
		row.MinReal = sum.min
		if len(res.Months) > 0 {
			last := res.Months[len(res.Months)-1]
			row.EndReal = last.NetWorth / last.Deflator
		}
		if res.Failed() {
			row.Failed, row.FailMonth, row.FailAge = true, res.FailMonth, main.AgeAt(res.FailMonth)
		}
		v.Crises = append(v.Crises, row)
	}

	from, title := sel()
	v.Scenario = s.buildScenario(lang, in, hs, from, title)
	return v
}

// scenarioFromQuery picks the replayed scenario: ?crisis=key or ?year=YYYY,
// defaulting to the 2007 financial crisis.
func (s *Server) scenarioFromQuery(r *http.Request, lang i18n.Lang, hs *history.Series) func() (domain.Month, string) {
	return func() (domain.Month, string) {
		if c, ok := engine.FindCrisis(r.URL.Query().Get("crisis")); ok {
			return c.Start, s.crisisLabel(lang, c.Key)
		}
		if y, err := strconv.Atoi(r.URL.Query().Get("year")); err == nil {
			m := domain.Month{Year: y, Month: 1}
			if hs.Index(m) >= 0 {
				return m, s.catalog.T(lang, "bt.start_year", y)
			}
		}
		c, _ := engine.FindCrisis("gfc")
		return c.Start, s.crisisLabel(lang, c.Key)
	}
}

// GET /backtest
func (s *Server) handleBacktest(w http.ResponseWriter, r *http.Request) {
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
	hs, err := history.Load()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := s.buildBacktestView(fc.lang, in, hs, s.scenarioFromQuery(r, fc.lang, hs))
	s.page(w, r, http.StatusOK, "backtest", "backtest.title", v)
}

// GET /backtest/summary and /backtest/heat: the rolling backtest's headline
// and heat map, loaded by the page's placeholders. Both share one
// calculation (simRunner), so loading them together costs one simulation.
func (s *Server) handleBacktestRolling(block string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		hs, err := history.Load()
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		v, err := s.buildRolling(r.Context(), fc.lang, in, hs)
		if err != nil {
			return // the person left; nobody is waiting for the response
		}
		if err := s.render.render(w, r, http.StatusOK, "backtest", block, v); err != nil {
			s.serverError(w, r, err)
		}
	}
}

// GET /backtest/scenario?crisis=key | ?year=YYYY
func (s *Server) handleBacktestScenario(w http.ResponseWriter, r *http.Request) {
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
	hs, err := history.Load()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	from, title := s.scenarioFromQuery(r, fc.lang, hs)()
	sc := s.buildScenario(fc.lang, in, hs, from, title)
	if err := s.render.render(w, r, http.StatusOK, "backtest", "bt-scenario", sc); err != nil {
		s.serverError(w, r, err)
	}
}
