-- P3-04: durable, source-scoped public event idempotency.
--
-- Adds the source system, normalization version and failure class to each
-- ingestion record, widens the status domain to the canonical lifecycle values
-- (legacy values remain readable), and makes the uniqueness identity include
-- the source system so equal keys from different producers never collide.

ALTER TABLE public_event_ingestions
    ADD COLUMN IF NOT EXISTS source_system varchar(64) NOT NULL DEFAULT 'event',
    ADD COLUMN IF NOT EXISTS payload_hash_version varchar(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS failure_class varchar(32) NOT NULL DEFAULT '';

ALTER TABLE public_event_ingestions
    DROP CONSTRAINT IF EXISTS chk_public_event_ingestion_status;
ALTER TABLE public_event_ingestions
    ADD CONSTRAINT chk_public_event_ingestion_status
    CHECK (status IN ('started', 'completed', 'retryable_failure', 'terminal_failure',
                      'processing', 'processed', 'failed'));

ALTER TABLE public_event_ingestions
    DROP CONSTRAINT IF EXISTS chk_public_event_ingestion_failure_class;
ALTER TABLE public_event_ingestions
    ADD CONSTRAINT chk_public_event_ingestion_failure_class
    CHECK (failure_class IN ('', 'transient', 'terminal'));

-- Replace the (company_id, idempotency_key) primary key with a
-- (company_id, source_system, idempotency_key) identity. The change is
-- reversible and does not drop data.
ALTER TABLE public_event_ingestions
    DROP CONSTRAINT IF EXISTS public_event_ingestions_pkey;
ALTER TABLE public_event_ingestions
    ADD CONSTRAINT public_event_ingestions_pkey
    PRIMARY KEY (company_id, source_system, idempotency_key);

CREATE INDEX IF NOT EXISTS idx_public_event_ingestions_source
    ON public_event_ingestions(company_id, source_system, idempotency_key);

-- Re-assert the tenant isolation policy after the key change (idempotent).
ALTER TABLE public_event_ingestions ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON public_event_ingestions;
CREATE POLICY tenant_isolation_policy ON public_event_ingestions
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
