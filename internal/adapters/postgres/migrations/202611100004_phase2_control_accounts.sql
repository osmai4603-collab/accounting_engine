ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS is_control_account boolean NOT NULL DEFAULT FALSE;
