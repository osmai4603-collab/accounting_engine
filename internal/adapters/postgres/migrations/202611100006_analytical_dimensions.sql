CREATE TABLE IF NOT EXISTS financial_dimension_values (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    kind varchar(32) NOT NULL CHECK (kind IN ('project', 'segment', 'business_unit')),
    id uuid NOT NULL,
    code varchar(64) NOT NULL,
    name varchar(255) NOT NULL,
    is_active boolean NOT NULL DEFAULT TRUE,
    PRIMARY KEY (company_id, kind, id),
    UNIQUE (company_id, kind, code)
);

ALTER TABLE financial_dimension_values ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON financial_dimension_values;
CREATE POLICY tenant_isolation_policy ON financial_dimension_values
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
