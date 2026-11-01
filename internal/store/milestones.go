package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/igomezal/horizonte/internal/domain"
)

const milestoneCols = `id, plan_id, name, kind, person_id, age, date_month, fi_multiple, fi_variant, fi_amount_cents`

func scanMilestone(sc interface{ Scan(...any) error }) (domain.Milestone, error) {
	var m domain.Milestone
	var person sql.NullInt64
	var date sql.NullString
	if err := sc.Scan(&m.ID, &m.PlanID, &m.Name, &m.Kind, &person, &m.Age, &date, &m.FIMultiple,
		&m.FIVariant, &m.FIAmount); err != nil {
		return m, err
	}
	m.PersonID = intPtr(person)
	var err error
	m.Date, err = scanMonth(date)
	return m, err
}

// ListMilestones returns the plan's milestones in creation order.
func (s *Store) ListMilestones(ctx context.Context, planID int64) ([]domain.Milestone, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+milestoneCols+` FROM milestones WHERE plan_id = ? ORDER BY id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Milestone
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMilestone returns one milestone of the plan.
func (s *Store) GetMilestone(ctx context.Context, planID, id int64) (domain.Milestone, error) {
	m, err := scanMilestone(s.DB.QueryRowContext(ctx,
		`SELECT `+milestoneCols+` FROM milestones WHERE id = ? AND plan_id = ?`, id, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// SaveMilestone inserts (ID 0) or updates a milestone and returns its ID.
// Only the fields of the milestone's kind are stored.
func (s *Store) SaveMilestone(ctx context.Context, m domain.Milestone) (int64, error) {
	var person, date any
	var age int
	var multiple float64
	variant, amount := domain.FIStandard, domain.Cents(0)
	switch m.Kind {
	case domain.MilestoneAge:
		if err := s.checkOwner(ctx, m.PlanID, m.PersonID); err != nil {
			return 0, err
		}
		person, age = nullInt(m.PersonID), m.Age
	case domain.MilestoneDate:
		date = nullMonth(m.Date)
	case domain.MilestoneFI:
		multiple, variant = m.FIMultiple, m.FIVariant
		if variant.UsesAmount() {
			amount = m.FIAmount
		}
		if variant == domain.FICoast {
			if err := s.checkOwner(ctx, m.PlanID, m.PersonID); err != nil {
				return 0, err
			}
			person, age = nullInt(m.PersonID), m.Age
		}
	}
	if m.ID == 0 {
		return s.insert(ctx, `INSERT INTO milestones (plan_id, name, kind, person_id, age, date_month, fi_multiple,
			fi_variant, fi_amount_cents) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.PlanID, m.Name, m.Kind, person, age, date, multiple, variant, amount)
	}
	return m.ID, s.execOne(ctx, `UPDATE milestones SET name = ?, kind = ?, person_id = ?, age = ?, date_month = ?,
		fi_multiple = ?, fi_variant = ?, fi_amount_cents = ? WHERE id = ? AND plan_id = ?`,
		m.Name, m.Kind, person, age, date, multiple, variant, amount, m.ID, m.PlanID)
}

// DeleteMilestone removes a milestone; linked streams fall back to their fixed months.
func (s *Store) DeleteMilestone(ctx context.Context, planID, id int64) error {
	return s.execOne(ctx, `DELETE FROM milestones WHERE id = ? AND plan_id = ?`, id, planID)
}

// checkMilestones verifies that every set milestone ID belongs to the plan.
func (s *Store) checkMilestones(ctx context.Context, planID int64, ids ...*int64) error {
	for _, id := range ids {
		if id == nil {
			continue
		}
		var n int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM milestones WHERE id = ? AND plan_id = ?`, *id, planID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}
