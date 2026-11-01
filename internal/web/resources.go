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
	total func(items []any, now domain.Month) domain.Cents
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
	}
	return 0
}

func itemOwner(item any) *int64 {
	switch v := item.(type) {
	case domain.Account:
		return v.OwnerID
	case domain.Income:
		return v.OwnerID
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
			kind: "accounts",
			list: func(ctx context.Context, plan int64) ([]any, error) { return toAny(st.ListAccounts(ctx, plan)) },
			get: func(ctx context.Context, plan, id int64) (any, error) {
				return st.GetAccount(ctx, plan, id)
			},
			values: func(lang i18n.Lang, item any) map[string]string {
				if item == nil {
					return map[string]string{"kind": string(domain.AccountCash)}
				}
				a := item.(domain.Account)
				return map[string]string{
					"name": a.Name, "kind": string(a.Kind), "owner": inOwner(a.OwnerID),
					"balance": inMoney(lang, a.Balance), "contribution": inMoney(lang, a.MonthlyContribution),
					"return": inOptPercent(lang, a.AnnualReturn),
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Account{
					Name:                p.str("name"),
					Kind:                domain.AccountKind(p.str("kind")),
					OwnerID:             p.owner("owner"),
					Balance:             p.money("balance", false),
					MonthlyContribution: p.money("contribution", false),
					AnnualReturn:        p.optPercent("return"),
				}
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
			total: func(items []any, _ domain.Month) domain.Cents {
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
					Start:           p.month("start"),
					End:             p.month("end"),
				}
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
			total: func(items []any, now domain.Month) domain.Cents {
				var t domain.Cents
				for _, it := range items {
					if i := it.(domain.Income); domain.Active(i.Start, i.End, now) {
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
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Expense{
					Name:            p.str("name"),
					Category:        domain.ExpenseCategory(p.str("category")),
					Amount:          p.money("amount", true),
					PaymentsPerYear: p.integer("payments"),
					AnnualGrowth:    p.optPercent("growth"),
					Start:           p.month("start"),
					End:             p.month("end"),
				}
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
			total: func(items []any, now domain.Month) domain.Cents {
				var t domain.Cents
				for _, it := range items {
					if e := it.(domain.Expense); domain.Active(e.Start, e.End, now) {
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
				}
			},
			parse: func(p *formParser, now domain.Month) (any, domain.Errors) {
				it := domain.Debt{
					Name:           p.str("name"),
					Kind:           domain.DebtKind(p.str("kind")),
					Balance:        p.money("balance", true),
					AnnualRate:     p.percent("rate"),
					MonthlyPayment: p.money("payment", true),
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
			total: func(items []any, _ domain.Month) domain.Cents {
				var t domain.Cents
				for _, it := range items {
					t += it.(domain.Debt).Balance
				}
				return t
			},
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
