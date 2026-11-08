package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// How far past today the chart follows the baseline. Its path runs to the end
// of the plan, and decades of it would flatten the months actually recorded.
const trackingChartMonthsAhead = 12

// trackingChart is the JSON contract for the "tracking" chart in charts.js.
// Actual and Baseline are aligned with Categories; null where there is no value.
type trackingChart struct {
	Type       string     `json:"type"`
	Categories []string   `json:"categories"`
	Actual     []*float64 `json:"actual"`
	Baseline   []*float64 `json:"baseline"`
	Labels     struct {
		Actual   string `json:"actual"`
		Baseline string `json:"baseline"`
		Diff     string `json:"diff"`
	} `json:"labels"`
}

// trackingView is the part of the page that every change re-renders.
type trackingView struct {
	Now             domain.Month
	HasThisMonth    bool
	Rows            []domain.NetWorthComparison // newest first
	Latest          *domain.NetWorthComparison  // newest month the baseline covers
	Baseline        *domain.NetWorthBaseline
	BaselineSavedOn string
	CanSaveBaseline bool   // the active plan has a projection to save
	BaselineError   string // i18n key
	Chart           *trackingChart
}

// trackingForm feeds the "field" template, like formView.
type trackingForm struct {
	Edit      domain.Month // the month being edited; zero for a new entry
	Prefilled bool         // balances come from the active plan
	V         map[string]string
	E         domain.Errors
	Kinds     []domain.AccountKind
}

// DOMID is the form element's id, used to re-target validation errors.
func (trackingForm) DOMID() string { return "tracking-entry-form" }

func (s *Server) loadTracking(ctx context.Context, fc finCtx) (trackingView, error) {
	v := trackingView{Now: fc.now}
	entries, err := s.store.ListNetWorthEntries(ctx)
	if err != nil {
		return v, err
	}
	if v.Baseline, err = s.store.GetNetWorthBaseline(ctx); err != nil {
		return v, err
	}
	people, err := s.store.ListPeople(ctx, fc.plan)
	if err != nil {
		return v, err
	}
	v.CanSaveBaseline = len(people) > 0
	if v.Baseline != nil {
		d := v.Baseline.SavedOn
		v.BaselineSavedOn = strconv.Itoa(d.Day()) + " " + i18n.FormatMonth(fc.lang, d.Year(), int(d.Month()))
	}
	v.Rows = domain.CompareNetWorth(entries, v.Baseline)
	slices.Reverse(v.Rows)
	for i := range v.Rows {
		if v.Rows[i].Month == fc.now {
			v.HasThisMonth = true
		}
		if v.Latest == nil && v.Rows[i].HasBaseline {
			v.Latest = &v.Rows[i]
		}
	}
	v.Chart = s.buildTrackingChart(fc, entries, v.Baseline)
	return v, nil
}

// buildTrackingChart returns nil when there is nothing to draw.
func (s *Server) buildTrackingChart(fc finCtx, entries []domain.NetWorthEntry, b *domain.NetWorthBaseline) *trackingChart {
	if b != nil && len(b.Points) == 0 {
		b = nil
	}
	if len(entries) == 0 && b == nil {
		return nil
	}
	actual, planned := map[domain.Month]float64{}, map[domain.Month]float64{}
	var first, last domain.Month
	if len(entries) > 0 {
		first, last = entries[0].Month, entries[len(entries)-1].Month
		for _, e := range entries {
			actual[e.Month] = e.NetWorth().Euros()
		}
	}
	if b != nil {
		if first.IsZero() || b.Points[0].Month.Before(first) {
			first = b.Points[0].Month
		}
		end := fc.now.AddMonths(trackingChartMonthsAhead)
		if pathEnd := b.Points[len(b.Points)-1].Month; pathEnd.Before(end) {
			end = pathEnd
		}
		if last.IsZero() || last.Before(end) {
			last = end
		}
		for _, p := range b.Points {
			planned[p.Month] = p.NetWorth.Euros()
		}
	}

	c := &trackingChart{Type: "tracking"}
	c.Labels.Actual = s.catalog.T(fc.lang, "tracking.chart.actual")
	c.Labels.Baseline = s.catalog.T(fc.lang, "tracking.chart.baseline")
	c.Labels.Diff = s.catalog.T(fc.lang, "tracking.chart.diff")
	valueAt := func(m map[domain.Month]float64, k domain.Month) *float64 {
		if v, ok := m[k]; ok {
			return &v
		}
		return nil
	}
	for m := first; !last.Before(m); m = m.AddMonths(1) {
		c.Categories = append(c.Categories, i18n.FormatMonth(fc.lang, m.Year, m.Month))
		c.Actual = append(c.Actual, valueAt(actual, m))
		c.Baseline = append(c.Baseline, valueAt(planned, m))
	}
	return c
}

func (s *Server) renderTracking(w http.ResponseWriter, r *http.Request, status int, block string, data any) {
	if err := s.render.render(w, r, status, "tracking", block, data); err != nil {
		s.serverError(w, r, err)
	}
}

// renderTrackingBody re-renders the page after a change. errorKey, if any, is
// shown in the baseline section.
func (s *Server) renderTrackingBody(w http.ResponseWriter, r *http.Request, fc finCtx, status int, errorKey string) {
	v, err := s.loadTracking(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.BaselineError = errorKey
	s.renderTracking(w, r, status, "tracking-body", v)
}

func (s *Server) renderTrackingForm(w http.ResponseWriter, r *http.Request, status int, f trackingForm) {
	f.Kinds = domain.AccountKinds
	if status == http.StatusUnprocessableEntity {
		// The form posts with the page body as target; errors replace the form instead.
		w.Header().Set("HX-Retarget", "#"+f.DOMID())
		w.Header().Set("HX-Reswap", "outerHTML")
	}
	s.renderTracking(w, r, status, "tracking-form", f)
}

// GET /tracking
func (s *Server) handleTracking(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v, err := s.loadTracking(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "tracking", "tracking.title", v)
}

// planBalances adds up the active plan's current balances by account kind and
// its debts. Only amounts that aren't zero are filled in.
func (s *Server) planBalances(ctx context.Context, fc finCtx) (map[string]string, error) {
	accounts, err := s.store.ListAccounts(ctx, fc.plan)
	if err != nil {
		return nil, err
	}
	debts, err := s.store.ListDebts(ctx, fc.plan)
	if err != nil {
		return nil, err
	}
	e := domain.NetWorthEntry{Month: fc.now, Balances: map[domain.AccountKind]domain.Cents{}}
	for _, a := range accounts {
		e.Balances[a.Kind] += a.Balance
	}
	for _, d := range debts {
		e.Debt += d.Balance
	}
	return entryValues(fc.lang, e), nil
}

// entryValues formats an entry for the form.
func entryValues(lang i18n.Lang, e domain.NetWorthEntry) map[string]string {
	v := map[string]string{"month": e.Month.String()}
	for k, c := range e.Balances {
		if c != 0 {
			v[domain.BalanceField(k)] = inMoney(lang, c)
		}
	}
	if e.Debt != 0 {
		v["debt"] = inMoney(lang, e.Debt)
	}
	return v
}

// GET /tracking/entries/new: blank for a past month, or with ?prefill=1 this
// month with the active plan's current balances.
func (s *Server) handleTrackingNew(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f := trackingForm{V: map[string]string{}, E: domain.Errors{}}
	if r.URL.Query().Get("prefill") == "1" {
		if f.V, err = s.planBalances(r.Context(), fc); err != nil {
			s.serverError(w, r, err)
			return
		}
		f.Prefilled = true
	}
	s.renderTrackingForm(w, r, http.StatusOK, f)
}

// pathMonth reads the {month} of an entry's URL.
func pathMonth(r *http.Request) (domain.Month, bool) {
	m, err := domain.ParseMonth(r.PathValue("month"))
	return m, err == nil && !m.IsZero()
}

// GET /tracking/entries/{month}/edit
func (s *Server) handleTrackingEdit(w http.ResponseWriter, r *http.Request) {
	m, ok := pathMonth(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	e, err := s.store.GetNetWorthEntry(r.Context(), m)
	if s.notFoundOrError(w, r, err) {
		return
	}
	s.renderTrackingForm(w, r, http.StatusOK, trackingForm{Edit: m, V: entryValues(fc.lang, e), E: domain.Errors{}})
}

// parseTrackingEntry reads the amounts of the form; the month comes from the
// caller. A form with every amount blank is refused: zero has to be typed.
func parseTrackingEntry(p *formParser, m, now domain.Month) (domain.NetWorthEntry, domain.Errors) {
	e := domain.NetWorthEntry{Month: m, Balances: map[domain.AccountKind]domain.Cents{}}
	blank := p.str("debt") == ""
	for _, k := range domain.AccountKinds {
		name := domain.BalanceField(k)
		if p.str(name) != "" {
			blank = false
		}
		if c := p.money(name, false); c != 0 {
			e.Balances[k] = c
		}
	}
	e.Debt = p.money("debt", false)
	if blank {
		p.errs.Add("_form", "tracking.error.no_amounts")
	}
	p.merge(e.Validate(now))
	return e, p.errs
}

// POST /tracking/entries and PUT /tracking/entries/{month}
func (s *Server) handleTrackingSave(w http.ResponseWriter, r *http.Request) {
	var edit domain.Month
	if r.Method == http.MethodPut {
		var ok bool
		if edit, ok = pathMonth(r); !ok {
			s.handleNotFound(w, r)
			return
		}
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	p := newFormParser(fc.lang, r.PostForm)
	m := edit
	if m.IsZero() {
		m = p.month("month")
	}
	e, errs := parseTrackingEntry(p, m, fc.now)
	if len(errs) == 0 {
		if edit.IsZero() {
			err = s.store.AddNetWorthEntry(r.Context(), e)
		} else {
			err = s.store.UpdateNetWorthEntry(r.Context(), e)
		}
		if errors.Is(err, store.ErrMonthExists) {
			errs.Add("month", "tracking.error.month_exists")
		} else if s.notFoundOrError(w, r, err) { // deleted in another tab
			return
		}
	}
	if len(errs) > 0 {
		s.renderTrackingForm(w, r, http.StatusUnprocessableEntity,
			trackingForm{Edit: edit, V: formValuesFromRequest(r.PostForm), E: errs})
		return
	}
	s.renderTrackingBody(w, r, fc, http.StatusOK, "")
}

// DELETE /tracking/entries/{month}
func (s *Server) handleTrackingDelete(w http.ResponseWriter, r *http.Request) {
	m, ok := pathMonth(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Deleting something already gone is fine: the page re-renders without it.
	if err := s.store.DeleteNetWorthEntry(r.Context(), m); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.renderTrackingBody(w, r, fc, http.StatusOK, "")
}

// baselineFromProjection takes the nominal net worth path of a projection.
func baselineFromProjection(planName string, res engine.Result, today time.Time) domain.NetWorthBaseline {
	b := domain.NetWorthBaseline{PlanName: planName, SavedOn: today}
	for _, row := range res.Months {
		b.Points = append(b.Points, domain.NetWorthPoint{Month: row.Month, NetWorth: domain.FromEuros(row.NetWorth)})
	}
	return b
}

// POST /tracking/baseline saves the active plan's projection. Replacing an
// existing baseline needs replace=1, which only the confirmation sends: a stale
// page that didn't know there was one must not replace it.
func (s *Server) handleTrackingBaseline(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	existing, err := s.store.GetNetWorthBaseline(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if existing != nil && r.PostForm.Get("replace") != "1" {
		s.renderTrackingBody(w, r, fc, http.StatusUnprocessableEntity, "tracking.baseline.exists")
		return
	}
	in, err := s.loadInput(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	plan, err := s.store.GetPlan(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var res engine.Result
	if len(in.People) > 0 {
		res = engine.Simulate(in)
	}
	if len(res.Months) == 0 {
		s.renderTrackingBody(w, r, fc, http.StatusUnprocessableEntity, "tracking.baseline.need_people")
		return
	}
	b := baselineFromProjection(plan.Name, res, s.now())
	if err := s.store.SaveNetWorthBaseline(r.Context(), b); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.InfoContext(r.Context(), "net worth baseline saved",
		"horizonte.plan.id", fc.plan,
		"horizonte.tracking.baseline_points", len(b.Points),
		"horizonte.tracking.baseline_replaced", existing != nil)
	s.renderTrackingBody(w, r, fc, http.StatusOK, "")
}
