ALTER TABLE account_moves
    DROP CONSTRAINT IF EXISTS chk_move_classification;

ALTER TABLE account_moves
    DROP COLUMN IF EXISTS classification;
