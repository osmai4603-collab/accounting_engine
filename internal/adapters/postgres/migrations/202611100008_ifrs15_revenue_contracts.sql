CREATE TABLE IF NOT EXISTS ifrs15_revenue_contracts (
    id uuid NOT NULL,
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    customer_id uuid NOT NULL,
    currency varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    transaction_price numeric(28, 4) NOT NULL,
    contract_data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (company_id, id)
);

CREATE INDEX IF NOT EXISTS idx_ifrs15_contracts_customer
    ON ifrs15_revenue_contracts(company_id, customer_id);

ALTER TABLE ifrs15_revenue_contracts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ifrs15_revenue_contracts FORCE ROW LEVEL SECURITY;

CREATE POLICY ifrs15_revenue_contracts_tenant_isolation ON ifrs15_revenue_contracts
    FOR ALL
    USING (company_id = NULLIF(current_setting('app.current_company_id', true), '')::uuid)
    WITH CHECK (company_id = NULLIF(current_setting('app.current_company_id', true), '')::uuid);

