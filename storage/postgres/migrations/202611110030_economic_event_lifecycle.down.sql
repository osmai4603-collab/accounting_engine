ALTER TABLE account_moves DROP CONSTRAINT IF EXISTS fk_account_moves_source_event;
DROP INDEX IF EXISTS uq_account_moves_source_event;
DROP INDEX IF EXISTS idx_account_moves_source_event;
ALTER TABLE account_moves DROP COLUMN IF EXISTS source_event_id;

DROP INDEX IF EXISTS idx_subledger_events_status_created;
ALTER TABLE subledger_events
    DROP CONSTRAINT IF EXISTS chk_subledger_event_status,
    DROP CONSTRAINT IF EXISTS uq_subledger_events_idempotency;
DROP INDEX IF EXISTS uq_subledger_events_event_id;
DROP INDEX IF EXISTS uq_subledger_events_company_event_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM subledger_events
        GROUP BY company_id, idempotency_key
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION
            'Cannot roll back economic event lifecycle: source systems contain duplicate company/idempotency keys';
    END IF;
    ALTER TABLE subledger_events
        ADD CONSTRAINT subledger_events_pkey PRIMARY KEY (company_id, idempotency_key);
END;
$$;

ALTER TABLE subledger_events
    DROP COLUMN IF EXISTS event_id,
    DROP COLUMN IF EXISTS source_system,
    DROP COLUMN IF EXISTS external_id,
    DROP COLUMN IF EXISTS event_type,
    DROP COLUMN IF EXISTS event_version,
    DROP COLUMN IF EXISTS payload_bytes,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS generated_entry_id,
    DROP COLUMN IF EXISTS error_message,
    DROP COLUMN IF EXISTS created_at,
    DROP COLUMN IF EXISTS processed_at;
