CREATE TABLE subledger_reconciliation_exceptions (
    id uuid PRIMARY KEY,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    domain varchar(32) NOT NULL,
    control_account_id uuid NOT NULL,
    as_of_date date NOT NULL,
    functional_curr varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    subledger_balance numeric(28,4) NOT NULL,
    gl_balance numeric(28,4) NOT NULL,
    difference numeric(28,4) NOT NULL CHECK (difference <> 0),
    status varchar(16) NOT NULL CHECK (status IN ('unmatched', 'matching', 'disputed', 'resolved')),
    actor_id uuid NOT NULL,
    reason text NOT NULL CHECK (length(trim(reason)) > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (company_id, id),
    FOREIGN KEY (company_id, control_account_id)
        REFERENCES accounts(company_id, id) ON DELETE RESTRICT
);

CREATE INDEX idx_subledger_reconciliation_exceptions_lookup
    ON subledger_reconciliation_exceptions(company_id, status, as_of_date);

CREATE TABLE subledger_reconciliation_exception_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL,
    exception_id uuid NOT NULL,
    from_status varchar(16) CHECK (from_status IS NULL OR from_status IN ('unmatched', 'matching', 'disputed', 'resolved')),
    to_status varchar(16) NOT NULL CHECK (to_status IN ('unmatched', 'matching', 'disputed', 'resolved')),
    actor_id uuid NOT NULL,
    reason text NOT NULL CHECK (length(trim(reason)) > 0),
    changed_at timestamptz NOT NULL,
    FOREIGN KEY (company_id, exception_id)
        REFERENCES subledger_reconciliation_exceptions(company_id, id) ON DELETE RESTRICT
);

CREATE INDEX idx_subledger_reconciliation_exception_events_history
    ON subledger_reconciliation_exception_events(company_id, exception_id, changed_at, id);

ALTER TABLE subledger_reconciliation_exceptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE subledger_reconciliation_exceptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON subledger_reconciliation_exceptions
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

ALTER TABLE subledger_reconciliation_exception_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE subledger_reconciliation_exception_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON subledger_reconciliation_exception_events
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

ALTER TABLE subledger_reconciliation_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE subledger_reconciliation_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON subledger_reconciliation_logs
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());

ALTER TABLE subledger_reconciliation_certificates ENABLE ROW LEVEL SECURITY;
ALTER TABLE subledger_reconciliation_certificates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON subledger_reconciliation_certificates
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
