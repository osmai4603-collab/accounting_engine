-- The audit chain hash is computed over the exact serialized payload bytes.
-- jsonb normalizes key order and whitespace, which made persisted rows
-- unverifiable after a round trip, so store the payload as immutable bytes.
ALTER TABLE financial_audit_logs
    ALTER COLUMN payload_before TYPE bytea
        USING CASE WHEN payload_before IS NULL THEN NULL
                   ELSE convert_to(payload_before::text, 'UTF8') END,
    ALTER COLUMN payload_after TYPE bytea
        USING CASE WHEN payload_after IS NULL THEN NULL
                   ELSE convert_to(payload_after::text, 'UTF8') END;
