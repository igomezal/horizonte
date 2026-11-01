package web

import (
	"math"
	"net/http"
	"strconv"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
	pension "github.com/igomezal/horizonte/internal/pension/es"
)

// pensionForm is the public pension calculator's form state.
type pensionForm struct {
	DOMID  string
	V      map[string]string
	E      domain.Errors
	People []domain.Person
}

type pensionView struct {
	pension.Result
	Person   string
	Start    domain.Month // month the pension starts
	Years    float64      // years contributed when it starts
	MaxBase  float64
	MinBase  float64
	BaseUsed float64 // contribution base after the legal limits
}

type pensionPage struct {
	HasPeople bool
	Form      pensionForm
	Result    *pensionView
	Error     string // i18n key for the add button
}

// pensionDefaults fills the form for a person from the plan: contribution
// base from their current work income, contributions until the milestone
// their salary ends on. Joint income counts as theirs when they are alone
// in the plan.
func pensionDefaults(lang i18n.Lang, now domain.Month, p domain.Person, alone bool, incomes []domain.Income, ms []domain.Milestone) map[string]string {
	age := p.AgeAt(now)
	var annual domain.Cents
	until := 67
	for _, inc := range incomes {
		mine := inc.OwnerID != nil && *inc.OwnerID == p.ID || inc.OwnerID == nil && alone
		if !mine {
			continue
		}
		if inc.Kind != domain.IncomeSalary && inc.Kind != domain.IncomeSelfEmployed {
			continue
		}
		if (inc.Start.IsZero() || !now.Before(inc.Start)) && (inc.End.IsZero() || !inc.End.Before(now)) {
			annual += inc.Annual()
		}
		if inc.EndMS != nil {
			for _, m := range ms {
				if m.ID == *inc.EndMS && m.Kind == domain.MilestoneAge && m.PersonID != nil && *m.PersonID == p.ID && m.Age > 0 {
					until = m.Age
				}
			}
		} else if !inc.End.IsZero() {
			until = p.AgeAt(inc.End)
		}
	}
	base := ""
	if annual > 0 {
		base = inMoney(lang, annual/12)
	}
	return map[string]string{
		"person": strconv.FormatInt(p.ID, 10),
		"years":  strconv.Itoa(max(0, age-25)),
		"base":   base,
		"until":  strconv.Itoa(max(until, age)),
		"retire": "",
		"regime": "employee",
		"topup":  "",
	}
}

// parsePension reads the form. It returns the person and calculator input.
func parsePension(lang i18n.Lang, now domain.Month, v map[string][]string, people []domain.Person) (pensionForm, *domain.Person, pension.Input) {
	p := newFormParser(lang, v)
	f := pensionForm{DOMID: "pension", People: people, V: map[string]string{}}
	for _, k := range []string{"person", "years", "base", "until", "retire", "regime", "topup"} {
		f.V[k] = p.str(k)
	}
	var who *domain.Person
	for i := range people {
		if strconv.FormatInt(people[i].ID, 10) == f.V["person"] {
			who = &people[i]
		}
	}
	if who == nil {
		p.errs.Add("person", domain.ErrChoice)
	}
	years := p.decimal("years")
	if years < 0 || years > 60 {
		p.errs.Add("years", domain.ErrRange)
	}
	base := p.money("base", true)
	if base < 0 {
		p.errs.Add("base", domain.ErrNegative)
	}
	until := p.integer("until")
	if until < 16 || until > 75 {
		p.errs.Add("until", domain.ErrRange)
	}
	retire := 0
	if f.V["retire"] != "" {
		retire = p.integer("retire")
		if retire < 60 || retire > 75 {
			p.errs.Add("retire", domain.ErrRange)
		}
	}
	if f.V["regime"] != "employee" && f.V["regime"] != "selfemployed" {
		p.errs.Add("regime", domain.ErrChoice)
	}
	in := pension.Input{YearsContributed: years, MonthlyBase: base.Euros(), ContributeUntilAge: until, RetireAge: retire,
		SelfEmployed: f.V["regime"] == "selfemployed", TopUpToMinimum: f.V["topup"] != ""}
	if who != nil {
		in.AgeMonths = who.Birth.MonthsUntil(now)
		if until < who.AgeAt(now) {
			p.errs.Add("until", domain.ErrRange)
		}
		if retire > 0 && !now.Before(who.Birth.AddMonths(retire*12)) {
			p.errs.Add("retire", domain.ErrRange)
		}
		if float64(who.AgeAt(now)-14) < years {
			p.errs.Add("years", domain.ErrRange)
		}
	}
	f.E = p.errs
	return f, who, in
}

func estimatePension(who domain.Person, in pension.Input) *pensionView {
	r := pension.Estimate(in)
	return &pensionView{Result: r, Person: who.Name, Start: who.Birth.AddMonths(r.RetireAge * 12),
		Years: float64(r.MonthsAtRetire) / 12, MaxBase: pension.MaxBase, MinBase: pension.MinBase,
		BaseUsed: math.Max(pension.MinBase, math.Min(pension.MaxBase, in.MonthlyBase))}
}

func (s *Server) pensionPeople(r *http.Request) (finCtx, []domain.Person, error) {
	fc, err := s.finCtx(r)
	if err != nil {
		return fc, nil, err
	}
	people, err := s.store.ListPeople(r.Context(), fc.plan)
	return fc, people, err
}

// GET /finances/pension: the calculator, filled from the plan. ?person=
// switches to another person's defaults.
func (s *Server) handlePension(w http.ResponseWriter, r *http.Request) {
	fc, people, err := s.pensionPeople(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := pensionPage{HasPeople: len(people) > 0}
	if page.HasPeople {
		who := people[0]
		for _, p := range people {
			if strconv.FormatInt(p.ID, 10) == r.URL.Query().Get("person") {
				who = p
			}
		}
		incomes, err := s.store.ListIncomes(r.Context(), fc.plan)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		ms, err := s.store.ListMilestones(r.Context(), fc.plan)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		v := pensionDefaults(fc.lang, fc.now, who, len(people) == 1, incomes, ms)
		form := map[string][]string{}
		for k, x := range v {
			form[k] = []string{x}
		}
		f, p, in := parsePension(fc.lang, fc.now, form, people)
		page.Form = f
		if len(f.E) == 0 && p != nil {
			page.Result = estimatePension(*p, in)
		} else {
			page.Form.E = domain.Errors{} // defaults that do not validate (e.g. no salary) are not errors yet
		}
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "pension-tool" {
		s.renderFragment(w, r, "pension", "pension-tool", page)
		return
	}
	s.page(w, r, http.StatusOK, "pension", "pension.title", page)
}

// POST /finances/pension/estimate: recompute as the form changes.
func (s *Server) handlePensionEstimate(w http.ResponseWriter, r *http.Request) {
	fc, people, err := s.pensionPeople(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f, who, in := parsePension(fc.lang, fc.now, r.PostForm, people)
	page := pensionPage{HasPeople: len(people) > 0, Form: f}
	if len(f.E) == 0 && who != nil {
		page.Result = estimatePension(*who, in)
	}
	s.renderFragment(w, r, "pension", "pension-result", page)
}

// POST /finances/pension: add the estimate as a pension income.
func (s *Server) handlePensionAdd(w http.ResponseWriter, r *http.Request) {
	fc, people, err := s.pensionPeople(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f, who, in := parsePension(fc.lang, fc.now, r.PostForm, people)
	page := pensionPage{HasPeople: len(people) > 0, Form: f}
	if len(f.E) == 0 && who != nil {
		page.Result = estimatePension(*who, in)
		if page.Result.Entitled && page.Result.Monthly > 0 {
			inc := domain.Income{PlanID: fc.plan, Name: s.catalog.T(fc.lang, "pension.income_name", who.Name),
				Kind: domain.IncomePension, OwnerID: &who.ID, Amount: domain.FromEuros(page.Result.Monthly),
				PaymentsPerYear: pension.Payments, Start: page.Result.Start}
			if _, err := s.store.SaveIncome(r.Context(), inc); err != nil {
				s.serverError(w, r, err)
				return
			}
			http.Redirect(w, r, "/finances#section-incomes", http.StatusSeeOther)
			return
		}
		page.Error = "pension.error.not_entitled"
	}
	s.page(w, r, http.StatusUnprocessableEntity, "pension", "pension.title", page)
}

// renderFragment renders one block of a page template.
func (s *Server) renderFragment(w http.ResponseWriter, r *http.Request, page, block string, data any) {
	if err := s.render.render(w, r, http.StatusOK, page, block, data); err != nil {
		s.serverError(w, r, err)
	}
}
