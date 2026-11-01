-- Withdrawal strategy, withdrawal order and pension plan availability (phase 8).
ALTER TABLE plan_assumptions ADD COLUMN wd_strategy TEXT NOT NULL DEFAULT 'fixed';
ALTER TABLE plan_assumptions ADD COLUMN wd_start_milestone_id INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
ALTER TABLE plan_assumptions ADD COLUMN wd_rate    REAL NOT NULL DEFAULT 0.04;
ALTER TABLE plan_assumptions ADD COLUMN wd_floor   REAL NOT NULL DEFAULT 0.7;
ALTER TABLE plan_assumptions ADD COLUMN wd_ceiling REAL NOT NULL DEFAULT 1.5;
ALTER TABLE plan_assumptions ADD COLUMN wd_order   TEXT NOT NULL DEFAULT 'cash,deposit,investment,other,pension';
ALTER TABLE plan_assumptions ADD COLUMN pension_available_milestone_id INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
