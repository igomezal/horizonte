package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/igomezal/horizonte/internal/domain"
)

// ErrNotFound is returned when a record does not exist in the given plan.
var ErrNotFound = errors.New("not found")

const settingCurrentPlan = "current_plan_id"

// CurrentPlan returns the active plan's ID, creating a default plan on first use.
func (s *Store) CurrentPlan(ctx context.Context, defaultName string) (int64, error) {
	if id, ok, err := s.ActivePlanID(ctx); err != nil || ok {
		return id, err
	}
	res, err := s.q.ExecContext(ctx, `INSERT INTO plans (name) VALUES (?)`, defaultName)
	if err != nil {
		return 0, fmt.Errorf("store: create default plan: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, s.SetSetting(ctx, settingCurrentPlan, strconv.FormatInt(id, 10))
}

// ActivePlanID returns the saved active plan, with ok=false when none is saved
// or it no longer exists. Unlike CurrentPlan it never writes.
func (s *Store) ActivePlanID(ctx context.Context) (id int64, ok bool, err error) {
	v, err := s.GetSetting(ctx, settingCurrentPlan, "")
	if err != nil {
		return 0, false, err
	}
	id, perr := strconv.ParseInt(v, 10, 64)
	if perr != nil {
		return 0, false, nil
	}
	var exists int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans WHERE id = ?`, id).Scan(&exists); err != nil {
		return 0, false, err
	}
	if exists != 1 {
		return 0, false, nil
	}
	return id, true, nil
}

// ---------- helpers for nullable columns ----------

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullCents(p *domain.Cents) any {
	if p == nil {
		return nil
	}
	return int64(*p)
}

func nullMonth(m domain.Month) any {
	if m.IsZero() {
		return nil
	}
	return m.String()
}

func intPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func centsPtr(n sql.NullInt64) *domain.Cents {
	if !n.Valid {
		return nil
	}
	v := domain.Cents(n.Int64)
	return &v
}

func scanMonth(n sql.NullString) (domain.Month, error) {
	if !n.Valid {
		return domain.Month{}, nil
	}
	return domain.ParseMonth(n.String)
}

// exec runs a write scoped to a plan and maps "no rows affected" to ErrNotFound.
func (s *Store) execOne(ctx context.Context, query string, args ...any) error {
	res, err := s.q.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) insert(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := s.q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// checkOwner verifies that ownerID (if set) is a person in the plan.
func (s *Store) checkOwner(ctx context.Context, planID int64, ownerID *int64) error {
	if ownerID == nil {
		return nil
	}
	var n int
	if err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM people WHERE id = ? AND plan_id = ?`, *ownerID, planID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- People ----------

const personCols = `id, plan_id, name, birth_month, life_expectancy`

func scanPerson(sc interface{ Scan(...any) error }) (domain.Person, error) {
	var p domain.Person
	var birth string
	if err := sc.Scan(&p.ID, &p.PlanID, &p.Name, &birth, &p.LifeExpectancy); err != nil {
		return p, err
	}
	var err error
	p.Birth, err = domain.ParseMonth(birth)
	return p, err
}

// ListPeople returns the plan's people, oldest record first.
func (s *Store) ListPeople(ctx context.Context, planID int64) ([]domain.Person, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+personCols+` FROM people WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPerson returns one person of the plan.
func (s *Store) GetPerson(ctx context.Context, planID, id int64) (domain.Person, error) {
	p, err := scanPerson(s.q.QueryRowContext(ctx,
		`SELECT `+personCols+` FROM people WHERE id = ? AND plan_id = ?`, id, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// SavePerson inserts (ID 0) or updates a person and returns its ID.
func (s *Store) SavePerson(ctx context.Context, p domain.Person) (int64, error) {
	if p.ID == 0 {
		return s.insert(ctx, `INSERT INTO people (plan_id, name, birth_month, life_expectancy) VALUES (?, ?, ?, ?)`,
			p.PlanID, p.Name, p.Birth.String(), p.LifeExpectancy)
	}
	return p.ID, s.execOne(ctx, `UPDATE people SET name = ?, birth_month = ?, life_expectancy = ? WHERE id = ? AND plan_id = ?`,
		p.Name, p.Birth.String(), p.LifeExpectancy, p.ID, p.PlanID)
}

// DeletePerson removes a person; their accounts and incomes become joint.
func (s *Store) DeletePerson(ctx context.Context, planID, id int64) error {
	return s.execOne(ctx, `DELETE FROM people WHERE id = ? AND plan_id = ?`, id, planID)
}

// ---------- Accounts ----------

const accountCols = `id, plan_id, name, kind, owner_id, balance_cents, monthly_contribution_cents, annual_return,
	contribution_end_milestone_id, cost_basis_cents,
	sale_month, sale_milestone_id, sale_share, sale_cost_rate, sale_destination_account_id, sale_main_residence`

func scanAccount(sc interface{ Scan(...any) error }) (domain.Account, error) {
	var a domain.Account
	var owner, contribEnd, costBasis, saleMS, saleDest sql.NullInt64
	var ret sql.NullFloat64
	var saleMonth sql.NullString
	var sale domain.PropertySale
	err := sc.Scan(&a.ID, &a.PlanID, &a.Name, &a.Kind, &owner, &a.Balance, &a.MonthlyContribution, &ret, &contribEnd, &costBasis,
		&saleMonth, &saleMS, &sale.Share, &sale.CostRate, &saleDest, &sale.MainResidence)
	a.OwnerID, a.AnnualReturn, a.ContributionEndMS = intPtr(owner), floatPtr(ret), intPtr(contribEnd)
	a.CostBasis = centsPtr(costBasis)
	if err != nil || (!saleMonth.Valid && !saleMS.Valid) {
		return a, err
	}
	if sale.Month, err = scanMonth(saleMonth); err != nil {
		return a, err
	}
	sale.MS, sale.DestinationID = intPtr(saleMS), intPtr(saleDest)
	a.Sale = &sale
	return a, nil
}

// saleArgs are the values of the sale columns.
func saleArgs(a domain.Account) []any {
	s := a.Sale
	if s == nil || a.Kind != domain.AccountRealEstate {
		return []any{nil, nil, 1.0, 0.0, nil, false}
	}
	return []any{nullMonth(s.Month), nullInt(s.MS), s.Share, s.CostRate, nullInt(s.DestinationID), s.MainResidence}
}

// checkSale verifies that a sale's links belong to the plan and that the
// destination can hold the money.
func (s *Store) checkSale(ctx context.Context, planID int64, sale *domain.PropertySale) error {
	if sale == nil {
		return nil
	}
	if err := s.checkMilestones(ctx, planID, sale.MS); err != nil {
		return err
	}
	if sale.DestinationID == nil {
		return nil
	}
	dest, err := s.GetAccount(ctx, planID, *sale.DestinationID)
	if err != nil {
		return err
	}
	if !dest.Kind.Liquid() {
		return ErrNotFound
	}
	return nil
}

// ListAccounts returns the plan's accounts.
func (s *Store) ListAccounts(ctx context.Context, planID int64) ([]domain.Account, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccount returns one account of the plan.
func (s *Store) GetAccount(ctx context.Context, planID, id int64) (domain.Account, error) {
	a, err := scanAccount(s.q.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE id = ? AND plan_id = ?`, id, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// SaveAccount inserts (ID 0) or updates an account and returns its ID.
func (s *Store) SaveAccount(ctx context.Context, a domain.Account) (int64, error) {
	if err := s.checkOwner(ctx, a.PlanID, a.OwnerID); err != nil {
		return 0, err
	}
	if err := s.checkMilestones(ctx, a.PlanID, a.ContributionEndMS); err != nil {
		return 0, err
	}
	if err := s.checkSale(ctx, a.PlanID, a.Sale); err != nil {
		return 0, err
	}
	if a.ID == 0 {
		args := append([]any{a.PlanID, a.Name, a.Kind, nullInt(a.OwnerID), a.Balance, a.MonthlyContribution, nullFloat(a.AnnualReturn),
			nullInt(a.ContributionEndMS), nullCents(a.CostBasis)}, saleArgs(a)...)
		return s.insert(ctx, `INSERT INTO accounts (plan_id, name, kind, owner_id, balance_cents, monthly_contribution_cents,
			annual_return, contribution_end_milestone_id, cost_basis_cents,
			sale_month, sale_milestone_id, sale_share, sale_cost_rate, sale_destination_account_id, sale_main_residence)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args...)
	}
	args := append([]any{a.Name, a.Kind, nullInt(a.OwnerID), a.Balance, a.MonthlyContribution, nullFloat(a.AnnualReturn),
		nullInt(a.ContributionEndMS), nullCents(a.CostBasis)}, saleArgs(a)...)
	args = append(args, a.ID, a.PlanID)
	return a.ID, s.execOne(ctx, `UPDATE accounts SET name = ?, kind = ?, owner_id = ?, balance_cents = ?,
		monthly_contribution_cents = ?, annual_return = ?, contribution_end_milestone_id = ?, cost_basis_cents = ?,
		sale_month = ?, sale_milestone_id = ?, sale_share = ?, sale_cost_rate = ?, sale_destination_account_id = ?,
		sale_main_residence = ?
		WHERE id = ? AND plan_id = ?`, args...)
}

// setSaleDestination links a sale to the account receiving the money.
func (s *Store) setSaleDestination(ctx context.Context, planID, id, destination int64) error {
	return s.execOne(ctx, `UPDATE accounts SET sale_destination_account_id = ? WHERE id = ? AND plan_id = ?`, destination, id, planID)
}

// DeleteAccount removes an account.
func (s *Store) DeleteAccount(ctx context.Context, planID, id int64) error {
	return s.execOne(ctx, `DELETE FROM accounts WHERE id = ? AND plan_id = ?`, id, planID)
}

// ---------- Incomes ----------

const incomeCols = `id, plan_id, name, kind, owner_id, amount_cents, payments_per_year, annual_growth, start_month, end_month,
	start_milestone_id, end_milestone_id`

func scanIncome(sc interface{ Scan(...any) error }) (domain.Income, error) {
	var i domain.Income
	var owner, startMS, endMS sql.NullInt64
	var growth sql.NullFloat64
	var start, end sql.NullString
	if err := sc.Scan(&i.ID, &i.PlanID, &i.Name, &i.Kind, &owner, &i.Amount, &i.PaymentsPerYear, &growth, &start, &end,
		&startMS, &endMS); err != nil {
		return i, err
	}
	i.OwnerID, i.AnnualGrowth = intPtr(owner), floatPtr(growth)
	i.StartMS, i.EndMS = intPtr(startMS), intPtr(endMS)
	var err error
	if i.Start, err = scanMonth(start); err != nil {
		return i, err
	}
	i.End, err = scanMonth(end)
	return i, err
}

// ListIncomes returns the plan's income streams.
func (s *Store) ListIncomes(ctx context.Context, planID int64) ([]domain.Income, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+incomeCols+` FROM incomes WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Income
	for rows.Next() {
		i, err := scanIncome(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// GetIncome returns one income stream of the plan.
func (s *Store) GetIncome(ctx context.Context, planID, id int64) (domain.Income, error) {
	i, err := scanIncome(s.q.QueryRowContext(ctx,
		`SELECT `+incomeCols+` FROM incomes WHERE id = ? AND plan_id = ?`, id, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

// SaveIncome inserts (ID 0) or updates an income stream and returns its ID.
func (s *Store) SaveIncome(ctx context.Context, i domain.Income) (int64, error) {
	if err := s.checkOwner(ctx, i.PlanID, i.OwnerID); err != nil {
		return 0, err
	}
	if err := s.checkMilestones(ctx, i.PlanID, i.StartMS, i.EndMS); err != nil {
		return 0, err
	}
	if i.ID == 0 {
		return s.insert(ctx, `INSERT INTO incomes (plan_id, name, kind, owner_id, amount_cents, payments_per_year, annual_growth,
			start_month, end_month, start_milestone_id, end_milestone_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			i.PlanID, i.Name, i.Kind, nullInt(i.OwnerID), i.Amount, i.PaymentsPerYear, nullFloat(i.AnnualGrowth),
			nullMonth(i.Start), nullMonth(i.End), nullInt(i.StartMS), nullInt(i.EndMS))
	}
	return i.ID, s.execOne(ctx, `UPDATE incomes SET name = ?, kind = ?, owner_id = ?, amount_cents = ?, payments_per_year = ?,
		annual_growth = ?, start_month = ?, end_month = ?, start_milestone_id = ?, end_milestone_id = ?
		WHERE id = ? AND plan_id = ?`,
		i.Name, i.Kind, nullInt(i.OwnerID), i.Amount, i.PaymentsPerYear, nullFloat(i.AnnualGrowth),
		nullMonth(i.Start), nullMonth(i.End), nullInt(i.StartMS), nullInt(i.EndMS), i.ID, i.PlanID)
}

// DeleteIncome removes an income stream.
func (s *Store) DeleteIncome(ctx context.Context, planID, id int64) error {
	return s.execOne(ctx, `DELETE FROM incomes WHERE id = ? AND plan_id = ?`, id, planID)
}

// ---------- Expenses ----------

const expenseCols = `id, plan_id, name, category, amount_cents, payments_per_year, annual_growth, start_month, end_month,
	start_milestone_id, end_milestone_id`

func scanExpense(sc interface{ Scan(...any) error }) (domain.Expense, error) {
	var e domain.Expense
	var growth sql.NullFloat64
	var start, end sql.NullString
	var startMS, endMS sql.NullInt64
	if err := sc.Scan(&e.ID, &e.PlanID, &e.Name, &e.Category, &e.Amount, &e.PaymentsPerYear, &growth, &start, &end,
		&startMS, &endMS); err != nil {
		return e, err
	}
	e.AnnualGrowth = floatPtr(growth)
	e.StartMS, e.EndMS = intPtr(startMS), intPtr(endMS)
	var err error
	if e.Start, err = scanMonth(start); err != nil {
		return e, err
	}
	e.End, err = scanMonth(end)
	return e, err
}

// ListExpenses returns the plan's expenses.
func (s *Store) ListExpenses(ctx context.Context, planID int64) ([]domain.Expense, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+expenseCols+` FROM expenses WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Expense
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetExpense returns one expense of the plan.
func (s *Store) GetExpense(ctx context.Context, planID, id int64) (domain.Expense, error) {
	e, err := scanExpense(s.q.QueryRowContext(ctx,
		`SELECT `+expenseCols+` FROM expenses WHERE id = ? AND plan_id = ?`, id, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// SaveExpense inserts (ID 0) or updates an expense and returns its ID.
func (s *Store) SaveExpense(ctx context.Context, e domain.Expense) (int64, error) {
	if err := s.checkMilestones(ctx, e.PlanID, e.StartMS, e.EndMS); err != nil {
		return 0, err
	}
	if e.ID == 0 {
		return s.insert(ctx, `INSERT INTO expenses (plan_id, name, category, amount_cents, payments_per_year, annual_growth,
			start_month, end_month, start_milestone_id, end_milestone_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.PlanID, e.Name, e.Category, e.Amount, e.PaymentsPerYear, nullFloat(e.AnnualGrowth),
			nullMonth(e.Start), nullMonth(e.End), nullInt(e.StartMS), nullInt(e.EndMS))
	}
	return e.ID, s.execOne(ctx, `UPDATE expenses SET name = ?, category = ?, amount_cents = ?, payments_per_year = ?,
		annual_growth = ?, start_month = ?, end_month = ?, start_milestone_id = ?, end_milestone_id = ?
		WHERE id = ? AND plan_id = ?`,
		e.Name, e.Category, e.Amount, e.PaymentsPerYear, nullFloat(e.AnnualGrowth),
		nullMonth(e.Start), nullMonth(e.End), nullInt(e.StartMS), nullInt(e.EndMS), e.ID, e.PlanID)
}

// DeleteExpense removes an expense.
func (s *Store) DeleteExpense(ctx context.Context, planID, id int64) error {
	return s.execOne(ctx, `DELETE FROM expenses WHERE id = ? AND plan_id = ?`, id, planID)
}

// ---------- Debts ----------

const debtCols = `id, plan_id, name, kind, balance_cents, annual_rate, monthly_payment_cents, redirect_account_id`

func scanDebt(sc interface{ Scan(...any) error }) (domain.Debt, error) {
	var d domain.Debt
	var redirect sql.NullInt64
	err := sc.Scan(&d.ID, &d.PlanID, &d.Name, &d.Kind, &d.Balance, &d.AnnualRate, &d.MonthlyPayment, &redirect)
	d.RedirectAccountID = intPtr(redirect)
	return d, err
}

// checkAccount verifies that an optional account ID belongs to the plan.
func (s *Store) checkAccount(ctx context.Context, planID int64, id *int64) error {
	if id == nil {
		return nil
	}
	_, err := s.GetAccount(ctx, planID, *id)
	return err
}

// ListDebts returns the plan's debts.
func (s *Store) ListDebts(ctx context.Context, planID int64) ([]domain.Debt, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+debtCols+` FROM debts WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Debt
	for rows.Next() {
		d, err := scanDebt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDebt returns one debt of the plan.
func (s *Store) GetDebt(ctx context.Context, planID, id int64) (domain.Debt, error) {
	d, err := scanDebt(s.q.QueryRowContext(ctx,
		`SELECT `+debtCols+` FROM debts WHERE id = ? AND plan_id = ?`, id, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// SaveDebt inserts (ID 0) or updates a debt and returns its ID.
func (s *Store) SaveDebt(ctx context.Context, d domain.Debt) (int64, error) {
	if err := s.checkAccount(ctx, d.PlanID, d.RedirectAccountID); err != nil {
		return 0, err
	}
	if d.ID == 0 {
		return s.insert(ctx, `INSERT INTO debts (plan_id, name, kind, balance_cents, annual_rate, monthly_payment_cents,
			redirect_account_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			d.PlanID, d.Name, d.Kind, d.Balance, d.AnnualRate, d.MonthlyPayment, nullInt(d.RedirectAccountID))
	}
	return d.ID, s.execOne(ctx, `UPDATE debts SET name = ?, kind = ?, balance_cents = ?, annual_rate = ?,
		monthly_payment_cents = ?, redirect_account_id = ? WHERE id = ? AND plan_id = ?`,
		d.Name, d.Kind, d.Balance, d.AnnualRate, d.MonthlyPayment, nullInt(d.RedirectAccountID), d.ID, d.PlanID)
}

// DeleteDebt removes a debt.
func (s *Store) DeleteDebt(ctx context.Context, planID, id int64) error {
	return s.execOne(ctx, `DELETE FROM debts WHERE id = ? AND plan_id = ?`, id, planID)
}
