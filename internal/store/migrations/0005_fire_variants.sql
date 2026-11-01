-- FIRE variants for financial-independence milestones. Existing FI milestones
-- become the classic variant. fi_amount_cents is the yearly budget (lean,
-- fat) or part-time income (barista), in today's euros. Coast FIRE reuses
-- person_id and age as its target.
ALTER TABLE milestones ADD COLUMN fi_variant TEXT NOT NULL DEFAULT 'standard';
ALTER TABLE milestones ADD COLUMN fi_amount_cents INTEGER NOT NULL DEFAULT 0;
