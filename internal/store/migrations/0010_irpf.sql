-- Tax model (phase 9): Spanish IRPF ('es', the new default) or a flat rate
-- ('flat'), and the regional IRPF scale.
ALTER TABLE plan_assumptions ADD COLUMN tax_model  TEXT NOT NULL DEFAULT 'es';
ALTER TABLE plan_assumptions ADD COLUMN tax_region TEXT NOT NULL DEFAULT 'general';
