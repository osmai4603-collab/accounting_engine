CREATE TABLE IF NOT EXISTS public_event_ingestions (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    idempotency_key varchar(255) NOT NULL,
    event_type varchar(128) NOT NULL DEFAULT '',
    payload_hash varchar(64) NOT NULL DEFAULT '',
    entry_id uuid,
    entry_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    status varchar(32) NOT NULL DEFAULT 'processed',
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (company_id, idempotency_key),
    CONSTRAINT chk_public_event_ingestion_status CHECK (status IN ('processing', 'processed', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_public_event_ingestions_entry
    ON public_event_ingestions(company_id, entry_id);

CREATE TABLE IF NOT EXISTS public_event_account_mappings (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    event_type varchar(128) NOT NULL,
    debit_account_code varchar(64) NOT NULL,
    credit_account_code varchar(64) NOT NULL,
    description_template varchar(255) NOT NULL DEFAULT '',
    journal_code varchar(64) NOT NULL DEFAULT '',
    ledger_id varchar(64) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT NOW(),
    PRIMARY KEY (company_id, event_type, ledger_id)
);

DO $$
DECLARE relation_name text;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY['public_event_ingestions', 'public_event_account_mappings'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', relation_name);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation_policy ON %I', relation_name);
        EXECUTE format(
            'CREATE POLICY tenant_isolation_policy ON %I USING (company_id = app_current_company_id()) WITH CHECK (company_id = app_current_company_id())',
            relation_name
        );
    END LOOP;
END;
$$;
