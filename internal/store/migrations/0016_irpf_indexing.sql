-- 1 keeps IRPF scales and personal minimums fixed in nominal euros instead of
-- rising with inflation (the default).
ALTER TABLE plan_assumptions ADD COLUMN irpf_frozen INTEGER NOT NULL DEFAULT 0;
