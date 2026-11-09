CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY,
    code VARCHAR(32) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    account_type VARCHAR(50) NOT NULL,
    normal_balance VARCHAR(6) NOT NULL,
    currency_id VARCHAR(3) NOT NULL DEFAULT 'SAR',
    parent_id UUID REFERENCES accounts(id) ON DELETE RESTRICT,
    is_reconcilable BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_cash_equivalent BOOLEAN NOT NULL DEFAULT FALSE,
    is_non_cash_adjustment BOOLEAN NOT NULL DEFAULT FALSE,
    cash_flow_activity VARCHAR(20) NOT NULL DEFAULT 'unclassified',
    sub_classification VARCHAR(50) NOT NULL DEFAULT 'unclassified',
    ifrs_category VARCHAR(30) NOT NULL DEFAULT 'operating',
    display_order INT NOT NULL DEFAULT 100,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_account_type CHECK (account_type IN ('asset', 'liability', 'equity', 'income', 'expense')),
    CONSTRAINT chk_normal_balance CHECK (normal_balance IN ('debit', 'credit')),
    CONSTRAINT chk_cash_flow_activity CHECK (cash_flow_activity IN ('operating', 'investing', 'financing', 'none', 'unclassified')),
    CONSTRAINT chk_ifrs_category CHECK (ifrs_category IN ('operating', 'investing', 'financing', 'tax', 'discontinued'))
);

CREATE TABLE IF NOT EXISTS fiscal_years (
    id uuid PRIMARY KEY,
    code varchar(32) NOT NULL UNIQUE,
    year int NOT NULL UNIQUE,
    start_date date NOT NULL,
    end_date date NOT NULL,
    state varchar(20) NOT NULL DEFAULT 'open',
    year_data jsonb NOT NULL,
    CONSTRAINT chk_fiscal_year_state CHECK (state IN ('open', 'soft_closed', 'hard_closed'))
);

CREATE TABLE IF NOT EXISTS fiscal_periods (
    id uuid PRIMARY KEY,
    year_id uuid NOT NULL REFERENCES fiscal_years(id) ON DELETE RESTRICT,
    code varchar(32) NOT NULL,
    start_date timestamptz NOT NULL,
    end_date timestamptz NOT NULL,
    state varchar(32) NOT NULL,
    period_number int,
    name varchar(50),
    closed_at timestamptz,
    closed_by uuid,
    CONSTRAINT uq_fiscal_period_code UNIQUE (year_id, code),
    CONSTRAINT chk_period_state CHECK (state IN ('open', 'soft_closed', 'hard_closed')),
    CONSTRAINT chk_period_dates CHECK (start_date < end_date),
    CONSTRAINT chk_period_closed CHECK (
        (state = 'open' AND closed_at IS NULL) OR
        (state IN ('soft_closed', 'hard_closed') AND closed_at IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS journals (
    id uuid PRIMARY KEY,
    code varchar(32) NOT NULL,
    next_sequence bigint NOT NULL DEFAULT 0,
    CONSTRAINT uq_journals_code UNIQUE (code),
    CONSTRAINT chk_journal_next_sequence CHECK (next_sequence >= 0)
);

CREATE TABLE IF NOT EXISTS account_moves (
    id uuid PRIMARY KEY,
    journal_id uuid NOT NULL REFERENCES journals(id) ON DELETE RESTRICT,
    period_id uuid REFERENCES fiscal_periods(id) ON DELETE RESTRICT,
    entry_date timestamptz NOT NULL,
    status varchar(16) NOT NULL,
    name varchar(64),
    ref varchar(255),
    reversed_move_id uuid REFERENCES account_moves(id) ON DELETE RESTRICT,
    created_by varchar(255),
    posted_by varchar(255),
    posted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    entry jsonb NOT NULL,
    CONSTRAINT chk_move_status CHECK (status IN ('draft', 'posted', 'reversed'))
);
CREATE INDEX IF NOT EXISTS account_moves_entry_date_idx ON account_moves(entry_date) WHERE status = 'posted';
CREATE INDEX IF NOT EXISTS account_moves_journal_idx ON account_moves(journal_id, id);

CREATE TABLE IF NOT EXISTS account_move_lines (
    move_id uuid NOT NULL REFERENCES account_moves(id) ON DELETE CASCADE,
    line_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    partner_id uuid,
    cost_center_id uuid,
    matching_number varchar(64),
    reconciled_amount numeric(28,4) NOT NULL DEFAULT 0,
    debit numeric(28,4) NOT NULL DEFAULT 0,
    credit numeric(28,4) NOT NULL DEFAULT 0,
    amount_currency numeric(28,4),
    currency_id varchar(3) NOT NULL DEFAULT 'SAR',
    line_data jsonb NOT NULL,
    CONSTRAINT chk_line_reconciled_amount CHECK (reconciled_amount >= 0),
    CONSTRAINT chk_line_debit_positive CHECK (debit >= 0),
    CONSTRAINT chk_line_credit_positive CHECK (credit >= 0),
    CONSTRAINT chk_line_has_value CHECK (debit > 0 OR credit > 0),
    CONSTRAINT chk_line_not_both_sides CHECK (NOT (debit > 0 AND credit > 0)),
    PRIMARY KEY (move_id, line_id)
);
CREATE INDEX IF NOT EXISTS account_move_lines_account_date_idx ON account_move_lines(account_id, move_id);
CREATE INDEX IF NOT EXISTS account_move_lines_matching_idx ON account_move_lines(matching_number) WHERE matching_number IS NOT NULL;

CREATE TABLE IF NOT EXISTS subledger_mappings (
    event_type varchar(128) PRIMARY KEY,
    debit_account_id uuid NOT NULL REFERENCES accounts(id),
    credit_account_id uuid NOT NULL REFERENCES accounts(id),
    journal_id uuid NOT NULL REFERENCES journals(id) ON DELETE RESTRICT
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
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    item_id uuid,
    item_data jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS bank_items_account_idx ON bank_items(account_id);

CREATE TABLE IF NOT EXISTS exchange_rates (
    from_currency varchar(3) NOT NULL,
    to_currency varchar(3) NOT NULL,
    rate numeric(28,10) NOT NULL,
    effective_date date NOT NULL,
    rate_type varchar(16) NOT NULL,
    PRIMARY KEY (from_currency, to_currency, effective_date, rate_type),
    CONSTRAINT chk_exchange_rate_positive CHECK (rate > 0)
);

CREATE TABLE IF NOT EXISTS financial_audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type varchar(128) NOT NULL,
    entity_id uuid NOT NULL,
    action varchar(128) NOT NULL,
    actor_id uuid NOT NULL,
    payload_before jsonb,
    payload_after jsonb,
    ip_address inet,
    timestamp timestamptz NOT NULL,
    previous_log_hash varchar(64) NOT NULL,
    current_log_hash varchar(64) NOT NULL
);

CREATE TABLE IF NOT EXISTS accounting_configurations (
    id uuid PRIMARY KEY,
    retained_earnings_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    fx_gain_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    fx_loss_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    fx_reconciliation_journal_id uuid NOT NULL REFERENCES journals(id) ON DELETE RESTRICT,
    functional_currency varchar(3) NOT NULL DEFAULT 'SAR',
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_accounting_configurations_singleton
    ON accounting_configurations ((TRUE));

CREATE TABLE IF NOT EXISTS account_reconciliations (
    id uuid PRIMARY KEY,
    date date NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    currency_id varchar(3) NOT NULL,
    realized_fx_move_id uuid REFERENCES account_moves(id) ON DELETE RESTRICT,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account_reconciliation_items (
    id uuid PRIMARY KEY,
    reconciliation_id uuid NOT NULL REFERENCES account_reconciliations(id) ON DELETE CASCADE,
    move_id uuid NOT NULL,
    move_line_id uuid NOT NULL,
    allocated_amount numeric(28,4) NOT NULL,
    currency_id varchar(3) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_reconciliation_line UNIQUE (reconciliation_id, move_id, move_line_id),
    CONSTRAINT chk_reconciliation_allocated_amount CHECK (allocated_amount > 0),
    FOREIGN KEY (move_id, move_line_id) REFERENCES account_move_lines(move_id, line_id) ON DELETE RESTRICT
);
