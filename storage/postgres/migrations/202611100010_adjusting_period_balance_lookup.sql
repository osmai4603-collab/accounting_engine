CREATE INDEX IF NOT EXISTS idx_account_moves_adjusting_period
    ON account_moves(company_id, period_id)
    WHERE classification='adjusting' AND status IN ('posted','reversed');
