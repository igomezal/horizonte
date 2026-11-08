-- Planned sale of a real-estate account. There is no sale while both
-- sale_month and sale_milestone_id are NULL.
ALTER TABLE accounts ADD COLUMN sale_month TEXT;
ALTER TABLE accounts ADD COLUMN sale_milestone_id INTEGER REFERENCES milestones(id) ON DELETE SET NULL;
ALTER TABLE accounts ADD COLUMN sale_share REAL NOT NULL DEFAULT 1;
ALTER TABLE accounts ADD COLUMN sale_cost_rate REAL NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN sale_destination_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE accounts ADD COLUMN sale_main_residence INTEGER NOT NULL DEFAULT 0;
