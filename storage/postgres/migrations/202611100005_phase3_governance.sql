ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS requires_cost_center boolean NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS account_versions (
    company_id uuid NOT NULL,
    account_id uuid NOT NULL,
    name varchar(255) NOT NULL,
    account_type varchar(50) NOT NULL,
    ifrs_category varchar(30) NOT NULL,
    effective_from date NOT NULL,
    effective_to date,
    PRIMARY KEY (company_id, account_id, effective_from),
    FOREIGN KEY (company_id, account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    CHECK (effective_to IS NULL OR effective_to > effective_from)
);
CREATE INDEX IF NOT EXISTS account_versions_as_of_idx
    ON account_versions(company_id, account_id, effective_from, effective_to);

CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER TABLE account_versions
    ADD CONSTRAINT account_versions_no_overlap
    EXCLUDE USING gist (
        company_id WITH =,
        account_id WITH =,
        daterange(effective_from, effective_to, '[)') WITH &&
    );

CREATE TABLE IF NOT EXISTS cost_centers (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    parent_id uuid,
    code varchar(64) NOT NULL,
    name varchar(255) NOT NULL,
    is_active boolean NOT NULL DEFAULT TRUE,
    UNIQUE (company_id, id),
    UNIQUE (company_id, code),
    FOREIGN KEY (company_id, parent_id) REFERENCES cost_centers(company_id, id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS governance_roles (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    role varchar(64) NOT NULL,
    PRIMARY KEY (company_id, role)
);

CREATE TABLE IF NOT EXISTS user_role_assignments (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    user_id uuid NOT NULL,
    role varchar(64) NOT NULL,
    granted_by uuid NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, user_id, role),
    FOREIGN KEY (company_id, role) REFERENCES governance_roles(company_id, role) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS approval_requests (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    entry_id uuid NOT NULL,
    journal_type varchar(64) NOT NULL DEFAULT '',
    creator_id uuid NOT NULL,
    amount numeric(28,4) NOT NULL CHECK (amount > 0),
    currency varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    UNIQUE (company_id, id)
);

CREATE TABLE IF NOT EXISTS approval_steps (
    company_id uuid NOT NULL,
    approval_id uuid NOT NULL,
    sequence integer NOT NULL CHECK (sequence > 0),
    required_role varchar(64) NOT NULL,
    approved_by uuid,
    approved_at timestamptz,
    PRIMARY KEY (company_id, approval_id, sequence),
    FOREIGN KEY (company_id, approval_id) REFERENCES approval_requests(company_id, id) ON DELETE RESTRICT,
    CHECK ((approved_by IS NULL) = (approved_at IS NULL))
);

CREATE OR REPLACE FUNCTION prevent_financial_audit_log_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'financial_audit_logs is append-only'
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_financial_audit_logs_no_update ON financial_audit_logs;
CREATE TRIGGER trg_financial_audit_logs_no_update
    BEFORE UPDATE ON financial_audit_logs
    FOR EACH ROW EXECUTE FUNCTION prevent_financial_audit_log_mutation();
DROP TRIGGER IF EXISTS trg_financial_audit_logs_no_delete ON financial_audit_logs;
CREATE TRIGGER trg_financial_audit_logs_no_delete
    BEFORE DELETE ON financial_audit_logs
    FOR EACH ROW EXECUTE FUNCTION prevent_financial_audit_log_mutation();
DROP TRIGGER IF EXISTS trg_financial_audit_logs_no_truncate ON financial_audit_logs;
CREATE TRIGGER trg_financial_audit_logs_no_truncate
    BEFORE TRUNCATE ON financial_audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION prevent_financial_audit_log_mutation();
REVOKE UPDATE, DELETE, TRUNCATE ON financial_audit_logs FROM PUBLIC;

CREATE OR REPLACE FUNCTION app_current_company_id() RETURNS uuid AS $$
    SELECT NULLIF(current_setting('app.current_company_id', true), '')::uuid
$$ LANGUAGE sql STABLE;

DO $$
DECLARE
    relation_name text;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY[
        'companies', 'accounts', 'fiscal_years', 'fiscal_periods', 'journals',
        'account_moves', 'account_move_lines', 'subledger_mappings', 'event_accounting_rules',
        'subledger_events', 'subledger_open_lines', 'fixed_assets', 'fixed_asset_depreciations',
        'bank_items', 'exchange_rates', 'financial_audit_logs', 'accounting_configurations',
        'account_reconciliations', 'account_reconciliation_items', 'bank_reconciliations',
        'account_versions', 'cost_centers', 'governance_roles', 'user_role_assignments',
        'approval_requests', 'approval_steps'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', relation_name);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation_policy ON %I', relation_name);
        IF relation_name = 'companies' THEN
            EXECUTE format(
                'CREATE POLICY tenant_isolation_policy ON %I USING (id = app_current_company_id()) WITH CHECK (id = app_current_company_id())',
                relation_name
            );
        ELSE
            EXECUTE format(
                'CREATE POLICY tenant_isolation_policy ON %I USING (company_id = app_current_company_id()) WITH CHECK (company_id = app_current_company_id())',
                relation_name
            );
        END IF;
    END LOOP;
END;
$$;
