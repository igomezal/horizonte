package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// Event names sent in HX-Trigger so other parts of the page refresh themselves.
const (
	evFinancesChanged = "finances-changed"   // summary and section totals
	evPeopleChanged   = "people-changed"     // owner names and selects in other sections
	evMilestonesChg   = "milestones-changed" // milestone names and selects in other sections
	evDebtsChanged    = "debts-changed"      // debt names in the early-repayments section
	evAccountsChanged = "accounts-changed"   // account names in debts and early repayments
)

// formOptions are the choices offered by the selects in the finance forms.
type formOptions struct {
	AccountKinds      []domain.AccountKind
	IncomeKinds       []domain.IncomeKind
	IncomePayments    []int
	ExpenseCategories []domain.ExpenseCategory
	ExpensePayments   []int
	DebtKinds         []domain.DebtKind
	MilestoneKinds    []domain.MilestoneKind
	FIVariants        []domain.FIVariant
	PrepayFrequencies []domain.PrepaymentFrequency
	PrepayModes       []domain.PrepaymentMode
}

var financeOptions = &formOptions{
	AccountKinds:      domain.AccountKinds,
	IncomeKinds:       domain.IncomeKinds,
	IncomePayments:    domain.IncomePayments,
	ExpenseCategories: domain.ExpenseCategories,
	ExpensePayments:   domain.ExpensePayments,
	DebtKinds:         domain.DebtKinds,
	MilestoneKinds:    domain.MilestoneKinds,
	FIVariants:        domain.FIVariants,
	PrepayFrequencies: domain.PrepaymentFrequencies,
	PrepayModes:       domain.PrepaymentModes,
}

type rowView struct {
	Kind  string
	ID    int64
	Item  any
	Owner string // owner's (or an age milestone's person's) name
	Now   domain.Month
	// MS maps milestone IDs to names, for "Desde Jubilación" labels.
	MS map[int64]string
	// When is a fixed milestone's month (zero for conditional ones).
	When domain.Month
	// Debt is the name of an early repayment's debt.
	Debt string
	// Accounts maps account IDs to names, for "the instalment goes to …".
	Accounts map[int64]string
	// HasPrepayments marks debts with early repayments (their payoff date
	// shown in the row ignores them).
	HasPrepayments bool
	// SaleLapsed marks an account whose sale is dated before the plan starts.
	SaleLapsed bool
	// SalesAtRisk names the property whose sale deleting this milestone cancels.
	SalesAtRisk string
}

// AccountName returns the name of a linked account, or "" when not linked.
func (r rowView) AccountName(id *int64) string {
	if id == nil {
		return ""
	}
	return r.Accounts[*id]
}

// MSName returns the name of a linked milestone, or "" when not linked.
func (r rowView) MSName(id *int64) string {
	if id == nil {
		return ""
	}
	return r.MS[*id]
}

type sectionView struct {
	Kind     string
	Rows     []rowView
	Total    domain.Cents
	HasTotal bool // the section shows a headline amount
	People   []domain.Person
	CanAdd   bool
	EmptyKey string // i18n key of the empty state
}

type formView struct {
	Kind       string
	ID         int64 // 0 for a new record
	V          map[string]string
	E          domain.Errors
	People     []domain.Person
	Milestones []domain.Milestone
	Debts      []domain.Debt
	Accounts   []domain.Account // destinations for money freed by paid-off debts
	Opt        *formOptions
	Now        domain.Month
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
	milestones, err := s.store.ListMilestones(ctx, fc.plan)
	if err != nil {
		return sectionView{}, err
	}
	names := make(map[int64]string, len(people))
	for _, p := range people {
		names[p.ID] = p.Name
	}
	msNames := make(map[int64]string, len(milestones))
	for _, m := range milestones {
		msNames[m.ID] = m.Name
	}
	sv := sectionView{Kind: res.kind, People: people, CanAdd: true, EmptyKey: res.kind + ".empty"}
	debtNames, prepaid, accNames := map[int64]string{}, map[int64]bool{}, map[int64]string{}
	if res.kind == "debts" || res.kind == "prepayments" {
		accounts, err := s.store.ListAccounts(ctx, fc.plan)
		if err != nil {
			return sectionView{}, err
		}
		for _, a := range accounts {
			accNames[a.ID] = a.Name
		}
		debts, err := s.store.ListDebts(ctx, fc.plan)
		if err != nil {
			return sectionView{}, err
		}
		for _, d := range debts {
			debtNames[d.ID] = d.Name
		}
		pps, err := s.store.ListPrepayments(ctx, fc.plan)
		if err != nil {
			return sectionView{}, err
		}
		for _, p := range pps {
			prepaid[p.DebtID] = true
		}
		if res.kind == "prepayments" && len(debts) == 0 {
			sv.CanAdd, sv.EmptyKey = false, "prepayments.need_debt"
		}
	}
	// Sales that have no date of their own are cancelled with their milestone.
	salesAtRisk := map[int64][]string{}
	if res.kind == "milestones" {
		accounts, err := s.store.ListAccounts(ctx, fc.plan)
		if err != nil {
			return sectionView{}, err
		}
		for _, a := range accounts {
			if a.Sale != nil && a.Sale.MS != nil && a.Sale.Month.IsZero() {
				salesAtRisk[*a.Sale.MS] = append(salesAtRisk[*a.Sale.MS], a.Name)
			}
		}
	}
	resolved := domain.ResolveFixed(milestones, people)
	for _, it := range items {
		rv := rowView{Kind: res.kind, ID: itemID(it), Item: it, Now: fc.now, MS: msNames, Accounts: accNames}
		if o := itemOwner(it); o != nil {
			rv.Owner = names[*o]
		}
		switch v := it.(type) {
		case domain.Milestone:
			rv.When, _ = v.FixedMonth(people)
			rv.SalesAtRisk = strings.Join(salesAtRisk[v.ID], ", ")
		case domain.Account:
			rv.SaleLapsed = v.Sale != nil && v.Sale.Lapsed(resolved, fc.now)
		case domain.Prepayment:
			rv.Debt = debtNames[v.DebtID]
		case domain.Debt:
			rv.HasPrepayments = prepaid[v.ID]
		}
		sv.Rows = append(sv.Rows, rv)
	}
	if res.total != nil {
		sv.Total, sv.HasTotal = res.total(items, fc.now, domain.ResolveFixed(milestones, people)), true
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
	people, err := s.store.ListPeople(ctx, fc.plan)
	if err != nil {
		return summaryView{}, err
	}
	milestones, err := s.store.ListMilestones(ctx, fc.plan)
	if err != nil {
		return summaryView{}, err
	}
	prepayments, err := s.store.ListPrepayments(ctx, fc.plan)
	if err != nil {
		return summaryView{}, err
	}
	return summaryView{
		Summary: domain.Summarize(fc.now, domain.ResolveFixed(milestones, people), accounts, incomes, expenses, debts, prepayments),
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
	milestones, err := s.store.ListMilestones(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	debts, err := s.store.ListDebts(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	accounts, err := s.store.ListAccounts(r.Context(), fc.plan)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// With a single person, new records are theirs by default.
	if id == 0 && len(people) == 1 && status == http.StatusOK {
		only := strconv.FormatInt(people[0].ID, 10)
		if len(debts) == 1 {
			if _, set := v["debt"]; !set {
				v["debt"] = strconv.FormatInt(debts[0].ID, 10)
			}
		}
		for _, field := range []string{"owner", "person", "coast_person"} {
			if _, set := v[field]; !set {
				v[field] = only
			}
		}
	}
	fv := formView{Kind: res.kind, ID: id, V: v, E: e, People: people, Milestones: milestones, Debts: debts, Accounts: surplusCandidates(accounts), Opt: financeOptions, Now: fc.now}
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
	if id != 0 {
		// The record itself may be gone (deleted in another tab).
		if _, err := res.get(r.Context(), fc.plan, id); s.notFoundOrError(w, r, err) {
			return
		}
	}
	if len(errs) == 0 {
		err = res.save(r.Context(), fc.plan, id, item)
		if errors.Is(err, store.ErrNotFound) {
			// A person or milestone chosen in the form no longer exists.
			errs.Add("_form", "form.error.stale")
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
	switch res.kind {
	case "people":
		w.Header().Set("HX-Trigger", evFinancesChanged+", "+evPeopleChanged)
	case "milestones":
		w.Header().Set("HX-Trigger", evFinancesChanged+", "+evMilestonesChg)
	case "debts":
		w.Header().Set("HX-Trigger", evFinancesChanged+", "+evDebtsChanged)
	case "accounts":
		w.Header().Set("HX-Trigger", evFinancesChanged+", "+evAccountsChanged)
	case "prepayments":
		// The debt rows note whether a debt has early repayments.
		w.Header().Set("HX-Trigger", evFinancesChanged+", "+evDebtsChanged)
	default:
		w.Header().Set("HX-Trigger", evFinancesChanged)
	}
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
