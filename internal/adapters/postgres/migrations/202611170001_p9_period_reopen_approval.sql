ALTER TABLE fiscal_periods
    ADD COLUMN IF NOT EXISTS last_state_approval_id uuid;
