package web

import (
	"context"
	"net/url"
	"strconv"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/i18n"
	"github.com/igomezal/horizonte/internal/store"
)

// resource describes one editable list on the Current Finances page. The
// generic handlers in finances.go use it to list, show, create, update and
// delete records, and render the "<kind>-section", "<kind>-row" and
// "<kind>-form" templates.
type resource struct {
	kind string // URL segment and template prefix

	list func(ctx context.Context, plan int64) ([]any, error)
	get  func(ctx context.Context, plan, id int64) (any, error)
	// values returns the form's input values; item is nil for a new record.
	values func(lang i18n.Lang, item any) map[string]string
	// parse builds a record from the form; it does not set ID or PlanID.
	parse func(p *formParser, now domain.Month) (any, domain.Errors)
	save  func(ctx context.Context, plan, id int64, item any) error
	del   func(ctx context.Context, plan, id int64) error
	// total sums the section's headline amount (nil = no total shown).
	total func(items []any, now domain.Month, r domain.Resolved) domain.Cents
}

func itemID(item any) int64 {
	switch v := item.(type) {
	case domain.Person:
		return v.ID
	case domain.Account:
		return v.ID
	case domain.Income:
		return v.ID
	case domain.Expense:
		return v.ID
	case domain.Debt:
		return v.ID
	case domain.Milestone:
		return v.ID
	case domain.Prepayment:
		return v.ID
	}
	return 0
}

func itemOwner(item any) *int64 {
	switch v := item.(type) {
	case domain.Account:
		return v.OwnerID
	case domain.Income:
		return v.OwnerID
	case domain.Milestone:
		return v.PersonID
	}
	return nil
}

func toAny[T any](items []T, err error) ([]any, error) {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it
	}
	return out, err
}

func newResources(st *store.Store) map[string]*resource {
	rs := []*resource{
		{
			kind: "people",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListPeople(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetPerson(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				if item == nil {
					return map[string]string{"life_expectancy": "90"}
				}
				p := item.(domain.Person)
				return map[string]string{
					"name": p.Name, "birth": p.Birth.String(),
					"life_expectancy": strconv.Itoa(p.LifeExpectancy),
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Person{
					Name:           p.str("name"),
					Birth:          p.month("birth"),
					LifeExpectancy: p.integer("life_expectancy"),
				}
				p.merge(it.Validate(now))
				return it, p.errs
			},
			save: func(ctx context.Context, plan, id int64, item any) error {
				it := item.(domain.Person)
				it.ID, it.PlanID = id, plan
				_, err := st.SavePerson(ctx, it)
				return err
			},
			del: st.DeletePerson,
		},
		{
			kind: "milestones",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListMilestones(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetMilestone(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				v := map[string]string{"kind": string(domain.MilestoneAge), "age": "65",
					"multiple":   i18n.FormatInput(lang, domain.DefaultFIMultiple, 1),
					"fi_variant": string(domain.FIStandard), "coast_age": "65"}
				if item == nil {
					return v
				}
				m := item.(domain.Milestone)
				v["name"], v["kind"], v["date"] = m.Name, string(m.Kind), m.Date.String()
				switch m.Kind {
				case domain.MilestoneAge:
					v["person"], v["age"] = inOwner(m.PersonID), strconv.Itoa(m.Age)
				case domain.MilestoneFI:
					v["multiple"] = i18n.FormatInput(lang, m.FIMultiple, 1)
					v["fi_variant"] = string(m.FIVariant)
					if m.FIVariant.UsesAmount() {
						v["fi_amount"] = inMoney(lang, m.FIAmount)
					}
					if m.FIVariant == domain.FICoast {
						v["coast_person"], v["coast_age"] = inOwner(m.PersonID), strconv.Itoa(m.Age)
					}
				}
				return v
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Milestone{Name: p.str("name"), Kind: domain.MilestoneKind(p.str("kind"))}
				// Only the fields of the chosen kind are read, so hidden fields never cause errors.
				switch it.Kind {
				case domain.MilestoneAge:
					it.PersonID = p.owner("person")
					it.Age = p.integer("age")
				case domain.MilestoneDate:
					it.Date = p.month("date")
				case domain.MilestoneFI:
					it.FIMultiple = p.decimal("multiple")
					it.FIVariant = domain.FIVariant(p.str("fi_variant"))
					if it.FIVariant == "" {
						it.FIVariant = domain.FIStandard
					}
					if it.FIVariant.UsesAmount() {
						it.FIAmount = p.money("fi_amount", true)
					}
					if it.FIVariant == domain.FICoast {
						it.PersonID = p.owner("coast_person")
						it.Age = p.integer("coast_age")
					}
				}
				p.merge(it.Validate())
				return it, p.errs
			},
			save: func(ctx context.Context, plan, id int64, item any) error {
				it := item.(domain.Milestone)
				it.ID, it.PlanID = id, plan
				_, err := st.SaveMilestone(ctx, it)
				return err
			},
			del: st.DeleteMilestone,
		},
		{
			kind: "accounts",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListAccounts(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetAccount(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				if item == nil {
					return map[string]string{"kind": string(domain.AccountCash), "sale_share": inPercent(lang, 1)}
				}
				a := item.(domain.Account)
				v := map[string]string{
					"name": a.Name, "kind": string(a.Kind), "owner": inOwner(a.OwnerID),
					"balance": inMoney(lang, a.Balance), "contribution": inMoney(lang, a.MonthlyContribution),
					"return": inOptPercent(lang, a.AnnualReturn), "contrib_end_ms": inOwner(a.ContributionEndMS),
					"cost_basis": inOptMoney(lang, a.CostBasis), "sale_share": inPercent(lang, 1),
				}
				if s := a.Sale; s != nil {
					v["sale_enabled"], v["sale_ms"], v["sale_month"] = "on", inOwner(s.MS), s.Month.String()
					v["sale_share"], v["sale_cost"] = inPercent(lang, s.Share), inPercent(lang, s.CostRate)
					v["sale_destination"] = inOwner(s.DestinationID)
					if s.MainResidence {
						v["sale_main_residence"] = "on"
					}
				}
				return v
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Account{
					Name:                p.str("name"),
					Kind:                domain.AccountKind(p.str("kind")),
					OwnerID:             p.owner("owner"),
					Balance:             p.money("balance", false),
					MonthlyContribution: p.money("contribution", false),
					AnnualReturn:        p.optPercent("return"),
					ContributionEndMS:   p.owner("contrib_end_ms"),
					CostBasis:           p.optMoney("cost_basis"),
				}
				if !it.Kind.HasCostBasis() {
					it.CostBasis = nil
				}
				it.Sale = parseSale(p, it.Kind)
				p.merge(it.Validate())
				return it, p.errs
			},
			save: func(ctx context.Context, plan, id int64, item any) error {
				it := item.(domain.Account)
				it.ID, it.PlanID = id, plan
				_, err := st.SaveAccount(ctx, it)
				return err
			},
			del: st.DeleteAccount,
			total: func(items []any, _ domain.Month, _ domain.Resolved) domain.Cents {
				var t domain.Cents
				for _, it := range items {
					t += it.(domain.Account).Balance
				}
				return t
			},
		},
		{
			kind: "incomes",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListIncomes(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetIncome(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				if item == nil {
					return map[string]string{"kind": string(domain.IncomeSalary), "payments": "12"}
				}
				i := item.(domain.Income)
				return map[string]string{
					"name": i.Name, "kind": string(i.Kind), "owner": inOwner(i.OwnerID),
					"amount": inMoney(lang, i.Amount), "payments": strconv.Itoa(i.PaymentsPerYear),
					"growth": inOptPercent(lang, i.AnnualGrowth), "start": i.Start.String(), "end": i.End.String(),
					"start_ms": inOwner(i.StartMS), "end_ms": inOwner(i.EndMS),
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Income{
					Name:            p.str("name"),
					Kind:            domain.IncomeKind(p.str("kind")),
					OwnerID:         p.owner("owner"),
					Amount:          p.money("amount", true),
					PaymentsPerYear: p.integer("payments"),
					AnnualGrowth:    p.optPercent("growth"),
				}
				it.StartMS, it.Start = p.boundary("start_ms", "start")
				it.EndMS, it.End = p.boundary("end_ms", "end")
				p.merge(it.Validate())
				return it, p.errs
			},
			save: func(ctx context.Context, plan, id int64, item any) error {
				it := item.(domain.Income)
				it.ID, it.PlanID = id, plan
				_, err := st.SaveIncome(ctx, it)
				return err
			},
			del: st.DeleteIncome,
			total: func(items []any, now domain.Month, r domain.Resolved) domain.Cents {
				var t domain.Cents
				for _, it := range items {
					if i := it.(domain.Income); r.ActiveIn(i.Start, i.End, i.StartMS, i.EndMS, now) {
						t += i.MonthlyAverage()
					}
				}
				return t
			},
		},
		{
			kind: "expenses",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListExpenses(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetExpense(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				if item == nil {
					return map[string]string{"category": string(domain.ExpenseHousing), "payments": "12"}
				}
				e := item.(domain.Expense)
				return map[string]string{
					"name": e.Name, "category": string(e.Category),
					"amount": inMoney(lang, e.Amount), "payments": strconv.Itoa(e.PaymentsPerYear),
					"growth": inOptPercent(lang, e.AnnualGrowth), "start": e.Start.String(), "end": e.End.String(),
					"start_ms": inOwner(e.StartMS), "end_ms": inOwner(e.EndMS),
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Expense{
					Name:            p.str("name"),
					Category:        domain.ExpenseCategory(p.str("category")),
					Amount:          p.money("amount", true),
					PaymentsPerYear: p.integer("payments"),
					AnnualGrowth:    p.optPercent("growth"),
				}
				it.StartMS, it.Start = p.boundary("start_ms", "start")
				it.EndMS, it.End = p.boundary("end_ms", "end")
				p.merge(it.Validate())
				return it, p.errs
			},
			save: func(ctx context.Context, plan, id int64, item any) error {
				it := item.(domain.Expense)
				it.ID, it.PlanID = id, plan
				_, err := st.SaveExpense(ctx, it)
				return err
			},
			del: st.DeleteExpense,
			total: func(items []any, now domain.Month, r domain.Resolved) domain.Cents {
				var t domain.Cents
				for _, it := range items {
					if e := it.(domain.Expense); r.ActiveIn(e.Start, e.End, e.StartMS, e.EndMS, now) {
						t += e.MonthlyAverage()
					}
				}
				return t
			},
		},
		{
			kind: "debts",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListDebts(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetDebt(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				if item == nil {
					return map[string]string{"kind": string(domain.DebtMortgage)}
				}
				d := item.(domain.Debt)
				return map[string]string{
					"name": d.Name, "kind": string(d.Kind), "balance": inMoney(lang, d.Balance),
					"rate": inPercent(lang, d.AnnualRate), "payment": inMoney(lang, d.MonthlyPayment),
					"redirect": inOwner(d.RedirectAccountID),
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Debt{
					Name:              p.str("name"),
					Kind:              domain.DebtKind(p.str("kind")),
					Balance:           p.money("balance", true),
					AnnualRate:        p.percent("rate"),
					MonthlyPayment:    p.money("payment", true),
					RedirectAccountID: p.owner("redirect"),
				}
				p.merge(it.Validate())
				return it, p.errs
			},
			save: func(ctx context.Context, plan, id int64, item any) error {
				it := item.(domain.Debt)
				it.ID, it.PlanID = id, plan
				_, err := st.SaveDebt(ctx, it)
				return err
			},
			del: st.DeleteDebt,
			total: func(items []any, _ domain.Month, _ domain.Resolved) domain.Cents {
				var t domain.Cents
				for _, it := range items {
					t += it.(domain.Debt).Balance
				}
				return t
			},
		},
		{
			kind: "prepayments",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListPrepayments(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetPrepayment(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				if item == nil {
					return map[string]string{"frequency": string(domain.PrepayOnce), "mode": string(domain.ReduceTerm)}
				}
				p := item.(domain.Prepayment)
				return map[string]string{
					"debt": strconv.FormatInt(p.DebtID, 10), "amount": inMoney(lang, p.Amount),
					"frequency": string(p.Frequency), "mode": string(p.Mode), "fee": inPercent(lang, p.FeeRate),
					"start": p.Start.String(), "end": p.End.String(),
					"start_ms": inOwner(p.StartMS), "end_ms": inOwner(p.EndMS),
					"redirect": inOwner(p.RedirectAccountID),
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Prepayment{
					RedirectAccountID: p.owner("redirect"),
					Amount:            p.money("amount", true),
					Frequency:         domain.PrepaymentFrequency(p.str("frequency")),
					Mode:              domain.PrepaymentMode(p.str("mode")),
				}
				if id := p.owner("debt"); id != nil {
					it.DebtID = *id
				}
				if fee := p.optPercent("fee"); fee != nil {
					it.FeeRate = *fee
				}
				it.StartMS, it.Start = p.boundary("start_ms", "start")
				if it.Frequency != domain.PrepayOnce {
					it.EndMS, it.End = p.boundary("end_ms", "end")
				}
				p.merge(it.Validate())
				return it, p.errs
			},
			save: func(ctx context.Context, plan, id int64, item any) error {
				it := item.(domain.Prepayment)
				it.ID, it.PlanID = id, plan
				_, err := st.SavePrepayment(ctx, it)
				return err
			},
			del: st.DeletePrepayment,
		},
	}
	m := make(map[string]*resource, len(rs))
	for _, r := range rs {
		m[r.kind] = r
	}
	return m
}

// formValuesFromRequest returns the submitted values so a form with errors
// re-renders exactly what the user typed.
func formValuesFromRequest(v url.Values) map[string]string {
	out := make(map[string]string, len(v))
	for k := range v {
		out[k] = v.Get(k)
	}
	return out
}
