DROP POLICY IF EXISTS tenant_isolation_policy ON governance_policies;
ALTER TABLE governance_policies NO FORCE ROW LEVEL SECURITY;
ALTER TABLE governance_policies DISABLE ROW LEVEL SECURITY;
DROP TABLE IF EXISTS governance_policies;

DROP INDEX IF EXISTS uq_approval_requests_company_entry;

ALTER TABLE approval_requests
    DROP CONSTRAINT IF EXISTS approval_requests_functional_currency_fk,
    DROP CONSTRAINT IF EXISTS approval_requests_entry_hash_sha256,
    DROP CONSTRAINT IF EXISTS approval_requests_functional_amount_positive,
    DROP COLUMN IF EXISTS functional_currency,
    DROP COLUMN IF EXISTS functional_amount,
    DROP COLUMN IF EXISTS entry_hash;
