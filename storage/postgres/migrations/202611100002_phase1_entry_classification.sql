ALTER TABLE account_moves
    ADD COLUMN IF NOT EXISTS classification varchar(16) NOT NULL DEFAULT 'regular';

ALTER TABLE account_moves
    DROP CONSTRAINT IF EXISTS chk_move_classification;

ALTER TABLE account_moves
    ADD CONSTRAINT chk_move_classification
    CHECK (classification IN ('regular', 'adjusting', 'closing', 'opening', 'reversing'));
