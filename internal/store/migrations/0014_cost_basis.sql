-- What an account's holdings cost, to tax their real gains. NULL counts the
-- starting balance as the cost.
ALTER TABLE accounts ADD COLUMN cost_basis_cents INTEGER;
