-- Fix posted-move balance trigger to verify functional minor-unit columns
-- instead of the deprecated numeric debit/credit compatibility columns.
--
-- The trigger must guarantee double-entry balance in the company functional
-- currency using functional_debit_minor / functional_credit_minor, which are
-- the authoritative posted amounts. Multi-currency entries balanced
-- functionally (e.g. EUR debit vs USD credit both translated to SAR) must be
-- accepted, while entries whose functional totals do not balance must be
-- rejected at the database boundary.

CREATE OR REPLACE FUNCTION check_posted_move_balance_statement() RETURNS trigger AS $$
DECLARE
    unbalanced_move record;
BEGIN
    SELECT posted_moves.id,
           posted_moves.entry->>'Status' AS entry_status,
           COALESCE(SUM(lines.functional_debit_minor), 0) AS total_debit,
           COALESCE(SUM(lines.functional_credit_minor), 0) AS total_credit
    INTO unbalanced_move
    FROM new_rows AS posted_moves
    JOIN old_rows AS prior_moves ON prior_moves.id = posted_moves.id
    LEFT JOIN account_move_lines AS lines ON lines.move_id = posted_moves.id
    WHERE prior_moves.status = 'draft'
      AND posted_moves.status = 'posted'
    GROUP BY posted_moves.id, posted_moves.entry->>'Status'
    HAVING posted_moves.entry->>'Status' IS DISTINCT FROM 'posted'
        OR COALESCE(SUM(lines.functional_debit_minor), 0) <> COALESCE(SUM(lines.functional_credit_minor), 0)
    LIMIT 1;

    IF FOUND THEN
        IF unbalanced_move.entry_status IS DISTINCT FROM 'posted' THEN
            RAISE EXCEPTION
                'Move % has inconsistent status in entry JSON: %',
                unbalanced_move.id, unbalanced_move.entry_status
                USING ERRCODE = 'check_violation';
        END IF;
        RAISE EXCEPTION
            'Move % is unbalanced: total_debit=% != total_credit=%',
            unbalanced_move.id, unbalanced_move.total_debit, unbalanced_move.total_credit
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_check_posted_move_balance_header ON account_moves;
DROP TRIGGER IF EXISTS trg_check_posted_move_balance_statement ON account_moves;
CREATE TRIGGER trg_check_posted_move_balance_statement
    AFTER UPDATE ON account_moves
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION check_posted_move_balance_statement();
