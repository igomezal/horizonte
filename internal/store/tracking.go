package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/igomezal/horizonte/internal/domain"
)

// ErrMonthExists is returned when a month already has a net worth entry.
var ErrMonthExists = errors.New("month already recorded")

const dateLayout = "2006-01-02"

// queryNetWorthEntries reads entries in month order, with their balances.
func (s *Store) queryNetWorthEntries(ctx context.Context, where string, args ...any) ([]domain.NetWorthEntry, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT e.month, e.debt_cents, b.kind, b.amount_cents
		FROM net_worth_entries e LEFT JOIN net_worth_balances b ON b.month = e.month
		`+where+` ORDER BY e.month, b.kind`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.NetWorthEntry
	for rows.Next() {
		var month string
		var debt domain.Cents
		var kind sql.NullString
		var amount sql.NullInt64
		if err := rows.Scan(&month, &debt, &kind, &amount); err != nil {
			return nil, err
		}
		m, err := domain.ParseMonth(month)
		if err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Month != m {
			out = append(out, domain.NetWorthEntry{Month: m, Debt: debt, Balances: map[domain.AccountKind]domain.Cents{}})
		}
		if kind.Valid {
			out[len(out)-1].Balances[domain.AccountKind(kind.String)] = domain.Cents(amount.Int64)
		}
	}
	return out, rows.Err()
}

// ListNetWorthEntries returns the household's recorded months, oldest first.
func (s *Store) ListNetWorthEntries(ctx context.Context) ([]domain.NetWorthEntry, error) {
	return s.queryNetWorthEntries(ctx, "")
}

// GetNetWorthEntry returns one month's entry, or ErrNotFound.
func (s *Store) GetNetWorthEntry(ctx context.Context, m domain.Month) (domain.NetWorthEntry, error) {
	out, err := s.queryNetWorthEntries(ctx, "WHERE e.month = ?", m.String())
	if err != nil {
		return domain.NetWorthEntry{}, err
	}
	if len(out) == 0 {
		return domain.NetWorthEntry{}, ErrNotFound
	}
	return out[0], nil
}

func (s *Store) insertNetWorthBalances(ctx context.Context, e domain.NetWorthEntry) error {
	for k, v := range e.Balances {
		if v == 0 {
			continue
		}
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO net_worth_balances (month, kind, amount_cents) VALUES (?, ?, ?)`,
			e.Month.String(), string(k), int64(v)); err != nil {
			return err
		}
	}
	return nil
}

// AddNetWorthEntry records a new month, or returns ErrMonthExists: an existing
// month is only ever changed with UpdateNetWorthEntry.
func (s *Store) AddNetWorthEntry(ctx context.Context, e domain.NetWorthEntry) error {
	return s.InTx(ctx, func(tx *Store) error {
		res, err := tx.q.ExecContext(ctx, `INSERT INTO net_worth_entries (month, debt_cents) VALUES (?, ?)
			ON CONFLICT(month) DO NOTHING`, e.Month.String(), int64(e.Debt))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrMonthExists
		}
		return tx.insertNetWorthBalances(ctx, e)
	})
}

// UpdateNetWorthEntry replaces a recorded month, or returns ErrNotFound.
func (s *Store) UpdateNetWorthEntry(ctx context.Context, e domain.NetWorthEntry) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.execOne(ctx, `UPDATE net_worth_entries SET debt_cents = ? WHERE month = ?`,
			int64(e.Debt), e.Month.String()); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM net_worth_balances WHERE month = ?`, e.Month.String()); err != nil {
			return err
		}
		return tx.insertNetWorthBalances(ctx, e)
	})
}

// DeleteNetWorthEntry removes a recorded month, or returns ErrNotFound.
func (s *Store) DeleteNetWorthEntry(ctx context.Context, m domain.Month) error {
	return s.execOne(ctx, `DELETE FROM net_worth_entries WHERE month = ?`, m.String())
}

// GetNetWorthBaseline returns the saved baseline, or nil if there is none.
func (s *Store) GetNetWorthBaseline(ctx context.Context) (*domain.NetWorthBaseline, error) {
	var b domain.NetWorthBaseline
	var saved string
	err := s.q.QueryRowContext(ctx, `SELECT plan_name, saved_on FROM net_worth_baseline WHERE id = 1`).Scan(&b.PlanName, &saved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if b.SavedOn, err = time.Parse(dateLayout, saved); err != nil {
		return nil, err
	}
	rows, err := s.q.QueryContext(ctx, `SELECT month, net_worth_cents FROM net_worth_baseline_points ORDER BY month`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var month string
		var p domain.NetWorthPoint
		if err := rows.Scan(&month, &p.NetWorth); err != nil {
			return nil, err
		}
		if p.Month, err = domain.ParseMonth(month); err != nil {
			return nil, err
		}
		b.Points = append(b.Points, p)
	}
	return &b, rows.Err()
}

// SaveNetWorthBaseline stores b as the baseline, replacing any other. Asking
// the user before replacing one is up to the caller.
func (s *Store) SaveNetWorthBaseline(ctx context.Context, b domain.NetWorthBaseline) error {
	return s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM net_worth_baseline_points`); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO net_worth_baseline (id, plan_name, saved_on) VALUES (1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET plan_name = excluded.plan_name, saved_on = excluded.saved_on`,
			b.PlanName, b.SavedOn.Format(dateLayout)); err != nil {
			return err
		}
		for _, p := range b.Points {
			if _, err := tx.q.ExecContext(ctx,
				`INSERT INTO net_worth_baseline_points (month, net_worth_cents) VALUES (?, ?)`,
				p.Month.String(), int64(p.NetWorth)); err != nil {
				return err
			}
		}
		return nil
	})
}

// MergeNetWorthHistory adds imported history without overwriting anything:
// only months that are missing are added, and the baseline only if there is
// none. It returns how many months were added and whether the baseline was.
func (s *Store) MergeNetWorthHistory(ctx context.Context, entries []domain.NetWorthEntry, baseline *domain.NetWorthBaseline) (added int, baselineAdded bool, err error) {
	err = s.InTx(ctx, func(tx *Store) error {
		for _, e := range entries {
			switch err := tx.AddNetWorthEntry(ctx, e); {
			case errors.Is(err, ErrMonthExists):
			case err != nil:
				return err
			default:
				added++
			}
		}
		if baseline == nil {
			return nil
		}
		existing, err := tx.GetNetWorthBaseline(ctx)
		if err != nil || existing != nil {
			return err
		}
		baselineAdded = true
		return tx.SaveNetWorthBaseline(ctx, *baseline)
	})
	return added, baselineAdded, err
}
