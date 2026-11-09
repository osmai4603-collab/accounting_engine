CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS currencies (
    code varchar(3) PRIMARY KEY,
    numeric_code varchar(3),
    name varchar(100) NOT NULL,
    minor_units smallint NOT NULL DEFAULT 2 CHECK (minor_units BETWEEN 0 AND 4),
    is_active boolean NOT NULL DEFAULT TRUE
);
INSERT INTO currencies(code, numeric_code, name, minor_units) VALUES
    ('BHD', '048', 'Bahraini Dinar', 3),
    ('EUR', '978', 'Euro', 2),
    ('JPY', '392', 'Yen', 0),
    ('KWD', '414', 'Kuwaiti Dinar', 3),
    ('SAR', '682', 'Saudi Riyal', 2),
    ('USD', '840', 'US Dollar', 2)
ON CONFLICT (code) DO NOTHING;

CREATE TABLE IF NOT EXISTS companies (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code varchar(50) NOT NULL UNIQUE,
    legal_name varchar(255) NOT NULL,
    functional_currency varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    presentation_currency varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    is_active boolean NOT NULL DEFAULT TRUE,
    created_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY,
    company_id UUID NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    code VARCHAR(32) NOT NULL,
    name VARCHAR(255) NOT NULL,
    account_type VARCHAR(50) NOT NULL,
    normal_balance VARCHAR(6) NOT NULL,
    currency_id VARCHAR(3) NOT NULL DEFAULT 'SAR' REFERENCES currencies(code) ON DELETE RESTRICT,
    parent_id UUID,
    is_reconcilable BOOLEAN NOT NULL DEFAULT FALSE,
    is_contra_account BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_cash_equivalent BOOLEAN NOT NULL DEFAULT FALSE,
    is_non_cash_adjustment BOOLEAN NOT NULL DEFAULT FALSE,
    cash_flow_activity VARCHAR(40) NOT NULL DEFAULT 'unclassified',
    sub_classification VARCHAR(50) NOT NULL DEFAULT 'unclassified',
    ifrs_category VARCHAR(30) NOT NULL DEFAULT 'unclassified',
    display_order INT NOT NULL DEFAULT 100,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (company_id, id),
    CONSTRAINT chk_account_type CHECK (account_type IN ('asset', 'liability', 'equity', 'income', 'expense')),
    CONSTRAINT chk_normal_balance CHECK (normal_balance IN ('debit', 'credit')),
    CONSTRAINT chk_cash_flow_activity CHECK (cash_flow_activity IN (
        'operating', 'investing', 'financing', 'cash_equivalents', 'non_cash', 'none', 'unclassified'
    )),
    CONSTRAINT chk_ifrs_category CHECK (ifrs_category IN (
        'operating', 'investing', 'financing', 'tax', 'income_taxes',
        'discontinued', 'discontinued_operations', 'equity_movements',
        'balance_sheet_perpetual', 'unclassified'
    )),
    FOREIGN KEY (company_id, parent_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_accounts_company_code ON accounts(company_id, code);
CREATE INDEX IF NOT EXISTS idx_accounts_company ON accounts(company_id, id);

CREATE TABLE IF NOT EXISTS fiscal_years (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    code varchar(32) NOT NULL,
    year int NOT NULL,
    start_date date NOT NULL,
    end_date date NOT NULL,
    state varchar(20) NOT NULL DEFAULT 'open',
    year_data jsonb NOT NULL,
    is_closed boolean NOT NULL DEFAULT FALSE,
    UNIQUE (company_id, id),
    CONSTRAINT chk_fiscal_year_state CHECK (state IN ('open', 'soft_closed', 'hard_closed'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_fiscal_years_company_code ON fiscal_years(company_id, code);
CREATE UNIQUE INDEX IF NOT EXISTS uq_fiscal_years_company_year ON fiscal_years(company_id, year);

CREATE TABLE IF NOT EXISTS fiscal_periods (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    year_id uuid NOT NULL,
    code varchar(32) NOT NULL,
    start_date timestamptz NOT NULL,
    end_date timestamptz NOT NULL,
    state varchar(32) NOT NULL,
    period_number int,
    name varchar(50),
    period_type varchar(32) NOT NULL DEFAULT 'normal',
    closed_at timestamptz,
    closed_by uuid,
    UNIQUE (company_id, id),
    CONSTRAINT uq_fiscal_period_code UNIQUE (year_id, code),
    CONSTRAINT chk_period_state CHECK (state IN ('open', 'soft_closed', 'hard_closed')),
    CONSTRAINT chk_period_dates CHECK (start_date <= end_date),
    CONSTRAINT chk_period_closed CHECK (
        (state = 'open' AND closed_at IS NULL) OR
        (state IN ('soft_closed', 'hard_closed') AND closed_at IS NOT NULL)
    ),
    FOREIGN KEY (company_id, year_id) REFERENCES fiscal_years(company_id, id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS journals (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    code varchar(32) NOT NULL,
    name varchar(100) NOT NULL DEFAULT 'General journal',
    current_sequence bigint NOT NULL DEFAULT 0,
    UNIQUE (company_id, id),
    CONSTRAINT uq_journals_code UNIQUE (company_id, code),
    CONSTRAINT chk_journal_current_sequence CHECK (current_sequence >= 0)
);

CREATE TABLE IF NOT EXISTS account_moves (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    journal_id uuid NOT NULL,
    period_id uuid,
    entry_date timestamptz NOT NULL,
    entry_number bigint NOT NULL DEFAULT 0,
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
    CONSTRAINT chk_move_status CHECK (status IN ('draft', 'posted', 'reversed')),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, journal_id) REFERENCES journals(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, period_id) REFERENCES fiscal_periods(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, reversed_move_id) REFERENCES account_moves(company_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_move_journal_number
    ON account_moves(company_id, journal_id, entry_number) WHERE entry_number > 0;
CREATE INDEX IF NOT EXISTS account_moves_entry_date_idx ON account_moves(entry_date) WHERE status = 'posted';
CREATE INDEX IF NOT EXISTS account_moves_journal_idx ON account_moves(journal_id, id);

CREATE TABLE IF NOT EXISTS account_move_lines (
    move_id uuid NOT NULL,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    line_id uuid NOT NULL,
    account_id uuid NOT NULL,
    partner_id uuid,
    cost_center_id uuid,
    project_id uuid,
    segment_id uuid,
    business_unit_id uuid,
    matching_number varchar(64),
    reconciled_amount numeric(28,4) NOT NULL DEFAULT 0,
    debit numeric(28,4) NOT NULL DEFAULT 0,
    credit numeric(28,4) NOT NULL DEFAULT 0,
    amount_currency numeric(28,4),
    currency_id varchar(3) NOT NULL DEFAULT 'SAR' REFERENCES currencies(code) ON DELETE RESTRICT,
    transaction_amount_minor bigint NOT NULL DEFAULT 0,
    transaction_currency varchar(3) NOT NULL DEFAULT 'SAR' REFERENCES currencies(code) ON DELETE RESTRICT,
    exchange_rate numeric(18,8) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
    functional_debit_minor bigint NOT NULL DEFAULT 0 CHECK (functional_debit_minor >= 0),
    functional_credit_minor bigint NOT NULL DEFAULT 0 CHECK (functional_credit_minor >= 0),
    line_data jsonb NOT NULL,
    CONSTRAINT chk_line_reconciled_amount CHECK (reconciled_amount >= 0),
    CONSTRAINT chk_line_debit_positive CHECK (debit >= 0),
    CONSTRAINT chk_line_credit_positive CHECK (credit >= 0),
    CONSTRAINT chk_line_has_value CHECK (debit > 0 OR credit > 0),
    CONSTRAINT chk_line_not_both_sides CHECK (NOT (debit > 0 AND credit > 0)),
    CONSTRAINT chk_functional_line_one_sided CHECK (
        (functional_debit_minor > 0 AND functional_credit_minor = 0) OR
        (functional_credit_minor > 0 AND functional_debit_minor = 0)
    ),
    PRIMARY KEY (move_id, line_id),
    FOREIGN KEY (company_id, move_id) REFERENCES account_moves(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS account_move_lines_account_date_idx ON account_move_lines(account_id, move_id);
CREATE INDEX IF NOT EXISTS account_move_lines_matching_idx ON account_move_lines(matching_number) WHERE matching_number IS NOT NULL;

CREATE OR REPLACE FUNCTION prevent_posted_line_mutation() RETURNS trigger AS $$
DECLARE
    move_status varchar(16);
    parent_move_id uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        parent_move_id := OLD.move_id;
    ELSE
        parent_move_id := NEW.move_id;
    END IF;
    SELECT status INTO move_status FROM account_moves WHERE id = parent_move_id;
    IF move_status IS DISTINCT FROM 'draft' THEN
        RAISE EXCEPTION 'journal lines for posted entries are immutable';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_immutable_posted_lines ON account_move_lines;
CREATE TRIGGER trg_immutable_posted_lines
    BEFORE INSERT OR UPDATE OR DELETE ON account_move_lines
    FOR EACH ROW EXECUTE FUNCTION prevent_posted_line_mutation();

CREATE TABLE IF NOT EXISTS subledger_mappings (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    event_type varchar(128) NOT NULL,
    debit_account_id uuid NOT NULL,
    credit_account_id uuid NOT NULL,
    journal_id uuid NOT NULL,
    PRIMARY KEY (company_id, event_type),
    FOREIGN KEY (company_id, debit_account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, credit_account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, journal_id) REFERENCES journals(company_id, id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS subledger_events (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    idempotency_key varchar(255) NOT NULL,
    event jsonb NOT NULL,
    PRIMARY KEY (company_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS subledger_open_lines (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    partner_id uuid,
    kind varchar(32) NOT NULL,
    line_data jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS subledger_open_lines_partner_idx ON subledger_open_lines(partner_id, kind);

CREATE TABLE IF NOT EXISTS fixed_assets (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    asset_data jsonb NOT NULL,
    UNIQUE (company_id, id)
);
CREATE INDEX IF NOT EXISTS idx_fixed_assets_company ON fixed_assets(company_id, id);

CREATE TABLE IF NOT EXISTS fixed_asset_depreciations (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    asset_id uuid NOT NULL,
    period_id uuid NOT NULL,
    amount numeric(28,4) NOT NULL,
    move_id uuid,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (asset_id, period_id),
    CONSTRAINT chk_asset_depreciation_amount CHECK (amount >= 0),
    FOREIGN KEY (company_id, asset_id) REFERENCES fixed_assets(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, period_id) REFERENCES fiscal_periods(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, move_id) REFERENCES account_moves(company_id, id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS bank_items (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL,
    item_id uuid PRIMARY KEY,
    item_data jsonb NOT NULL,
    UNIQUE (company_id, item_id),
    FOREIGN KEY (company_id, account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS bank_items_account_idx ON bank_items(account_id);

CREATE TABLE IF NOT EXISTS exchange_rates (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    from_currency varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    to_currency varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    rate numeric(28,10) NOT NULL,
    effective_date date NOT NULL,
    rate_type varchar(16) NOT NULL,
    PRIMARY KEY (company_id, from_currency, to_currency, effective_date, rate_type),
    CONSTRAINT chk_exchange_rate_positive CHECK (rate > 0)
);

CREATE TABLE IF NOT EXISTS financial_audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
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
CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_chain_previous_hash
    ON financial_audit_logs(company_id, previous_log_hash);
CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_chain_current_hash
    ON financial_audit_logs(company_id, current_log_hash);

CREATE TABLE IF NOT EXISTS accounting_configurations (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    retained_earnings_account_id uuid NOT NULL,
    fx_gain_account_id uuid NOT NULL,
    fx_loss_account_id uuid NOT NULL,
    fx_reconciliation_journal_id uuid NOT NULL,
    functional_currency varchar(3) NOT NULL DEFAULT 'SAR' REFERENCES currencies(code) ON DELETE RESTRICT,
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    FOREIGN KEY (company_id, retained_earnings_account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, fx_gain_account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, fx_loss_account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, fx_reconciliation_journal_id) REFERENCES journals(company_id, id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_accounting_configurations_singleton
    ON accounting_configurations(company_id);

CREATE TABLE IF NOT EXISTS account_reconciliations (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    date date NOT NULL,
    account_id uuid NOT NULL,
    currency_id varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    realized_fx_move_id uuid,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, realized_fx_move_id) REFERENCES account_moves(company_id, id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS account_reconciliation_items (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    reconciliation_id uuid NOT NULL,
    move_id uuid NOT NULL,
    move_line_id uuid NOT NULL,
    allocated_amount numeric(28,4) NOT NULL,
    currency_id varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_reconciliation_line UNIQUE (reconciliation_id, move_id, move_line_id),
    CONSTRAINT chk_reconciliation_allocated_amount CHECK (allocated_amount > 0),
    FOREIGN KEY (company_id, reconciliation_id) REFERENCES account_reconciliations(company_id, id) ON DELETE CASCADE,
    FOREIGN KEY (company_id, move_id) REFERENCES account_moves(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (move_id, move_line_id) REFERENCES account_move_lines(move_id, line_id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS bank_reconciliations (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL,
    statement_item_id uuid NOT NULL,
    ledger_move_line_id uuid NOT NULL,
    matched_date date NOT NULL,
    value_date date NOT NULL,
    tolerance_days integer NOT NULL CHECK (tolerance_days BETWEEN 1 AND 5),
    status varchar(16) NOT NULL CHECK (status IN ('matched', 'unmatched')),
    created_at timestamptz NOT NULL DEFAULT NOW(),
    FOREIGN KEY (company_id, account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, statement_item_id) REFERENCES bank_items(company_id, item_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_bank_reconciliations_company
    ON bank_reconciliations(company_id, account_id, matched_date);
