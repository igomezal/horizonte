package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/igomezal/horizonte/internal/domain"
)

const prepaymentCols = `id, plan_id, debt_id, amount_cents, frequency, mode, fee_rate, start_month, end_month,
	start_milestone_id, end_milestone_id, redirect_account_id`

func scanPrepayment(sc interface{ Scan(...any) error }) (domain.Prepayment, error) {
	var p domain.Prepayment
	var start, end sql.NullString
	var startMS, endMS, redirect sql.NullInt64
	if err := sc.Scan(&p.ID, &p.PlanID, &p.DebtID, &p.Amount, &p.Frequency, &p.Mode, &p.FeeRate,
		&start, &end, &startMS, &endMS, &redirect); err != nil {
		return p, err
	}
	p.StartMS, p.EndMS, p.RedirectAccountID = intPtr(startMS), intPtr(endMS), intPtr(redirect)
	var err error
	if p.Start, err = scanMonth(start); err != nil {
		return p, err
	}
	p.End, err = scanMonth(end)
	return p, err
}

// ListPrepayments returns the plan's early repayments, grouped by debt.
func (s *Store) ListPrepayments(ctx context.Context, planID int64) ([]domain.Prepayment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+prepaymentCols+` FROM debt_prepayments
		WHERE plan_id = ? ORDER BY debt_id, id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Prepayment
	for rows.Next() {
		p, err := scanPrepayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPrepayment returns one early repayment of the plan.
func (s *Store) GetPrepayment(ctx context.Context, planID, id int64) (domain.Prepayment, error) {
	p, err := scanPrepayment(s.DB.QueryRowContext(ctx,
		`SELECT `+prepaymentCols+` FROM debt_prepayments WHERE id = ? AND plan_id = ?`, id, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// SavePrepayment inserts (ID 0) or updates an early repayment and returns its ID.
// One-off payments store no end.
func (s *Store) SavePrepayment(ctx context.Context, p domain.Prepayment) (int64, error) {
	if _, err := s.GetDebt(ctx, p.PlanID, p.DebtID); err != nil {
		return 0, err
	}
	if p.Frequency == domain.PrepayOnce {
		p.End, p.EndMS = domain.Month{}, nil
	}
	if err := s.checkMilestones(ctx, p.PlanID, p.StartMS, p.EndMS); err != nil {
		return 0, err
	}
	if err := s.checkAccount(ctx, p.PlanID, p.RedirectAccountID); err != nil {
		return 0, err
	}
	if p.ID == 0 {
		return s.insert(ctx, `INSERT INTO debt_prepayments (plan_id, debt_id, amount_cents, frequency, mode, fee_rate,
			start_month, end_month, start_milestone_id, end_milestone_id, redirect_account_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.PlanID, p.DebtID, p.Amount, p.Frequency, p.Mode, p.FeeRate,
			nullMonth(p.Start), nullMonth(p.End), nullInt(p.StartMS), nullInt(p.EndMS), nullInt(p.RedirectAccountID))
	}
	return p.ID, s.execOne(ctx, `UPDATE debt_prepayments SET debt_id = ?, amount_cents = ?, frequency = ?, mode = ?,
		fee_rate = ?, start_month = ?, end_month = ?, start_milestone_id = ?, end_milestone_id = ?,
		redirect_account_id = ? WHERE id = ? AND plan_id = ?`,
		p.DebtID, p.Amount, p.Frequency, p.Mode, p.FeeRate,
		nullMonth(p.Start), nullMonth(p.End), nullInt(p.StartMS), nullInt(p.EndMS), nullInt(p.RedirectAccountID),
		p.ID, p.PlanID)
}

// DeletePrepayment removes an early repayment.
func (s *Store) DeletePrepayment(ctx context.Context, planID, id int64) error {
	return s.execOne(ctx, `DELETE FROM debt_prepayments WHERE id = ? AND plan_id = ?`, id, planID)
}
