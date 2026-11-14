ALTER TABLE fiscal_periods
    ADD COLUMN IF NOT EXISTS last_state_actor_id uuid,
    ADD COLUMN IF NOT EXISTS last_state_reason text,
    ADD COLUMN IF NOT EXISTS last_state_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE journals
    ADD COLUMN IF NOT EXISTS is_active boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS allowed_currencies text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS numbering_policy varchar(32) NOT NULL DEFAULT 'per_journal';

ALTER TABLE journals
    DROP CONSTRAINT IF EXISTS chk_journal_numbering_policy;
ALTER TABLE journals
    ADD CONSTRAINT chk_journal_numbering_policy
    CHECK (numbering_policy = 'per_journal');

ALTER TABLE account_moves
    ADD COLUMN IF NOT EXISTS posting_date date;

UPDATE account_moves
SET posting_date = COALESCE(
    NULLIF(entry->>'PostingDate', '')::date,
    posted_at::date,
    entry_date::date
)
WHERE status IN ('posted', 'reversed') AND posting_date IS NULL;

ALTER TABLE account_moves
    DROP CONSTRAINT IF EXISTS chk_posted_move_posting_date;
ALTER TABLE account_moves
    ADD CONSTRAINT chk_posted_move_posting_date
    CHECK (status = 'draft' OR posting_date IS NOT NULL);

CREATE OR REPLACE FUNCTION prevent_posted_entry_number_mutation() RETURNS trigger AS $$
BEGIN
    IF OLD.status IN ('posted', 'reversed')
       AND NEW.entry_number IS DISTINCT FROM OLD.entry_number THEN
        RAISE EXCEPTION 'Cannot change the number of a posted move (id=%).', OLD.id
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_prevent_posted_entry_number_mutation ON account_moves;
CREATE TRIGGER trg_prevent_posted_entry_number_mutation
    BEFORE UPDATE OF entry_number ON account_moves
    FOR EACH ROW EXECUTE FUNCTION prevent_posted_entry_number_mutation();
