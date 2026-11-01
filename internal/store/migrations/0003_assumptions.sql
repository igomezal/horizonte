-- Plan-wide assumptions for the projection (phase 2). One row per plan; a
-- missing row means "use the defaults". Rates are ratios (0.025 = 2,5 %).
CREATE TABLE plan_assumptions (
    plan_id            INTEGER PRIMARY KEY REFERENCES plans(id) ON DELETE CASCADE,
    inflation          REAL NOT NULL,
    tax_rate           REAL NOT NULL,
    return_cash        REAL NOT NULL,
    return_deposit     REAL NOT NULL,
    return_investment  REAL NOT NULL,
    return_pension     REAL NOT NULL,
    return_realestate  REAL NOT NULL,
    return_other       REAL NOT NULL,
    surplus_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL
);
