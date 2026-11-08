-- Money the liquid accounts keep when funding early repayments, in today's
-- euros (it rises with inflation in the simulation).
ALTER TABLE plan_assumptions ADD COLUMN prepayment_reserve_cents INTEGER NOT NULL DEFAULT 0;
