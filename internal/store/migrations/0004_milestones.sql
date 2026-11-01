-- Milestones (phase 3) and the links that let streams start or stop on them.
-- Deleting a milestone unlinks streams (SET NULL), which then fall back to
-- their fixed months. Age milestones are removed with their person.
CREATE TABLE milestones (
    id          INTEGER PRIMARY KEY,
    plan_id     INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    person_id   INTEGER REFERENCES people(id) ON DELETE CASCADE,
    age         INTEGER NOT NULL DEFAULT 0,
    date_month  TEXT,
    fi_multiple REAL    NOT NULL DEFAULT 0
);
CREATE INDEX milestones_plan ON milestones(plan_id);

ALTER TABLE incomes  ADD COLUMN start_milestone_id INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
ALTER TABLE incomes  ADD COLUMN end_milestone_id   INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
ALTER TABLE expenses ADD COLUMN start_milestone_id INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
ALTER TABLE expenses ADD COLUMN end_milestone_id   INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
ALTER TABLE accounts ADD COLUMN contribution_end_milestone_id INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
