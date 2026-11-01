-- Where money goes once a debt is paid off: the freed instalment (debts) and
-- early repayments that fall due after payoff (debt_prepayments). NULL keeps
-- the plan's rule for unallocated money; deleting the account resets it.
ALTER TABLE debts            ADD COLUMN redirect_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE debt_prepayments ADD COLUMN redirect_account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL;
