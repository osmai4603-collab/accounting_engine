CREATE TABLE IF NOT EXISTS financial_statement_notes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    report_code varchar(64) NOT NULL,
    statement_date date NOT NULL,
    code varchar(64) NOT NULL,
    title varchar(255) NOT NULL,
    description text NOT NULL DEFAULT '',
    amount numeric(28, 8) NOT NULL,
    currency_id varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, report_code, statement_date, code),
    UNIQUE (company_id, id)
);

CREATE INDEX IF NOT EXISTS idx_financial_statement_notes_report
    ON financial_statement_notes(company_id, report_code, statement_date, code);

ALTER TABLE financial_statement_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE financial_statement_notes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON financial_statement_notes;
CREATE POLICY tenant_isolation_policy ON financial_statement_notes
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
