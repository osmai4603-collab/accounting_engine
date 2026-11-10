DROP VIEW IF EXISTS v_account_move_lines_dual_currency;
ALTER TABLE account_move_lines
    DROP CONSTRAINT IF EXISTS chk_currency_layers_consistency;
