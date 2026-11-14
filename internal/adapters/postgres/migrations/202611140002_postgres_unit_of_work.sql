CREATE TABLE IF NOT EXISTS posting_idempotency (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    idempotency_key varchar(255) NOT NULL,
    operation varchar(64) NOT NULL,
    payload_hash varchar(128) NOT NULL,
    status varchar(32) NOT NULL,
    entry_id uuid,
    response bytea,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, idempotency_key),
    FOREIGN KEY (company_id, entry_id) REFERENCES account_moves(company_id, id) ON DELETE RESTRICT,
    CHECK (length(idempotency_key) > 0),
    CHECK (length(operation) > 0),
    CHECK (length(payload_hash) > 0),
    CHECK (status IN ('started', 'completed', 'retryable_failure', 'terminal_failure')),
    CHECK ((status = 'completed' AND entry_id IS NOT NULL) OR status <> 'completed')
);

ALTER TABLE posting_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE posting_idempotency FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON posting_idempotency;
CREATE POLICY tenant_isolation_policy ON posting_idempotency
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
