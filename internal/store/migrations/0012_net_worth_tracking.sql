-- Real net worth recorded month by month, and the saved projection it is
-- compared with. Kept once for the household: nothing here references plans,
-- so the history survives copying or deleting any of them.
CREATE TABLE net_worth_entries (
    month      TEXT PRIMARY KEY, -- YYYY-MM: one entry per calendar month
    debt_cents INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE net_worth_balances (
    month        TEXT NOT NULL REFERENCES net_worth_entries(month) ON DELETE CASCADE,
    kind         TEXT NOT NULL, -- account kind
    amount_cents INTEGER NOT NULL,
    PRIMARY KEY (month, kind)
);

-- One baseline at a time.
CREATE TABLE net_worth_baseline (
    id        INTEGER PRIMARY KEY CHECK (id = 1),
    plan_name TEXT NOT NULL, -- the plan it came from, by name: the plan may go away
    saved_on  TEXT NOT NULL  -- YYYY-MM-DD
);

CREATE TABLE net_worth_baseline_points (
    month           TEXT PRIMARY KEY,
    net_worth_cents INTEGER NOT NULL -- nominal
);
