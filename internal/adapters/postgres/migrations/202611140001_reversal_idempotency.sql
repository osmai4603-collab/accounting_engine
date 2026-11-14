CREATE UNIQUE INDEX IF NOT EXISTS uq_account_moves_reversal_idempotency
    ON account_moves (company_id, (entry->>'idempotency_key'))
    WHERE classification = 'reversing'
      AND NULLIF(entry->>'idempotency_key', '') IS NOT NULL;
