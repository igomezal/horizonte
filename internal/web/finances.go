package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// Event names sent in HX-Trigger so other parts of the page refresh themselves.
const (
	evFinancesChanged = "finances-changed" // summary and section totals
	evPeopleChanged   = "people-changed"   // owner names and selects in other sections
)

// formOptions are the choices offered by the selects in the finance forms.
type formOptions struct {
	AccountKinds      []domain.AccountKind
	IncomeKinds       []domain.IncomeKind
	IncomePayments    []int
	ExpenseCategories []domain.ExpenseCategory
	ExpensePayments   []int
	DebtKinds         []domain.DebtKind
}

var financeOptions = &formOptions{
	AccountKinds:      domain.AccountKinds,
	IncomeKinds:       domain.IncomeKinds,
	IncomePayments:    domain.IncomePayments,
	ExpenseCategories: domain.ExpenseCategories,
	ExpensePayments:   domain.ExpensePayments,
	DebtKinds:         domain.DebtKinds,
}

type rowView struct {
	Kind  string
	ID    int64
	Item  any
	Owner string // owner's name; empty = joint or not applicable
	Now   domain.Month
}

type sectionView struct {
	Kind   string
	Rows   []rowView
	Total  domain.Cents
	People []domain.Person
	CanAdd bool
}

type formView struct {
	Kind   string
	ID     int64 // 0 for a new record
	V      map[string]string
	E      domain.Errors
	People []domain.Person
	Opt    *formOptions
	Now    domain.Month
}

// DOMID is the form element's id, used to re-target validation errors.
func (f formView) DOMID() string {
	if f.ID == 0 {
		return "form-" + f.Kind + "-new"
	}
	return "form-" + f.Kind + "-" + strconv.FormatInt(f.ID, 10)
}

type summaryView struct {
	domain.Summary
	Empty bool // nothing entered yet
}

type financesPage struct {
	Summary  summaryView
	Sections map[string]sectionView
}

// finCtx carries what every finances handler needs.
type finCtx struct {
	plan int64
	lang i18n.Lang
	now  domain.Month
}

func (s *Server) finCtx(r *http.Request) (finCtx, error) {
	lang := i18n.FromContext(r.Context())
	plan, err := s.store.CurrentPlan(r.Context(), s.catalog.T(lang, "plan.default_name"))
	return finCtx{plan: plan, lang: lang, now: domain.MonthOf(s.now())}, err
}

func (s *Server) resourceFor(w http.ResponseWriter, r *http.Request) (*resource, bool) {
	res, ok := s.resources[r.PathValue("kind")]
	if !ok {
		s.handleNotFound(w, r)
	}
	return res, ok
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// partial renders one named template of the finances page.
func (s *Server) partial(w http.ResponseWriter, r *http.Request, status int, block string, data any) {
	if err := s.render.render(w, r, status, "finances", block, data); err != nil {
		s.serverError(w, r, err)
	}
}

func (s *Server) loadSection(ctx context.Context, fc finCtx, res *resource) (sectionView, error) {
	items, err := res.list(ctx, fc.plan)
	if err != nil {
		return sectionView{}, err
	}
	people, err := s.store.ListPeople(ctx, fc.plan)
	if err != nil {
		return sectionView{}, err
	}
	names := make(map[int64]string, len(people))
	for _, p := range people {
		names[p.ID] = p.Name
	}
	sv := sectionView{Kind: res.kind, People: people, CanAdd: true}
	for _, it := range items {
		rv := rowView{Kind: res.kind, ID: itemID(it), Item: it, Now: fc.now}
		if o := itemOwner(it); o != nil {
			rv.Owner = names[*o]
		}
		sv.Rows = append(sv.Rows, rv)
	}
	if res.total != nil {
		sv.Total = res.total(items, fc.now)
	}
	if res.kind == "people" {
		sv.CanAdd = len(people) < domain.MaxPeople
	}
	return sv, nil
}

func (s *Server) loadSummary(ctx context.Context, fc finCtx) (summaryView, error) {
	accounts, err := s.store.ListAccounts(ctx, fc.plan)
	if err != nil {
		return summaryView{}, err
	}
	incomes, err := s.store.ListIncomes(ctx, fc.plan)
	if err != nil {
		return summaryView{}, err
	}
	expenses, err := s.store.ListExpenses(ctx, fc.plan)
	if err != nil {
		return summaryView{}, err
	}
	debts, err := s.store.ListDebts(ctx, fc.plan)
	if err != nil {
		return summaryView{}, err
	}
	return summaryView{
		Summary: domain.Summarize(fc.now, accounts, incomes, expenses, debts),
		Empty:   len(accounts)+len(incomes)+len(expenses)+len(debts) == 0,
	}, nil
}

// GET /finances
func (s *Server) handleFinances(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := financesPage{Sections: map[string]sectionView{}}
	if page.Summary, err = s.loadSummary(r.Context(), fc); err != nil {
		s.serverError(w, r, err)
		return
	}
	for kind, res := range s.resources {
		sv, err := s.loadSection(r.Context(), fc, res)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		page.Sections[kind] = sv
	}
	s.page(w, r, http.StatusOK, "finances", "finances.title", page)
}

// GET /finances/summary
func (s *Server) handleFinancesSummary(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sum, err := s.loadSummary(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.partial(w, r, http.StatusOK, "summary", sum)
}

// GET /finances/{kind}
func (s *Server) handleSection(w http.ResponseWriter, r *http.Request) {
	res, ok := s.resourceFor(w, r)
	if !ok {
		return
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderSection(w, r, fc, res, http.StatusOK)
}

func (s *Server) renderSection(w http.ResponseWriter, r *http.Request, fc finCtx, res *resource, status int) {
	sv, err := s.loadSection(r.Context(), fc, res)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.partial(w, r, status, "fin-section", sv)
}

// GET /finances/{kind}/new
func (s *Server) handleNewForm(w http.ResponseWriter, r *http.Request) {
	res, ok := s.resourceFor(w, r)
	if !ok {
		return
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderForm(w, r, fc, res, http.StatusOK, 0, res.values(fc.lang, nil), domain.Errors{})
}

// GET /finances/{kind}/{id}/edit
func (s *Server) handleEditForm(w http.ResponseWriter, r *http.Request) {
	res, ok := s.resourceFor(w, r)
	if !ok {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	item, err := res.get(r.Context(), fc.plan, id)
	if s.notFoundOrError(w, r, err) {
		return
	}
	s.renderForm(w, r, fc, res, http.StatusOK, id, res.values(fc.lang, item), domain.Errors{})
}

func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, fc finCtx, res *resource, status int, id int64, v map[string]string, e domain.Errors) {
	people, err := s.store.ListPeople(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// With a single person, new accounts and incomes are theirs by default.
	if _, set := v["owner"]; id == 0 && !set && len(people) == 1 && status == http.StatusOK {
		v["owner"] = strconv.FormatInt(people[0].ID, 10)
	}
	fv := formView{Kind: res.kind, ID: id, V: v, E: e, People: people, Opt: financeOptions, Now: fc.now}
	if status == http.StatusUnprocessableEntity {
		// The form posts with the section as target; errors replace the form instead.
		w.Header().Set("HX-Retarget", "#"+fv.DOMID())
		w.Header().Set("HX-Reswap", "outerHTML")
	}
	s.partial(w, r, status, res.kind+"-form", fv)
}

// GET /finances/{kind}/{id}
func (s *Server) handleRow(w http.ResponseWriter, r *http.Request) {
	res, ok := s.resourceFor(w, r)
	if !ok {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sv, err := s.loadSection(r.Context(), fc, res)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, row := range sv.Rows {
		if row.ID == id {
			s.partial(w, r, http.StatusOK, "fin-row", row)
			return
		}
	}
	s.handleNotFound(w, r)
}

// POST /finances/{kind} and PUT /finances/{kind}/{id}
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	res, ok := s.resourceFor(w, r)
	if !ok {
		return
	}
	var id int64
	if r.Method == http.MethodPut {
		if id, ok = pathID(r); !ok {
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

	item, errs := res.parse(newFormParser(fc.lang, r.PostForm), fc.now)
	if res.kind == "people" && id == 0 {
		people, err := s.store.ListPeople(r.Context(), fc.plan)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(people) >= domain.MaxPeople {
			errs.Add("_form", "form.error.max_people")
		}
	}
	if len(errs) == 0 {
		err = res.save(r.Context(), fc.plan, id, item)
		if errors.Is(err, store.ErrNotFound) {
			if id != 0 {
				// The record itself is gone (deleted in another tab).
				s.handleNotFound(w, r)
				return
			}
			// A referenced owner no longer exists.
			errs.Add("owner", domain.ErrChoice)
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if len(errs) > 0 {
		s.renderForm(w, r, fc, res, http.StatusUnprocessableEntity, id, formValuesFromRequest(r.PostForm), errs)
		return
	}
	s.triggerChanged(w, res)
	s.renderSection(w, r, fc, res, http.StatusOK)
}

// DELETE /finances/{kind}/{id}
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	res, ok := s.resourceFor(w, r)
	if !ok {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.handleNotFound(w, r)
		return
	}
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Deleting something already gone is fine: the section re-renders without it.
	if err := res.del(r.Context(), fc.plan, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.triggerChanged(w, res)
	s.renderSection(w, r, fc, res, http.StatusOK)
}

func (s *Server) triggerChanged(w http.ResponseWriter, res *resource) {
	if res.kind == "people" {
		w.Header().Set("HX-Trigger", evFinancesChanged+", "+evPeopleChanged)
		return
	}
	w.Header().Set("HX-Trigger", evFinancesChanged)
}

// notFoundOrError writes a 404 or 500 for err and reports whether it did.
func (s *Server) notFoundOrError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		s.handleNotFound(w, r)
	default:
		s.serverError(w, r, err)
	}
	return true
}
