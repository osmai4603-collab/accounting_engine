-- P4-04: an original entry can have at most one reversal. The engine already
-- links the pair transactionally, but this partial unique index is the database
-- backstop that prevents a second reversing entry from being persisted for the
-- same original under any race or direct SQL path. If a pre-existing database
-- already contains duplicate reversal rows the CREATE INDEX fails loudly; the
-- duplicates must be investigated and remediated manually, never deleted by a
-- migration.
CREATE UNIQUE INDEX IF NOT EXISTS uq_account_moves_reversal_original
    ON account_moves (company_id, (entry->>'OriginalEntryID'))
    WHERE classification = 'reversing'
      AND NULLIF(entry->>'OriginalEntryID', '') IS NOT NULL;
