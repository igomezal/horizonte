-- Monte Carlo settings (phase 5): annual volatility per account kind and the
-- number of scenarios. Defaults are typical long-run values.
ALTER TABLE plan_assumptions ADD COLUMN vol_cash       REAL    NOT NULL DEFAULT 0;
ALTER TABLE plan_assumptions ADD COLUMN vol_deposit    REAL    NOT NULL DEFAULT 0.01;
ALTER TABLE plan_assumptions ADD COLUMN vol_investment REAL    NOT NULL DEFAULT 0.16;
ALTER TABLE plan_assumptions ADD COLUMN vol_pension    REAL    NOT NULL DEFAULT 0.10;
ALTER TABLE plan_assumptions ADD COLUMN vol_realestate REAL    NOT NULL DEFAULT 0.08;
ALTER TABLE plan_assumptions ADD COLUMN vol_other      REAL    NOT NULL DEFAULT 0.05;
ALTER TABLE plan_assumptions ADD COLUMN mc_runs        INTEGER NOT NULL DEFAULT 1000;
