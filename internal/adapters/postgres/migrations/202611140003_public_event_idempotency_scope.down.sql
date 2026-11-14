-- Reverse P3-04. Restoring the narrower primary key requires that no two
-- source systems share the same key for a company; roll back only after
-- resolving such rows if they exist.

DROP INDEX IF EXISTS idx_public_event_ingestions_source;

ALTER TABLE public_event_ingestions
    DROP CONSTRAINT IF EXISTS chk_public_event_ingestion_failure_class;
ALTER TABLE public_event_ingestions
    DROP CONSTRAINT IF EXISTS chk_public_event_ingestion_status;

-- Map canonical values back to their legacy equivalents before restoring the
-- narrower status domain; otherwise rows written after the upgrade would make
-- the rollback fail.
UPDATE public_event_ingestions
   SET status = CASE status
                    WHEN 'started' THEN 'processing'
                    WHEN 'completed' THEN 'processed'
                    WHEN 'retryable_failure' THEN 'failed'
                    WHEN 'terminal_failure' THEN 'failed'
                    ELSE status
                END
 WHERE status IN ('started', 'completed', 'retryable_failure', 'terminal_failure');

ALTER TABLE public_event_ingestions
    ADD CONSTRAINT chk_public_event_ingestion_status
    CHECK (status IN ('processing', 'processed', 'failed'));

ALTER TABLE public_event_ingestions
    DROP CONSTRAINT IF EXISTS public_event_ingestions_pkey;
ALTER TABLE public_event_ingestions
    ADD CONSTRAINT public_event_ingestions_pkey
    PRIMARY KEY (company_id, idempotency_key);

ALTER TABLE public_event_ingestions
    DROP COLUMN IF EXISTS failure_class,
    DROP COLUMN IF EXISTS payload_hash_version,
    DROP COLUMN IF EXISTS source_system;
