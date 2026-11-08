CREATE TABLE IF NOT EXISTS accounts (
    id uuid PRIMARY KEY,
    code varchar(64) NOT NULL UNIQUE,
    account_data jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS fiscal_years (
    id uuid PRIMARY KEY,
    code varchar(32) NOT NULL UNIQUE,
    start_date date NOT NULL,
    end_date date NOT NULL,
    year_data jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS fiscal_periods (
    id uuid PRIMARY KEY,
    year_id uuid NOT NULL REFERENCES fiscal_years(id),
    code varchar(32) NOT NULL,
    start_date timestamptz NOT NULL,
    end_date timestamptz NOT NULL,
    state varchar(32) NOT NULL
);

CREATE TABLE IF NOT EXISTS journals (
    id uuid PRIMARY KEY,
    code varchar(32) NOT NULL,
    next_sequence bigint NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS account_moves (
    id uuid PRIMARY KEY,
    journal_id uuid NOT NULL,
    entry_date timestamptz NOT NULL,
    status varchar(16) NOT NULL,
    entry jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS account_moves_entry_date_idx ON account_moves(entry_date) WHERE status = 'POSTED';
CREATE INDEX IF NOT EXISTS account_moves_journal_idx ON account_moves(journal_id, id);

CREATE TABLE IF NOT EXISTS account_move_lines (
    move_id uuid NOT NULL REFERENCES account_moves(id),
    line_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id),
    partner_id uuid,
    debit jsonb,
    credit jsonb,
    PRIMARY KEY (move_id, line_id)
);

CREATE TABLE IF NOT EXISTS subledger_mappings (
    event_type varchar(128) PRIMARY KEY,
    debit_account_id uuid NOT NULL REFERENCES accounts(id),
    credit_account_id uuid NOT NULL REFERENCES accounts(id),
    journal_id uuid NOT NULL
);

CREATE TABLE IF NOT EXISTS subledger_events (
    idempotency_key varchar(255) PRIMARY KEY,
    event jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS subledger_open_lines (
    id uuid PRIMARY KEY,
    partner_id uuid,
    kind varchar(32) NOT NULL,
    line_data jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS subledger_open_lines_partner_idx ON subledger_open_lines(partner_id, kind);

CREATE TABLE IF NOT EXISTS fixed_assets (
    id uuid PRIMARY KEY,
    asset_data jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS bank_items (
    account_id uuid NOT NULL,
    item_id uuid,
    item_data jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS exchange_rates (
    from_currency varchar(3) NOT NULL,
    to_currency varchar(3) NOT NULL,
    rate numeric(24,6) NOT NULL CHECK (rate > 0),
    effective_date date NOT NULL,
    rate_type varchar(16) NOT NULL,
    PRIMARY KEY (from_currency, to_currency, effective_date, rate_type)
);

CREATE TABLE IF NOT EXISTS financial_audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type varchar(128) NOT NULL,
    entity_id uuid NOT NULL,
    action varchar(128) NOT NULL,
    actor_id uuid NOT NULL,
    payload_before bytea,
    payload_after bytea,
    timestamp timestamptz NOT NULL,
    previous_log_hash varchar(64) NOT NULL,
    current_log_hash varchar(64) NOT NULL
);
