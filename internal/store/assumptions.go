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
	var wdStart, pensionFrom sql.NullInt64
	var order, strategy string
	err := s.q.QueryRowContext(ctx, `SELECT inflation, tax_rate, return_cash, return_deposit,
		return_investment, return_pension, return_realestate, return_other, surplus_account_id,
		vol_cash, vol_deposit, vol_investment, vol_pension, vol_realestate, vol_other, mc_runs,
		wd_strategy, wd_start_milestone_id, wd_rate, wd_floor, wd_ceiling, wd_order, pension_available_milestone_id,
		tax_model, tax_region, prepayment_reserve_cents, irpf_frozen
		FROM plan_assumptions WHERE plan_id = ?`, planID).
		Scan(&a.Inflation, &a.TaxRate, &cash, &deposit, &investment, &pension, &realestate, &other, &surplus,
			&vCash, &vDeposit, &vInvestment, &vPension, &vRealestate, &vOther, &a.MCRuns,
			&strategy, &wdStart, &a.Withdrawal.Rate, &a.Withdrawal.Floor, &a.Withdrawal.Ceiling, &order, &pensionFrom,
			&a.TaxModel, &a.TaxRegion, &a.PrepaymentReserve, &a.FreezeIRPF)
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
	a.Withdrawal.Strategy = engine.WithdrawalStrategy(strategy)
	a.Withdrawal.StartMS = intPtr(wdStart)
	a.PensionAvailableMS = intPtr(pensionFrom)
	if a.Order = engine.ParseOrder(order); a.Order == nil {
		a.Order = append([]domain.AccountKind(nil), engine.DefaultOrder...)
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
	if err := s.checkMilestones(ctx, planID, a.Withdrawal.StartMS, a.PensionAvailableMS); err != nil {
		return err
	}
	w := a.Withdrawal
	if w.Strategy == "" {
		w = engine.DefaultWithdrawal()
	}
	order := engine.FormatOrder(a.Order)
	if engine.ParseOrder(order) == nil {
		order = engine.FormatOrder(engine.DefaultOrder)
	}
	taxModel, taxRegion := a.TaxModel, a.TaxRegion
	if taxModel == "" {
		taxModel = engine.TaxES
	}
	if taxRegion == "" {
		taxRegion = "general"
	}
	r, v := a.Returns, a.Volatility
	_, err := s.q.ExecContext(ctx, `INSERT INTO plan_assumptions (plan_id, inflation, tax_rate, return_cash,
		return_deposit, return_investment, return_pension, return_realestate, return_other, surplus_account_id,
		vol_cash, vol_deposit, vol_investment, vol_pension, vol_realestate, vol_other, mc_runs,
		wd_strategy, wd_start_milestone_id, wd_rate, wd_floor, wd_ceiling, wd_order, pension_available_milestone_id,
		tax_model, tax_region, prepayment_reserve_cents, irpf_frozen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(plan_id) DO UPDATE SET inflation = excluded.inflation, tax_rate = excluded.tax_rate,
		return_cash = excluded.return_cash, return_deposit = excluded.return_deposit,
		return_investment = excluded.return_investment, return_pension = excluded.return_pension,
		return_realestate = excluded.return_realestate, return_other = excluded.return_other,
		surplus_account_id = excluded.surplus_account_id,
		vol_cash = excluded.vol_cash, vol_deposit = excluded.vol_deposit, vol_investment = excluded.vol_investment,
		vol_pension = excluded.vol_pension, vol_realestate = excluded.vol_realestate, vol_other = excluded.vol_other,
		mc_runs = excluded.mc_runs, wd_strategy = excluded.wd_strategy,
		wd_start_milestone_id = excluded.wd_start_milestone_id, wd_rate = excluded.wd_rate,
		wd_floor = excluded.wd_floor, wd_ceiling = excluded.wd_ceiling, wd_order = excluded.wd_order,
		pension_available_milestone_id = excluded.pension_available_milestone_id,
		tax_model = excluded.tax_model, tax_region = excluded.tax_region,
		prepayment_reserve_cents = excluded.prepayment_reserve_cents, irpf_frozen = excluded.irpf_frozen`,
		planID, a.Inflation, a.TaxRate, r[domain.AccountCash], r[domain.AccountDeposit], r[domain.AccountInvestment],
		r[domain.AccountPension], r[domain.AccountRealEstate], r[domain.AccountOther], nullInt(a.SurplusAccountID),
		v[domain.AccountCash], v[domain.AccountDeposit], v[domain.AccountInvestment],
		v[domain.AccountPension], v[domain.AccountRealEstate], v[domain.AccountOther], a.MCRuns,
		w.Strategy, nullInt(w.StartMS), w.Rate, w.Floor, w.Ceiling, order, nullInt(a.PensionAvailableMS),
		taxModel, taxRegion, a.PrepaymentReserve, a.FreezeIRPF)
	return err
}
