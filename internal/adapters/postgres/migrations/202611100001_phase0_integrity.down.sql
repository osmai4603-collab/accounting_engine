DROP TRIGGER IF EXISTS trg_check_posted_move_balance_statement ON account_moves;
DROP TRIGGER IF EXISTS trg_check_posted_move_balance_header ON account_moves;
DROP FUNCTION IF EXISTS check_posted_move_balance_statement();

DROP TRIGGER IF EXISTS trg_protect_posted_move_lines ON account_move_lines;
DROP TRIGGER IF EXISTS trg_immutable_posted_lines ON account_move_lines;
DROP FUNCTION IF EXISTS protect_posted_move_lines();

DROP TRIGGER IF EXISTS trg_prevent_direct_posted_move_insert ON account_moves;
DROP FUNCTION IF EXISTS prevent_direct_posted_move_insert();

DROP TRIGGER IF EXISTS trg_prevent_posted_move_mutation ON account_moves;
DROP FUNCTION IF EXISTS prevent_posted_move_mutation();
