package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/igomezal/horizonte/internal/backup"
	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/history"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// Presets applied to a copied plan's assumptions.
var scenarioPresets = []string{"exact", "pessimistic", "optimistic"}

// mcRunsCompare keeps the comparison page quick: each plan gets this many
// Monte Carlo scenarios (the Probabilidad de éxito page uses the plan's own count).
const mcRunsCompare = 500

type scenarioRow struct {
	ID         int64
	Name       string
	ParentName string
	Active     bool
	NoPeople   bool

	Failed    bool
	FailAge   int
	EndAge    int
	EndReal   float64
	MCRate    float64
	HistRate  float64
	HasHist   bool
	FIName    string
	FIAge     int
	FIReached bool
	HasFI     bool

	years map[int]float64 // net worth in today's euros by year, for the chart
}

type scenarioChartSpec struct {
	Type       string           `json:"type"`
	Categories []string         `json:"categories"`
	Series     []scenarioSeries `json:"series"`
}

type scenarioSeries struct {
	Name   string     `json:"name"`
	Active bool       `json:"active"`
	Data   []*float64 `json:"data"`
}

type scenariosPage struct {
	Rows    []scenarioRow
	Chart   scenarioChartSpec
	Plans   []store.Plan
	Current int64
	Presets []string
	Form    map[string]string
	Error   string // i18n key
	// ImportError is the (translated) reason an imported file was rejected.
	ImportError string
}

func (s *Server) summarizePlan(ctx context.Context, lang i18n.Lang, now domain.Month, p store.Plan, hs *history.Series) (scenarioRow, error) {
	row := scenarioRow{ID: p.ID, Name: p.Name, years: map[int]float64{}}
	in, err := s.loadInput(ctx, finCtx{plan: p.ID, lang: lang, now: now})
	if err != nil {
		return row, err
	}
	if len(in.People) == 0 {
		row.NoPeople = true
		return row, nil
	}
	main := in.People[0]
	det := engine.Simulate(in)
	if len(det.Months) == 0 {
		row.NoPeople = true
		return row, nil
	}
	last := det.Months[len(det.Months)-1]
	row.EndAge, row.EndReal = main.AgeAt(last.Month), last.NetWorth/last.Deflator
	if det.Failed() {
		row.Failed, row.FailAge = true, main.AgeAt(det.FailMonth)
	}
	for _, y := range det.Years() {
		row.years[y.Year] = y.Last.NetWorth / y.Last.Deflator
	}
	for _, h := range det.Milestones {
		if h.Milestone.Conditional() {
			row.HasFI, row.FIName = true, h.Milestone.Name
			if h.Reached() {
				row.FIReached, row.FIAge = true, main.AgeAt(h.Month)
			}
			break
		}
	}

	mc := engine.MonteCarlo(in, in.Assumptions.Volatility, engine.MCSettings{
		Runs: mcRunsCompare, Seed: defaultSeed, Correlation: mcCorrelation,
	})
	row.MCRate = mc.SuccessRate()

	full, ok := 0, 0
	for _, st := range engine.Rolling(in, hs) {
		if st.Partial {
			continue
		}
		full++
		if !st.Failed {
			ok++
		}
	}
	if full > 0 {
		row.HasHist, row.HistRate = true, float64(ok)/float64(full)
	}
	return row, nil
}

func (s *Server) buildScenariosPage(ctx context.Context, lang i18n.Lang, now domain.Month) (scenariosPage, error) {
	page := scenariosPage{Presets: scenarioPresets, Form: map[string]string{}}
	plans, err := s.store.ListPlans(ctx)
	if err != nil {
		return page, err
	}
	cur, err := s.store.CurrentPlan(ctx, s.catalog.T(lang, "plan.default_name"))
	if err != nil {
		return page, err
	}
	if len(plans) == 0 { // CurrentPlan just created the first one
		if plans, err = s.store.ListPlans(ctx); err != nil {
			return page, err
		}
	}
	page.Plans, page.Current = plans, cur
	page.Form["source"] = strconv.FormatInt(cur, 10)
	page.Form["preset"] = "exact"

	hs, err := history.Load()
	if err != nil {
		return page, err
	}
	names := map[int64]string{}
	for _, p := range plans {
		names[p.ID] = p.Name
	}
	yearSet := map[int]bool{}
	for _, p := range plans {
		row, err := s.summarizePlan(ctx, lang, now, p, hs)
		if err != nil {
			return page, err
		}
		row.Active = p.ID == cur
		if p.ParentID != nil {
			row.ParentName = names[*p.ParentID]
		}
		for y := range row.years {
			yearSet[y] = true
		}
		page.Rows = append(page.Rows, row)
	}

	var years []int
	for y := range yearSet {
		years = append(years, y)
	}
	sort.Ints(years)
	page.Chart = scenarioChartSpec{Type: "scenarios"}
	for _, y := range years {
		page.Chart.Categories = append(page.Chart.Categories, strconv.Itoa(y))
	}
	for _, row := range page.Rows {
		if row.NoPeople {
			continue
		}
		ser := scenarioSeries{Name: row.Name, Active: row.Active}
		for _, y := range years {
			if v, ok := row.years[y]; ok {
				r := round0(v)
				ser.Data = append(ser.Data, &r)
			} else {
				ser.Data = append(ser.Data, nil)
			}
		}
		page.Chart.Series = append(page.Chart.Series, ser)
	}
	return page, nil
}

// applyPreset shifts a copied plan's assumptions.
func applyPreset(a *engine.Assumptions, preset string) {
	shift := 0.0
	switch preset {
	case "pessimistic":
		shift = -0.02
		a.Inflation += 0.01
	case "optimistic":
		shift = 0.01
	default:
		return
	}
	for k, r := range a.Returns {
		if k == domain.AccountCash {
			continue
		}
		a.Returns[k] = r + shift
	}
}

func validPlanName(name string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(name))
	return n > 0 && n <= 80
}

// GET /scenarios
func (s *Server) handleScenarios(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.buildScenariosPage(r.Context(), fc.lang, fc.now)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "scenarios", "scenarios.title", page)
}

// POST /scenarios: copy a plan into a new scenario and make it active.
func (s *Server) handleCreateScenario(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	name := strings.TrimSpace(strings.ToValidUTF8(r.FormValue("name"), ""))
	preset := r.FormValue("preset")
	src, convErr := strconv.ParseInt(r.FormValue("source"), 10, 64)
	errKey := ""
	switch {
	case !validPlanName(name):
		errKey = "scenarios.error.name"
	case convErr != nil:
		errKey = "form.error.choice"
	}
	presetOK := false
	for _, p := range scenarioPresets {
		presetOK = presetOK || p == preset
	}
	if !presetOK {
		errKey = "form.error.choice"
	}
	var id int64
	if errKey == "" {
		id, err = s.store.CopyPlan(r.Context(), src, name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			errKey = "form.error.stale"
		case err != nil:
			s.serverError(w, r, err)
			return
		}
	}
	if errKey != "" {
		page, err := s.buildScenariosPage(r.Context(), fc.lang, fc.now)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		page.Error = errKey
		page.Form = map[string]string{"name": name, "source": r.FormValue("source"), "preset": preset}
		s.page(w, r, http.StatusUnprocessableEntity, "scenarios", "scenarios.title", page)
		return
	}
	a, err := s.store.GetAssumptions(r.Context(), id)
	if err == nil {
		applyPreset(&a, preset)
		err = s.store.SaveAssumptions(r.Context(), id, a)
	}
	if err == nil {
		err = s.store.SetCurrentPlan(r.Context(), id)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/scenarios", http.StatusSeeOther)
}

func planIDFromPath(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// POST /scenarios/{id}/activate (and the top-bar switcher, POST /plans/switch)
func (s *Server) handleActivatePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := planIDFromPath(r)
	if !ok {
		id, _ = strconv.ParseInt(r.FormValue("plan"), 10, 64)
	}
	if err := s.store.SetCurrentPlan(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.handleNotFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	next := safeRedirect(r.FormValue("next"))
	if r.FormValue("next") == "" {
		next = "/scenarios"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// POST /scenarios/{id}/rename
func (s *Server) handleRenamePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := planIDFromPath(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	name := strings.TrimSpace(strings.ToValidUTF8(r.FormValue("name"), ""))
	if validPlanName(name) {
		if err := s.store.RenamePlan(r.Context(), id, name); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, "/scenarios", http.StatusSeeOther)
}

// POST /scenarios/{id}/delete
func (s *Server) handleDeletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := planIDFromPath(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	if err := s.store.DeletePlan(r.Context(), id); err != nil &&
		!errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrLastPlan) {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/scenarios", http.StatusSeeOther)
}

// GET /scenarios/{id}/export: the plan as a JSON file.
func (s *Server) handleExportPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := planIDFromPath(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	p, err := s.store.GetPlan(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	snap, err := s.store.LoadSnapshot(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	now := time.Now()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.json"`, slug(p.Name), now.Format("2006-01-02")))
	w.Header().Set("Cache-Control", "no-store")
	if err := backup.Write(w, backup.FromSnapshot(p.Name, snap, now)); err != nil {
		s.log.Error("export plan", "err", err)
	}
}

// POST /scenarios/import: create a new plan from a JSON file and make it active.
func (s *Server) handleImportPlan(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	fail := func(msg string) {
		page, err := s.buildScenariosPage(r.Context(), fc.lang, fc.now)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		page.ImportError = msg
		s.page(w, r, http.StatusUnprocessableEntity, "scenarios", "scenarios.title", page)
	}
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxSize+64<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(s.catalog.T(fc.lang, "import.error.too_large"))
		} else {
			fail(s.catalog.T(fc.lang, "import.error.no_file"))
		}
		return
	}
	defer file.Close()
	entries, err := backup.Read(file, fc.now)
	if err != nil {
		fail(s.importErrorMessage(fc.lang, err))
		return
	}
	if _, err := s.importEntries(r.Context(), entries); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/scenarios", http.StatusSeeOther)
}

// importEntries creates every plan of a file in one transaction, keeping the
// scenario links between them, and activates the plan marked active (or the
// first one). It returns the new plan IDs in file order.
func (s *Server) importEntries(ctx context.Context, entries []backup.Entry) ([]int64, error) {
	var ids []int64
	err := s.store.InTx(ctx, func(tx *store.Store) error {
		newID := map[int64]int64{}
		active := int64(0)
		for _, e := range entries {
			var parent *int64
			if e.ParentID != nil { // parents come first in exported files
				if id, ok := newID[*e.ParentID]; ok {
					parent = &id
				}
			}
			id, err := tx.CreatePlanFromSnapshot(ctx, e.Name, parent, e.Snapshot)
			if err != nil {
				return err
			}
			if e.ID > 0 {
				newID[e.ID] = id
			}
			if e.Active || active == 0 {
				active = id
			}
			ids = append(ids, id)
		}
		return tx.SetCurrentPlan(ctx, active)
	})
	return ids, err
}

// GET /scenarios/export: every plan in one JSON file.
func (s *Server) handleExportAll(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	plans, err := s.store.ListPlans(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var entries []backup.Entry
	for _, p := range plans {
		snap, err := s.store.LoadSnapshot(r.Context(), p.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		entries = append(entries, backup.Entry{ID: p.ID, ParentID: p.ParentID, Active: p.ID == fc.plan, Name: p.Name, Snapshot: snap})
	}
	now := time.Now()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.json"`,
		slug(s.catalog.T(fc.lang, "scenarios.export_all_file")), now.Format("2006-01-02")))
	w.Header().Set("Cache-Control", "no-store")
	if err := backup.Write(w, backup.FromEntries(entries, now)); err != nil {
		s.log.Error("export plans", "err", err)
	}
}

func (s *Server) importErrorMessage(lang i18n.Lang, err error) string {
	var ie *backup.InvalidError
	switch {
	case errors.Is(err, backup.ErrNotPlan):
		return s.catalog.T(lang, "import.error.format")
	case errors.Is(err, backup.ErrVersion):
		return s.catalog.T(lang, "import.error.version")
	case errors.Is(err, backup.ErrTooLarge):
		return s.catalog.T(lang, "import.error.too_large")
	case errors.As(err, &ie):
		section := s.catalog.T(lang, "import.section."+ie.Section)
		msg := s.catalog.T(lang, "import.error.invalid_named", section, ie.Name, ie.Field)
		if ie.Name == "" {
			msg = s.catalog.T(lang, "import.error.invalid", section, ie.Field)
		}
		if ie.Plan != "" {
			msg = s.catalog.T(lang, "import.error.in_plan", ie.Plan) + " " + msg
		}
		return msg
	}
	return s.catalog.T(lang, "import.error.format")
}

// slug makes an ASCII file name from a plan name.
func slug(name string) string {
	fold := map[rune]string{'á': "a", 'é': "e", 'í': "i", 'ó': "o", 'ú': "u", 'ü': "u", 'ñ': "n", 'ç': "c", 'à': "a", 'è': "e", 'ò': "o", 'ï': "i"}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if f, ok := fold[r]; ok {
			b.WriteString(f)
			dash = false
		} else if r < 128 && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if out == "" {
		out = "plan"
	}
	return out
}
