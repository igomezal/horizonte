-- Current finances (phase 1). Money is stored in integer euro cents, rates as
-- ratios (0.03 = 3 %), months as 'YYYY-MM' text (NULL = not set).

CREATE TABLE people (
    id              INTEGER PRIMARY KEY,
    plan_id         INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    birth_month     TEXT    NOT NULL,
    life_expectancy INTEGER NOT NULL
);
CREATE INDEX people_plan ON people(plan_id);

CREATE TABLE accounts (
    id                         INTEGER PRIMARY KEY,
    plan_id                    INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    name                       TEXT    NOT NULL,
    kind                       TEXT    NOT NULL,
    owner_id                   INTEGER REFERENCES people(id) ON DELETE SET NULL,
    balance_cents              INTEGER NOT NULL DEFAULT 0,
    monthly_contribution_cents INTEGER NOT NULL DEFAULT 0,
    annual_return              REAL
);
CREATE INDEX accounts_plan ON accounts(plan_id);

CREATE TABLE incomes (
    id                INTEGER PRIMARY KEY,
    plan_id           INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    name              TEXT    NOT NULL,
    kind              TEXT    NOT NULL,
    owner_id          INTEGER REFERENCES people(id) ON DELETE SET NULL,
    amount_cents      INTEGER NOT NULL,
    payments_per_year INTEGER NOT NULL DEFAULT 12,
    annual_growth     REAL,
    start_month       TEXT,
    end_month         TEXT
);
CREATE INDEX incomes_plan ON incomes(plan_id);

CREATE TABLE expenses (
    id                INTEGER PRIMARY KEY,
    plan_id           INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    name              TEXT    NOT NULL,
    category          TEXT    NOT NULL,
    amount_cents      INTEGER NOT NULL,
    payments_per_year INTEGER NOT NULL DEFAULT 12,
    annual_growth     REAL,
    start_month       TEXT,
    end_month         TEXT
);
CREATE INDEX expenses_plan ON expenses(plan_id);

CREATE TABLE debts (
    id                    INTEGER PRIMARY KEY,
    plan_id               INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    name                  TEXT    NOT NULL,
    kind                  TEXT    NOT NULL,
    balance_cents         INTEGER NOT NULL,
    annual_rate           REAL    NOT NULL DEFAULT 0,
    monthly_payment_cents INTEGER NOT NULL
);
CREATE INDEX debts_plan ON debts(plan_id);
