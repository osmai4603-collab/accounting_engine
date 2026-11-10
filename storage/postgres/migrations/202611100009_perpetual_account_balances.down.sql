DROP TRIGGER IF EXISTS trg_update_balances_on_posting ON account_moves;
DROP FUNCTION IF EXISTS update_account_balance_on_posting();
DROP TABLE IF EXISTS account_balances;
