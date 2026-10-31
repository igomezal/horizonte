-- Application-wide key/value settings (e.g. default plan, preferences).
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- A plan groups a person's (or couple's) finances and assumptions.
-- Scenarios (phase 7) are plans cloned from a parent plan.
CREATE TABLE plans (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    parent_id  INTEGER REFERENCES plans(id) ON DELETE SET NULL,
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);
