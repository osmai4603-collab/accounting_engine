-- Phase 2: bind approval requests to the immutable content and functional amount
-- of the journal entry they approve, and persist company-level governance policy.

ALTER TABLE approval_requests
    ADD COLUMN IF NOT EXISTS entry_hash varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS functional_amount numeric(28,4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS functional_currency varchar(3) NOT NULL DEFAULT '';

-- Backfill existing requests from the values captured at request creation time.
UPDATE approval_requests
SET functional_amount = amount,
    functional_currency = currency
WHERE functional_currency = '';

ALTER TABLE approval_requests
    ADD CONSTRAINT approval_requests_functional_amount_positive CHECK (functional_amount > 0),
    ADD CONSTRAINT approval_requests_entry_hash_sha256 CHECK (entry_hash = '' OR length(entry_hash) = 64),
    ADD CONSTRAINT approval_requests_functional_currency_fk
        FOREIGN KEY (functional_currency) REFERENCES currencies(code) ON DELETE RESTRICT;

-- A single entry may only ever have one approval request; conflicting duplicate
-- requests are rejected by the database rather than silently overwriting.
CREATE UNIQUE INDEX IF NOT EXISTS uq_approval_requests_company_entry
    ON approval_requests(company_id, entry_id);

CREATE TABLE IF NOT EXISTS governance_policies (
    company_id uuid PRIMARY KEY REFERENCES companies(id) ON DELETE RESTRICT,
    maker_checker_threshold numeric(28,4) NOT NULL DEFAULT 0
        CHECK (maker_checker_threshold >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE governance_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE governance_policies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON governance_policies;
CREATE POLICY tenant_isolation_policy ON governance_policies
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
