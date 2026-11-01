-- Early repayments of debts (amortizaciones anticipadas). Removed with their
-- debt; milestone links fall back to the fixed months when a milestone goes.
CREATE TABLE debt_prepayments (
    id                 INTEGER PRIMARY KEY,
    plan_id            INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    debt_id            INTEGER NOT NULL REFERENCES debts(id) ON DELETE CASCADE,
    amount_cents       INTEGER NOT NULL,
    frequency          TEXT    NOT NULL,
    mode               TEXT    NOT NULL,
    fee_rate           REAL    NOT NULL DEFAULT 0,
    start_month        TEXT,
    end_month          TEXT,
    start_milestone_id INTEGER REFERENCES milestones(id) ON DELETE SET NULL,
    end_milestone_id   INTEGER REFERENCES milestones(id) ON DELETE SET NULL
);
CREATE INDEX debt_prepayments_plan ON debt_prepayments(plan_id);
