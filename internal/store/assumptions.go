package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
)

// GetAssumptions returns the plan's assumptions, or the defaults if none are saved.
func (s *Store) GetAssumptions(ctx context.Context, planID int64) (engine.Assumptions, error) {
	a := engine.DefaultAssumptions()
	var surplus sql.NullInt64
	var cash, deposit, investment, pension, realestate, other float64
	var vCash, vDeposit, vInvestment, vPension, vRealestate, vOther float64
	err := s.q.QueryRowContext(ctx, `SELECT inflation, tax_rate, return_cash, return_deposit,
		return_investment, return_pension, return_realestate, return_other, surplus_account_id,
		vol_cash, vol_deposit, vol_investment, vol_pension, vol_realestate, vol_other, mc_runs
		FROM plan_assumptions WHERE plan_id = ?`, planID).
		Scan(&a.Inflation, &a.TaxRate, &cash, &deposit, &investment, &pension, &realestate, &other, &surplus,
			&vCash, &vDeposit, &vInvestment, &vPension, &vRealestate, &vOther, &a.MCRuns)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.Returns = map[domain.AccountKind]float64{
		domain.AccountCash: cash, domain.AccountDeposit: deposit, domain.AccountInvestment: investment,
		domain.AccountPension: pension, domain.AccountRealEstate: realestate, domain.AccountOther: other,
	}
	a.SurplusAccountID = intPtr(surplus)
	a.Volatility = map[domain.AccountKind]float64{
		domain.AccountCash: vCash, domain.AccountDeposit: vDeposit, domain.AccountInvestment: vInvestment,
		domain.AccountPension: vPension, domain.AccountRealEstate: vRealestate, domain.AccountOther: vOther,
	}
	return a, nil
}

// SaveAssumptions stores the plan's assumptions. A surplus account that does
// not belong to the plan is rejected with ErrNotFound.
func (s *Store) SaveAssumptions(ctx context.Context, planID int64, a engine.Assumptions) error {
	if a.SurplusAccountID != nil {
		if _, err := s.GetAccount(ctx, planID, *a.SurplusAccountID); err != nil {
			return err
		}
	}
	r, v := a.Returns, a.Volatility
	_, err := s.q.ExecContext(ctx, `INSERT INTO plan_assumptions (plan_id, inflation, tax_rate, return_cash,
		return_deposit, return_investment, return_pension, return_realestate, return_other, surplus_account_id,
		vol_cash, vol_deposit, vol_investment, vol_pension, vol_realestate, vol_other, mc_runs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(plan_id) DO UPDATE SET inflation = excluded.inflation, tax_rate = excluded.tax_rate,
		return_cash = excluded.return_cash, return_deposit = excluded.return_deposit,
		return_investment = excluded.return_investment, return_pension = excluded.return_pension,
		return_realestate = excluded.return_realestate, return_other = excluded.return_other,
		surplus_account_id = excluded.surplus_account_id,
		vol_cash = excluded.vol_cash, vol_deposit = excluded.vol_deposit, vol_investment = excluded.vol_investment,
		vol_pension = excluded.vol_pension, vol_realestate = excluded.vol_realestate, vol_other = excluded.vol_other,
		mc_runs = excluded.mc_runs`,
		planID, a.Inflation, a.TaxRate, r[domain.AccountCash], r[domain.AccountDeposit], r[domain.AccountInvestment],
		r[domain.AccountPension], r[domain.AccountRealEstate], r[domain.AccountOther], nullInt(a.SurplusAccountID),
		v[domain.AccountCash], v[domain.AccountDeposit], v[domain.AccountInvestment],
		v[domain.AccountPension], v[domain.AccountRealEstate], v[domain.AccountOther], a.MCRuns)
	return err
}
