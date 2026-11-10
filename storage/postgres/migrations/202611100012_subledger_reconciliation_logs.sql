CREATE TABLE IF NOT EXISTS subledger_reconciliation_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    domain varchar(32) NOT NULL,
    control_account_id uuid NOT NULL,
    as_of_date date NOT NULL,
    functional_curr varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    subledger_balance numeric(28,4) NOT NULL,
    gl_balance numeric(28,4) NOT NULL,
    difference numeric(28,4) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('balanced', 'discrepancy')),
    details jsonb NOT NULL DEFAULT '{}',
    certified_by varchar(64) NOT NULL DEFAULT 'SYSTEM_DAEMON',
    created_at timestamptz NOT NULL DEFAULT NOW(),
    FOREIGN KEY (company_id, control_account_id)
        REFERENCES accounts(company_id, id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_logs_lookup
    ON subledger_reconciliation_logs(company_id, domain, as_of_date);

CREATE INDEX IF NOT EXISTS idx_reconciliation_discrepancies
    ON subledger_reconciliation_logs(company_id, status)
    WHERE status = 'discrepancy';

CREATE TABLE IF NOT EXISTS subledger_reconciliation_certificates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    as_of_date date NOT NULL,
    is_clean boolean NOT NULL,
    details jsonb NOT NULL,
    certified_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_certificates_lookup
    ON subledger_reconciliation_certificates(company_id, as_of_date, certified_at);
