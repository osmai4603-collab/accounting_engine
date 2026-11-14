DROP TRIGGER IF EXISTS trg_prevent_posted_entry_number_mutation ON account_moves;
DROP FUNCTION IF EXISTS prevent_posted_entry_number_mutation();

ALTER TABLE account_moves
    DROP CONSTRAINT IF EXISTS chk_posted_move_posting_date,
    DROP COLUMN IF EXISTS posting_date;

ALTER TABLE journals
    DROP CONSTRAINT IF EXISTS chk_journal_numbering_policy,
    DROP COLUMN IF EXISTS numbering_policy,
    DROP COLUMN IF EXISTS allowed_currencies,
    DROP COLUMN IF EXISTS is_active;

ALTER TABLE fiscal_periods
    DROP COLUMN IF EXISTS last_state_at,
    DROP COLUMN IF EXISTS last_state_reason,
    DROP COLUMN IF EXISTS last_state_actor_id;
