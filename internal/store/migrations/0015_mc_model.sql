-- Monte Carlo model per plan: 'parametric' (the original, independent normal
-- returns) or 'bootstrap' (blocks of historical returns and inflation).
ALTER TABLE plan_assumptions ADD COLUMN mc_model TEXT NOT NULL DEFAULT 'parametric';
