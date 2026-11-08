package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
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

// Snapshot is everything a plan holds. IDs are the ones the records had
// when it was taken; links between records use them.
type Snapshot struct {
	People      []domain.Person
	Milestones  []domain.Milestone
	Accounts    []domain.Account
	Incomes     []domain.Income
	Expenses    []domain.Expense
	Debts       []domain.Debt
	Prepayments []domain.Prepayment
	Assumptions engine.Assumptions
}

// ErrBadLink is returned when a snapshot record points at a record that is
// not in the snapshot and the link cannot be dropped.
var ErrBadLink = errors.New("store: snapshot record links to a missing record")

// LoadSnapshot reads every record of a plan.
func (s *Store) LoadSnapshot(ctx context.Context, planID int64) (Snapshot, error) {
	var snap Snapshot
	if _, err := s.GetPlan(ctx, planID); err != nil {
		return snap, err
	}
	var err error
	if snap.People, err = s.ListPeople(ctx, planID); err != nil {
		return snap, err
	}
	if snap.Milestones, err = s.ListMilestones(ctx, planID); err != nil {
		return snap, err
	}
	if snap.Accounts, err = s.ListAccounts(ctx, planID); err != nil {
		return snap, err
	}
	if snap.Incomes, err = s.ListIncomes(ctx, planID); err != nil {
		return snap, err
	}
	if snap.Expenses, err = s.ListExpenses(ctx, planID); err != nil {
		return snap, err
	}
	if snap.Debts, err = s.ListDebts(ctx, planID); err != nil {
		return snap, err
	}
	if snap.Prepayments, err = s.ListPrepayments(ctx, planID); err != nil {
		return snap, err
	}
	snap.Assumptions, err = s.GetAssumptions(ctx, planID)
	return snap, err
}

// CreatePlanFromSnapshot creates a new plan named name (a copy of parent when
// not nil) holding the snapshot's records, with every internal link pointing
// at the new records. Optional links to records missing from the snapshot are
// dropped; an early repayment of a missing debt is an error. It runs in one
// transaction, so a failure leaves nothing half-created.
func (s *Store) CreatePlanFromSnapshot(ctx context.Context, name string, parent *int64, snap Snapshot) (int64, error) {
	var newID int64
	err := s.InTx(ctx, func(tx *Store) error {
		id, err := tx.insert(ctx, `INSERT INTO plans (name, parent_id) VALUES (?, ?)`, strings.TrimSpace(name), nullInt(parent))
		if err != nil {
			return err
		}
		newID = id

		personMap := map[int64]int64{}
		for _, p := range snap.People {
			old := p.ID
			p.ID, p.PlanID = 0, id
			if personMap[old], err = tx.SavePerson(ctx, p); err != nil {
				return err
			}
		}
		msMap := map[int64]int64{}
		for _, m := range snap.Milestones {
			old := m.ID
			m.ID, m.PlanID, m.PersonID = 0, id, remap(m.PersonID, personMap)
			if msMap[old], err = tx.SaveMilestone(ctx, m); err != nil {
				return err
			}
		}
		accMap := map[int64]int64{}
		for _, a := range snap.Accounts {
			old := a.ID
			a.ID, a.PlanID = 0, id
			a.OwnerID, a.ContributionEndMS = remap(a.OwnerID, personMap), remap(a.ContributionEndMS, msMap)
			if a.Sale != nil {
				sale := *a.Sale
				// The destination may not exist yet: linked below.
				sale.MS, sale.DestinationID = remap(sale.MS, msMap), nil
				a.Sale = &sale
			}
			if accMap[old], err = tx.SaveAccount(ctx, a); err != nil {
				return err
			}
		}
		for _, a := range snap.Accounts {
			if a.Sale == nil {
				continue
			}
			if dest := remap(a.Sale.DestinationID, accMap); dest != nil {
				if err := tx.setSaleDestination(ctx, id, accMap[a.ID], *dest); err != nil {
					return err
				}
			}
		}
		for _, i := range snap.Incomes {
			i.ID, i.PlanID = 0, id
			i.OwnerID, i.StartMS, i.EndMS = remap(i.OwnerID, personMap), remap(i.StartMS, msMap), remap(i.EndMS, msMap)
			if _, err := tx.SaveIncome(ctx, i); err != nil {
				return err
			}
		}
		for _, e := range snap.Expenses {
			e.ID, e.PlanID = 0, id
			e.StartMS, e.EndMS = remap(e.StartMS, msMap), remap(e.EndMS, msMap)
			if _, err := tx.SaveExpense(ctx, e); err != nil {
				return err
			}
		}
		debtMap := map[int64]int64{}
		for _, d := range snap.Debts {
			old := d.ID
			d.ID, d.PlanID, d.RedirectAccountID = 0, id, remap(d.RedirectAccountID, accMap)
			if debtMap[old], err = tx.SaveDebt(ctx, d); err != nil {
				return err
			}
		}
		for _, p := range snap.Prepayments {
			debt, ok := debtMap[p.DebtID]
			if !ok {
				return ErrBadLink
			}
			p.ID, p.PlanID, p.DebtID = 0, id, debt
			p.StartMS, p.EndMS = remap(p.StartMS, msMap), remap(p.EndMS, msMap)
			p.RedirectAccountID = remap(p.RedirectAccountID, accMap)
			if _, err := tx.SavePrepayment(ctx, p); err != nil {
				return err
			}
		}

		a := snap.Assumptions
		a.SurplusAccountID = remap(a.SurplusAccountID, accMap)
		a.Withdrawal.StartMS = remap(a.Withdrawal.StartMS, msMap)
		a.PensionAvailableMS = remap(a.PensionAvailableMS, msMap)
		return tx.SaveAssumptions(ctx, id, a)
	})
	return newID, err
}

// CopyPlan creates a new plan named name with a full copy of src.
func (s *Store) CopyPlan(ctx context.Context, src int64, name string) (int64, error) {
	snap, err := s.LoadSnapshot(ctx, src)
	if err != nil {
		return 0, err
	}
	return s.CreatePlanFromSnapshot(ctx, name, &src, snap)
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
