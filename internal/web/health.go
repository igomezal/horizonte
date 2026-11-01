package web

import (
	"net/http"
	"strconv"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// healthPhase is one age band of a health cost. To = 0 means until the end
// of the plan.
type healthPhase struct {
	Concept  string // insurance, outofpocket, care
	From, To int
	Monthly  domain.Cents
}

// defaultHealthPhases are rough Spanish figures in today's euros: individual
// private insurance premiums rising with age, out-of-pocket costs public
// healthcare does not cover (dentist, glasses, pharmacy co-payments), and a
// private care home late in life (off by default).
var defaultHealthPhases = []healthPhase{
	{"insurance", 0, 44, 6000},
	{"insurance", 45, 54, 8000},
	{"insurance", 55, 64, 12000},
	{"insurance", 65, 74, 18000},
	{"insurance", 75, 0, 25000},
	{"outofpocket", 0, 64, 3000},
	{"outofpocket", 65, 0, 6000},
	{"care", 85, 0, 0},
}

type healthRow struct {
	Concept string
	Ages    string
	Past    bool // the band is over for this person
	Years   float64
	Total   float64 // monthly × months in the plan, today's euros
}

type healthPage struct {
	HasPeople bool
	Form      pensionForm // same shape: DOMID, values, errors, people
	Rows      []healthRow
	Total     float64
	Added     int
}

func (s *Server) healthFormFor(lang i18n.Lang, p domain.Person) map[string]string {
	v := map[string]string{"person": strconv.FormatInt(p.ID, 10)}
	for i, ph := range defaultHealthPhases {
		k := strconv.Itoa(i)
		v["from"+k] = strconv.Itoa(ph.From)
		v["to"+k] = ""
		if ph.To > 0 {
			v["to"+k] = strconv.Itoa(ph.To)
		}
		v["amount"+k] = ""
		if ph.Monthly > 0 {
			v["amount"+k] = inMoney(lang, ph.Monthly)
		}
	}
	return v
}

// parseHealth reads the phases. Rows with no amount are skipped.
func parseHealth(lang i18n.Lang, now domain.Month, values map[string][]string, people []domain.Person) (pensionForm, *domain.Person, []healthPhase) {
	p := newFormParser(lang, values)
	f := pensionForm{DOMID: "health", People: people, V: map[string]string{"person": p.str("person")}}
	var who *domain.Person
	for i := range people {
		if strconv.FormatInt(people[i].ID, 10) == f.V["person"] {
			who = &people[i]
		}
	}
	if who == nil {
		p.errs.Add("person", domain.ErrChoice)
	}
	var phases []healthPhase
	for i, def := range defaultHealthPhases {
		k := strconv.Itoa(i)
		for _, name := range []string{"from", "to", "amount"} {
			f.V[name+k] = p.str(name + k)
		}
		amount := p.money("amount"+k, false)
		if amount < 0 {
			p.errs.Add("amount"+k, domain.ErrNegative)
		}
		if amount <= 0 {
			continue
		}
		from := p.integer("from" + k)
		to := 0
		if f.V["to"+k] != "" {
			to = p.integer("to" + k)
		}
		if from < 0 || from > 120 {
			p.errs.Add("from"+k, domain.ErrRange)
		}
		if to != 0 && (to < from || to > 120) {
			p.errs.Add("to"+k, domain.ErrRange)
		}
		phases = append(phases, healthPhase{Concept: def.Concept, From: from, To: to, Monthly: amount})
	}
	f.E = p.errs
	return f, who, phases
}

// phaseSpan is the months a phase covers within the plan (zero months when
// it is over or starts after the plan ends).
func phaseSpan(p domain.Person, ph healthPhase, now domain.Month) (start, end domain.Month, months int) {
	start = p.Birth.AddMonths(ph.From * 12)
	if start.Before(now) {
		start = now
	}
	planEnd := p.Birth.AddMonths(p.LifeExpectancy*12 + 11)
	end = planEnd
	if ph.To > 0 {
		end = p.Birth.AddMonths((ph.To+1)*12 - 1)
	}
	if planEnd.Before(end) {
		end = planEnd
	}
	months = start.MonthsUntil(end) + 1
	if months < 0 {
		months = 0
	}
	return start, end, months
}

func (s *Server) buildHealthPage(lang i18n.Lang, now domain.Month, f pensionForm, who *domain.Person, phases []healthPhase) healthPage {
	page := healthPage{HasPeople: len(f.People) > 0, Form: f}
	if who == nil {
		return page
	}
	for _, ph := range phases {
		_, _, months := phaseSpan(*who, ph, now)
		row := healthRow{Concept: ph.Concept, Ages: phaseAges(ph), Past: months == 0, Years: float64(months) / 12, Total: ph.Monthly.Euros() * float64(months)}
		page.Total += row.Total
		page.Rows = append(page.Rows, row)
	}
	return page
}

func (s *Server) healthPeople(r *http.Request) (finCtx, []domain.Person, error) {
	return s.pensionPeople(r)
}

// GET /finances/health
func (s *Server) handleHealthTool(w http.ResponseWriter, r *http.Request) {
	fc, people, err := s.healthPeople(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := healthPage{}
	if len(people) > 0 {
		who := people[0]
		for _, p := range people {
			if strconv.FormatInt(p.ID, 10) == r.URL.Query().Get("person") {
				who = p
			}
		}
		form := map[string][]string{}
		for k, v := range s.healthFormFor(fc.lang, who) {
			form[k] = []string{v}
		}
		f, p, phases := parseHealth(fc.lang, fc.now, form, people)
		page = s.buildHealthPage(fc.lang, fc.now, f, p, phases)
	}
	s.page(w, r, http.StatusOK, "health", "health.title", page)
}

// POST /finances/health/preview: totals as the phases change.
func (s *Server) handleHealthPreview(w http.ResponseWriter, r *http.Request) {
	fc, people, err := s.healthPeople(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f, who, phases := parseHealth(fc.lang, fc.now, r.PostForm, people)
	page := s.buildHealthPage(fc.lang, fc.now, f, who, phases)
	s.renderFragment(w, r, "health", "health-summary", page)
}

// POST /finances/health: add each phase as a health expense.
func (s *Server) handleHealthAdd(w http.ResponseWriter, r *http.Request) {
	fc, people, err := s.healthPeople(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f, who, phases := parseHealth(fc.lang, fc.now, r.PostForm, people)
	page := s.buildHealthPage(fc.lang, fc.now, f, who, phases)
	if len(f.E) > 0 || who == nil {
		s.page(w, r, http.StatusUnprocessableEntity, "health", "health.title", page)
		return
	}
	var add []domain.Expense
	for _, ph := range phases {
		start, end, months := phaseSpan(*who, ph, fc.now)
		if months == 0 {
			continue
		}
		e := domain.Expense{PlanID: fc.plan, Name: s.catalog.T(fc.lang, "health.expense_name", s.catalog.T(fc.lang, "health.concept."+ph.Concept), who.Name, phaseAges(ph)),
			Category: domain.ExpenseHealth, Amount: ph.Monthly, PaymentsPerYear: 12, Start: start}
		if ph.To > 0 && end.Before(who.Birth.AddMonths(who.LifeExpectancy*12+11)) {
			e.End = end
		}
		add = append(add, e)
	}
	if len(add) == 0 {
		page.Form.E = domain.Errors{"rows": "health.error.nothing"}
		s.page(w, r, http.StatusUnprocessableEntity, "health", "health.title", page)
		return
	}
	err = s.store.InTx(r.Context(), func(tx *store.Store) error {
		for _, e := range add {
			if _, err := tx.SaveExpense(r.Context(), e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/finances#section-expenses", http.StatusSeeOther)
}

// phaseAges labels an age band: "45–54" or "75+".
func phaseAges(ph healthPhase) string {
	if ph.To > 0 {
		return strconv.Itoa(ph.From) + "–" + strconv.Itoa(ph.To)
	}
	return strconv.Itoa(ph.From) + "+"
}
