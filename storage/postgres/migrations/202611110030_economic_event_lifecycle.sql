CREATE TABLE IF NOT EXISTS subledger_events (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    idempotency_key varchar(255) NOT NULL,
    event jsonb NOT NULL,
    PRIMARY KEY (company_id, idempotency_key)
);

ALTER TABLE subledger_events
    ADD COLUMN IF NOT EXISTS event_id uuid,
    ADD COLUMN IF NOT EXISTS source_system varchar(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_id varchar(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS event_type varchar(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS event_version integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS payload_bytes bytea NOT NULL DEFAULT ''::bytea,
    ADD COLUMN IF NOT EXISTS status varchar(32) NOT NULL DEFAULT 'received',
    ADD COLUMN IF NOT EXISTS generated_entry_id uuid,
    ADD COLUMN IF NOT EXISTS error_message text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS processed_at timestamptz;

UPDATE subledger_events
SET event_id = COALESCE(NULLIF(event->>'EventID', '')::uuid, gen_random_uuid()),
    source_system = COALESCE(event->>'SourceSystem', ''),
    external_id = COALESCE(event->>'ExternalID', ''),
    event_type = COALESCE(event->>'EventType', ''),
    event_version = COALESCE(NULLIF(event->>'EventVersion', '')::integer, 0),
    payload_bytes = CASE
        WHEN COALESCE(event->>'PayloadJSON', '') = '' THEN ''::bytea
        ELSE decode(event->>'PayloadJSON', 'base64')
    END,
    generated_entry_id = COALESCE(
        NULLIF(event->>'GeneratedEntryID', '')::uuid,
        NULLIF(event->>'ResultEntryID', '')::uuid
    ),
    status = CASE
        WHEN event->>'Status' IN ('received', 'processed', 'rejected', 'dead_letter')
            THEN event->>'Status'
        WHEN COALESCE(NULLIF(event->>'ResultEntryID', '')::uuid, NULL) IS NOT NULL
            THEN 'processed'
        ELSE 'received'
    END,
    error_message = COALESCE(event->>'ErrorMessage', ''),
    created_at = COALESCE(NULLIF(event->>'CreatedAt', '')::timestamptz, created_at),
    processed_at = NULLIF(event->>'ProcessedAt', '')::timestamptz
WHERE event_id IS NULL;

ALTER TABLE subledger_events
    ALTER COLUMN event_id SET NOT NULL,
    ADD CONSTRAINT chk_subledger_event_status
        CHECK (status IN ('received', 'processed', 'rejected', 'dead_letter'));

CREATE UNIQUE INDEX IF NOT EXISTS uq_subledger_events_event_id
    ON subledger_events(event_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_subledger_events_company_event_id
    ON subledger_events(company_id, event_id);

DO $$
DECLARE
    primary_key_name text;
BEGIN
    SELECT constraint_name
    INTO primary_key_name
    FROM information_schema.table_constraints
    WHERE table_schema = current_schema()
      AND table_name = 'subledger_events'
      AND constraint_type = 'PRIMARY KEY';

    IF primary_key_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE subledger_events DROP CONSTRAINT %I', primary_key_name);
    END IF;
END;
$$;

ALTER TABLE subledger_events
    ADD CONSTRAINT uq_subledger_events_idempotency
        UNIQUE (company_id, source_system, idempotency_key);

UPDATE subledger_events
SET event = jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(event, '{EventID}', to_jsonb(event_id), true),
                '{SourceSystem}', to_jsonb(source_system), true
            ),
            '{Status}', to_jsonb(status), true
        ),
        '{PayloadJSON}', to_jsonb(encode(payload_bytes, 'base64')), true
    );

ALTER TABLE account_moves
    ADD COLUMN IF NOT EXISTS source_event_id uuid;

UPDATE account_moves AS moves
SET source_event_id = events.event_id
FROM subledger_events AS events
WHERE events.company_id = moves.company_id
  AND events.generated_entry_id = moves.id
  AND moves.source_event_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_account_moves_source_event
    ON account_moves(company_id, source_event_id)
    WHERE source_event_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_subledger_events_status_created
    ON subledger_events(company_id, status, created_at);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'fk_account_moves_source_event'
          AND conrelid = 'account_moves'::regclass
    ) THEN
        ALTER TABLE account_moves
            ADD CONSTRAINT fk_account_moves_source_event
            FOREIGN KEY (company_id, source_event_id)
            REFERENCES subledger_events(company_id, event_id)
            ON DELETE RESTRICT
            DEFERRABLE INITIALLY DEFERRED;
    END IF;
END;
$$;
