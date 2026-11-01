package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// Plan is a plan or scenario (a plan copied from another one).
type Plan struct {
	ID       int64
	Name     string
	ParentID *int64
}

// ErrLastPlan is returned when deleting the only remaining plan.
var ErrLastPlan = errors.New("store: cannot delete the last plan")

// ListPlans returns every plan, oldest first.
func (s *Store) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, name, parent_id FROM plans ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		var p Plan
		var parent sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &parent); err != nil {
			return nil, err
		}
		p.ParentID = intPtr(parent)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlan returns one plan.
func (s *Store) GetPlan(ctx context.Context, id int64) (Plan, error) {
	var p Plan
	var parent sql.NullInt64
	err := s.q.QueryRowContext(ctx, `SELECT id, name, parent_id FROM plans WHERE id = ?`, id).Scan(&p.ID, &p.Name, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.ParentID = intPtr(parent)
	return p, err
}

// SetCurrentPlan makes id the active plan.
func (s *Store) SetCurrentPlan(ctx context.Context, id int64) error {
	if _, err := s.GetPlan(ctx, id); err != nil {
		return err
	}
	return s.SetSetting(ctx, settingCurrentPlan, strconv.FormatInt(id, 10))
}

// RenamePlan changes a plan's name.
func (s *Store) RenamePlan(ctx context.Context, id int64, name string) error {
	return s.execOne(ctx, `UPDATE plans SET name = ?, updated_at = datetime('now') WHERE id = ?`,
		strings.TrimSpace(name), id)
}

// DeletePlan removes a plan and everything in it. The last plan cannot be
// deleted; if the active plan is deleted, the oldest remaining one becomes active.
func (s *Store) DeletePlan(ctx context.Context, id int64) error {
	return s.InTx(ctx, func(tx *Store) error {
		plans, err := tx.ListPlans(ctx)
		if err != nil {
			return err
		}
		if len(plans) <= 1 {
			return ErrLastPlan
		}
		if err := tx.execOne(ctx, `DELETE FROM plans WHERE id = ?`, id); err != nil {
			return err
		}
		cur, err := tx.GetSetting(ctx, settingCurrentPlan, "")
		if err != nil {
			return err
		}
		if cur == strconv.FormatInt(id, 10) {
			for _, p := range plans {
				if p.ID != id {
					return tx.SetSetting(ctx, settingCurrentPlan, strconv.FormatInt(p.ID, 10))
				}
			}
		}
		return nil
	})
}

// remap translates an optional ID through an old→new map.
func remap(id *int64, m map[int64]int64) *int64 {
	if id == nil {
		return nil
	}
	if v, ok := m[*id]; ok {
		return &v
	}
	return nil
}

// CopyPlan creates a new plan named name with a full copy of src: people,
// milestones, accounts, incomes, expenses, debts, early repayments and
// assumptions, with every internal link pointing at the copies. It runs in
// one transaction, so a failure leaves nothing half-copied.
func (s *Store) CopyPlan(ctx context.Context, src int64, name string) (int64, error) {
	var newID int64
	err := s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.GetPlan(ctx, src); err != nil {
			return err
		}
		id, err := tx.insert(ctx, `INSERT INTO plans (name, parent_id) VALUES (?, ?)`, strings.TrimSpace(name), src)
		if err != nil {
			return err
		}
		newID = id

		people, err := tx.ListPeople(ctx, src)
		if err != nil {
			return err
		}
		personMap := map[int64]int64{}
		for _, p := range people {
			old := p.ID
			p.ID, p.PlanID = 0, id
			if personMap[old], err = tx.SavePerson(ctx, p); err != nil {
				return err
			}
		}

		milestones, err := tx.ListMilestones(ctx, src)
		if err != nil {
			return err
		}
		msMap := map[int64]int64{}
		for _, m := range milestones {
			old := m.ID
			m.ID, m.PlanID, m.PersonID = 0, id, remap(m.PersonID, personMap)
			if msMap[old], err = tx.SaveMilestone(ctx, m); err != nil {
				return err
			}
		}

		accounts, err := tx.ListAccounts(ctx, src)
		if err != nil {
			return err
		}
		accMap := map[int64]int64{}
		for _, a := range accounts {
			old := a.ID
			a.ID, a.PlanID = 0, id
			a.OwnerID, a.ContributionEndMS = remap(a.OwnerID, personMap), remap(a.ContributionEndMS, msMap)
			if accMap[old], err = tx.SaveAccount(ctx, a); err != nil {
				return err
			}
		}

		incomes, err := tx.ListIncomes(ctx, src)
		if err != nil {
			return err
		}
		for _, i := range incomes {
			i.ID, i.PlanID = 0, id
			i.OwnerID, i.StartMS, i.EndMS = remap(i.OwnerID, personMap), remap(i.StartMS, msMap), remap(i.EndMS, msMap)
			if _, err := tx.SaveIncome(ctx, i); err != nil {
				return err
			}
		}

		expenses, err := tx.ListExpenses(ctx, src)
		if err != nil {
			return err
		}
		for _, e := range expenses {
			e.ID, e.PlanID = 0, id
			e.StartMS, e.EndMS = remap(e.StartMS, msMap), remap(e.EndMS, msMap)
			if _, err := tx.SaveExpense(ctx, e); err != nil {
				return err
			}
		}

		debts, err := tx.ListDebts(ctx, src)
		if err != nil {
			return err
		}
		debtMap := map[int64]int64{}
		for _, d := range debts {
			old := d.ID
			d.ID, d.PlanID, d.RedirectAccountID = 0, id, remap(d.RedirectAccountID, accMap)
			if debtMap[old], err = tx.SaveDebt(ctx, d); err != nil {
				return err
			}
		}

		pps, err := tx.ListPrepayments(ctx, src)
		if err != nil {
			return err
		}
		for _, p := range pps {
			p.ID, p.PlanID, p.DebtID = 0, id, debtMap[p.DebtID]
			p.StartMS, p.EndMS = remap(p.StartMS, msMap), remap(p.EndMS, msMap)
			p.RedirectAccountID = remap(p.RedirectAccountID, accMap)
			if _, err := tx.SavePrepayment(ctx, p); err != nil {
				return err
			}
		}

		a, err := tx.GetAssumptions(ctx, src)
		if err != nil {
			return err
		}
		a.SurplusAccountID = remap(a.SurplusAccountID, accMap)
		return tx.SaveAssumptions(ctx, id, a)
	})
	return newID, err
}

// PlanCounts reports how many records a plan has, for tests and summaries.
func (s *Store) PlanCounts(ctx context.Context, planID int64) (map[string]int, error) {
	out := map[string]int{}
	for _, table := range []string{"people", "milestones", "accounts", "incomes", "expenses", "debts", "debt_prepayments"} {
		var n int
		if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE plan_id = ?`, planID).Scan(&n); err != nil {
			return nil, err
		}
		out[table] = n
	}
	return out, nil
}
