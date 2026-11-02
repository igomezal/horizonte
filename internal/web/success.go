package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

const evSuccessChanged = "success-changed"

// mcCorrelation ties every account to a common market factor.
const mcCorrelation = 0.6

// defaultSeed makes results reproducible until the user asks for new scenarios.
const defaultSeed = 1

// fanChart is the JSON contract for the "fan" chart in charts.js.
type fanChart struct {
	Type       string       `json:"type"`
	Categories []string     `json:"categories"`
	Ages       []string     `json:"ages"`
	AgeFormat  string       `json:"ageFormat"`
	Bands      [5][]float64 `json:"bands"` // p10, p25, p50, p75, p90
	Plan       []float64    `json:"plan"`  // deterministic projection
	Labels     fanLabels    `json:"labels"`
}

type fanLabels struct {
	Median string `json:"median"`
	Wide   string `json:"wide"`
	Narrow string `json:"narrow"`
	Plan   string `json:"plan"`
}

// survivalChart is the JSON contract for the "survival" chart.
type survivalChart struct {
	Type       string    `json:"type"`
	Categories []string  `json:"categories"`
	Ages       []string  `json:"ages"`
	AgeFormat  string    `json:"ageFormat"`
	Data       []float64 `json:"data"`
	Label      string    `json:"label"`
}

type mcMilestoneRow struct {
	Name                string
	Share               float64
	Reached             bool
	Best, Median, Worst domain.Month
	BestAge, MedianAge  int
	WorstAge            int
}

type successView struct {
	HasPeople bool
	// Pending: the simulation isn't cached yet, so the page shows a
	// placeholder that loads the results from /success/results.
	Pending   bool
	Seed      uint64
	Runs      int
	Rate      float64
	EndAge    int
	BadFailed bool
	BadMonth  domain.Month
	BadAge    int
	BadEnd    float64
	Fan       fanChart
	Survival  survivalChart
	// AlwaysLasts: every scenario has money every year, so the survival
	// chart would be a flat 100 % line; the page says so in words instead.
	AlwaysLasts bool
	Milestones  []mcMilestoneRow
}

type mcSettingsForm struct {
	V     map[string]string
	E     domain.Errors
	Kinds []domain.AccountKind
	Runs  []int
	Saved bool
}

// DOMID is the form's element id (also the prefix of its field ids).
func (mcSettingsForm) DOMID() string { return "mc-settings" }

type successPage struct {
	View successView
	Form mcSettingsForm
}

func mcSettings(in engine.Input, seed uint64) engine.MCSettings {
	return engine.MCSettings{Runs: in.Assumptions.MCRuns, Seed: seed, Correlation: mcCorrelation}
}

// pendingSuccessView is the placeholder shown while the simulation runs, or
// the full view when its result is already cached.
func (s *Server) pendingSuccessView(ctx context.Context, lang i18n.Lang, in engine.Input, seed uint64) successView {
	if len(in.People) > 0 {
		if mc, ok := cachedSim[engine.MCResult](s.sims, mcKey(in, mcSettings(in, seed))); ok {
			countSimCache(ctx, "monte_carlo", "success", servedPage, true)
			return s.successViewFrom(lang, in, seed, mc)
		}
	}
	// What is known before simulating, so the placeholder has the same
	// layout as the results.
	v := successView{HasPeople: len(in.People) > 0, Pending: true, Seed: seed, Runs: in.Assumptions.MCRuns, EndAge: endAge(in)}
	for _, m := range in.Milestones {
		if m.Conditional() {
			v.Milestones = append(v.Milestones, mcMilestoneRow{Name: m.Name})
		}
	}
	return v
}

// buildSuccessView runs the simulation (or takes it from the cache). The
// error is ctx's when the person left before it finished.
func (s *Server) buildSuccessView(ctx context.Context, lang i18n.Lang, in engine.Input, seed uint64) (successView, error) {
	if len(in.People) == 0 {
		return successView{Seed: seed}, nil
	}
	mc, err := s.monteCarlo(ctx, "success", in, mcSettings(in, seed))
	if err != nil {
		return successView{}, err
	}
	return s.successViewFrom(lang, in, seed, mc), nil
}

func (s *Server) successViewFrom(lang i18n.Lang, in engine.Input, seed uint64, mc engine.MCResult) successView {
	v := successView{HasPeople: len(in.People) > 0, Seed: seed}
	if !v.HasPeople || mc.Runs == 0 {
		return v
	}
	main := in.People[0]
	v.Runs, v.Rate = mc.Runs, mc.SuccessRate()
	v.EndAge = main.AgeAt(mc.YearEnd[len(mc.YearEnd)-1])
	v.BadFailed, v.BadMonth, v.BadEnd = mc.Bad.Failed, mc.Bad.FailMonth, mc.Bad.EndReal
	if mc.Bad.Failed {
		v.BadAge = main.AgeAt(mc.Bad.FailMonth)
	}

	// The deterministic projection, for comparison.
	det := engine.Simulate(in)
	detByMonth := make(map[domain.Month]float64, len(det.Months))
	for _, m := range det.Months {
		detByMonth[m.Month] = m.NetWorth / m.Deflator
	}

	ageFormat := s.catalog.T(lang, "plan.chart.age")
	v.Fan = fanChart{Type: "fan", AgeFormat: ageFormat, Labels: fanLabels{
		Median: s.catalog.T(lang, "mc.median"), Wide: s.catalog.T(lang, "mc.band80"),
		Narrow: s.catalog.T(lang, "mc.band50"), Plan: s.catalog.T(lang, "mc.plan_line"),
	}}
	v.Survival = survivalChart{Type: "survival", AgeFormat: ageFormat, Label: s.catalog.T(lang, "mc.survival_series")}
	for y, year := range mc.Years {
		label, age := strconv.Itoa(year), strconv.Itoa(main.AgeAt(mc.YearEnd[y]))
		v.Fan.Categories = append(v.Fan.Categories, label)
		v.Fan.Ages = append(v.Fan.Ages, age)
		for k := range 5 {
			v.Fan.Bands[k] = append(v.Fan.Bands[k], round0(mc.NetWorth[y][k]))
		}
		v.Fan.Plan = append(v.Fan.Plan, round0(detByMonth[mc.YearEnd[y]]))
		v.Survival.Categories = append(v.Survival.Categories, label)
		v.Survival.Ages = append(v.Survival.Ages, age)
		v.Survival.Data = append(v.Survival.Data, round4(mc.Survival[y]))
	}
	v.AlwaysLasts = len(v.Survival.Data) > 0
	for _, x := range v.Survival.Data {
		v.AlwaysLasts = v.AlwaysLasts && x >= 1
	}

	for _, m := range mc.Milestone {
		row := mcMilestoneRow{Name: m.Milestone.Name, Share: float64(m.Reached) / float64(mc.Runs), Reached: m.Reached > 0}
		if row.Reached {
			row.Best, row.Median, row.Worst = m.Best, m.Median, m.Worst
			row.BestAge, row.MedianAge, row.WorstAge = main.AgeAt(m.Best), main.AgeAt(m.Median), main.AgeAt(m.Worst)
		}
		v.Milestones = append(v.Milestones, row)
	}
	return v
}

func (s *Server) mcSettingsValues(lang i18n.Lang, a engine.Assumptions) map[string]string {
	v := map[string]string{"mc_runs": strconv.Itoa(a.MCRuns)}
	for _, k := range domain.AccountKinds {
		v["vol_"+string(k)] = inPercent(lang, a.Volatility[k])
	}
	return v
}

func newMCForm(v map[string]string, e domain.Errors) mcSettingsForm {
	return mcSettingsForm{V: v, E: e, Kinds: domain.AccountKinds, Runs: engine.MCRunsOptions}
}

func parseSeed(r *http.Request) uint64 {
	if r.URL.Query().Get("reseed") != "" {
		return uint64(time.Now().UnixNano())
	}
	if seed, err := strconv.ParseUint(r.URL.Query().Get("seed"), 10, 64); err == nil && seed > 0 {
		return seed
	}
	return defaultSeed
}

// GET /success
func (s *Server) handleSuccess(w http.ResponseWriter, r *http.Request) {
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
	page := successPage{
		View: s.pendingSuccessView(r.Context(), fc.lang, in, parseSeed(r)),
		Form: newMCForm(s.mcSettingsValues(fc.lang, in.Assumptions), domain.Errors{}),
	}
	s.page(w, r, http.StatusOK, "success", "success.title", page)
}

// GET /success/results
func (s *Server) handleSuccessResults(w http.ResponseWriter, r *http.Request) {
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
	v, err := s.buildSuccessView(r.Context(), fc.lang, in, parseSeed(r))
	if err != nil {
		return // the person left; nobody is waiting for the response
	}
	if err := s.render.render(w, r, http.StatusOK, "success", "mc-results", v); err != nil {
		s.serverError(w, r, err)
	}
}

// POST /success/settings: volatility per account kind and number of scenarios.
func (s *Server) handleSaveMCSettings(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	a, err := s.store.GetAssumptions(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	p := newFormParser(fc.lang, r.PostForm)
	a.Volatility = map[domain.AccountKind]float64{}
	for _, k := range domain.AccountKinds {
		a.Volatility[k] = p.percent("vol_" + string(k))
	}
	a.MCRuns, _ = strconv.Atoi(p.str("mc_runs"))
	p.merge(a.Validate())
	errs := p.errs
	if len(errs) == 0 {
		if err := s.store.SaveAssumptions(r.Context(), fc.plan, a); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.serverError(w, r, err)
			return
		}
	}
	form := newMCForm(nil, errs)
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusUnprocessableEntity
		form.V = formValuesFromRequest(r.PostForm)
	} else {
		form.V = s.mcSettingsValues(fc.lang, a)
		form.Saved = true
		w.Header().Set("HX-Trigger", evSuccessChanged)
	}
	if err := s.render.render(w, r, status, "success", "mc-settings", form); err != nil {
		s.serverError(w, r, err)
	}
}
