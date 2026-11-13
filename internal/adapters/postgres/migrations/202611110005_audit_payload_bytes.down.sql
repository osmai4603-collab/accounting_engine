ALTER TABLE financial_audit_logs
    ALTER COLUMN payload_before TYPE jsonb
        USING CASE WHEN payload_before IS NULL THEN NULL
                   ELSE convert_from(payload_before, 'UTF8')::jsonb END,
    ALTER COLUMN payload_after TYPE jsonb
        USING CASE WHEN payload_after IS NULL THEN NULL
                   ELSE convert_from(payload_after, 'UTF8')::jsonb END;
