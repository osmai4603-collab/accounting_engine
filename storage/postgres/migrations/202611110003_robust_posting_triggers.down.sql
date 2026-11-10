-- Revert to the 202611110002 statement-level trigger bodies, which join the
-- transition tables directly with account_move_lines.

CREATE OR REPLACE FUNCTION check_posted_move_balance_statement() RETURNS trigger AS $$
DECLARE
    unbalanced_move record;
BEGIN
    SELECT posted_moves.id,
           posted_moves.entry->>'Status' AS entry_status,
           COALESCE(line_totals.total_debit, 0) AS total_debit,
           COALESCE(line_totals.total_credit, 0) AS total_credit
    INTO unbalanced_move
    FROM new_rows AS posted_moves
    JOIN old_rows AS prior_moves ON prior_moves.id = posted_moves.id
    LEFT JOIN (
        SELECT lines.move_id,
               SUM(lines.functional_debit_minor) AS total_debit,
               SUM(lines.functional_credit_minor) AS total_credit
        FROM account_move_lines AS lines
        JOIN new_rows AS batch_moves ON batch_moves.id = lines.move_id
        GROUP BY lines.move_id
    ) AS line_totals ON line_totals.move_id = posted_moves.id
    WHERE prior_moves.status = 'draft'
      AND posted_moves.status = 'posted'
      AND (posted_moves.entry->>'Status' IS DISTINCT FROM 'posted'
           OR COALESCE(line_totals.total_debit, 0) <> COALESCE(line_totals.total_credit, 0))
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

DROP TRIGGER IF EXISTS trg_check_posted_move_balance_statement ON account_moves;
CREATE TRIGGER trg_check_posted_move_balance_statement
    AFTER UPDATE ON account_moves
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION check_posted_move_balance_statement();

CREATE OR REPLACE FUNCTION update_account_balance_on_posting()
RETURNS trigger AS $$
DECLARE
    unperioded_move record;
BEGIN
    SELECT batch_moves.id
    INTO unperioded_move
    FROM new_rows AS batch_moves
    JOIN old_rows AS prior_moves ON prior_moves.id = batch_moves.id
    WHERE prior_moves.status = 'draft'
      AND batch_moves.status = 'posted'
      AND batch_moves.period_id IS NULL
    LIMIT 1;

    IF FOUND THEN
        RAISE EXCEPTION 'Posted move % has no fiscal period', unperioded_move.id
            USING ERRCODE = 'check_violation';
    END IF;

    INSERT INTO account_balances (
        company_id, account_id, fiscal_period_id, currency_id, period_debit, period_credit
    )
    SELECT
        lines.company_id,
        lines.account_id,
        posted.period_id,
        company.functional_currency,
        SUM(lines.functional_debit_minor::numeric / power(10::numeric, currency.minor_units)),
        SUM(lines.functional_credit_minor::numeric / power(10::numeric, currency.minor_units))
    FROM (
        SELECT batch_moves.id AS move_id, batch_moves.period_id
        FROM new_rows AS batch_moves
        JOIN old_rows AS prior_moves ON prior_moves.id = batch_moves.id
        WHERE prior_moves.status = 'draft'
          AND batch_moves.status = 'posted'
    ) AS posted
    JOIN account_move_lines lines ON lines.move_id = posted.move_id
    JOIN companies company ON company.id = lines.company_id
    JOIN currencies currency ON currency.code = company.functional_currency
    GROUP BY lines.company_id, lines.account_id, posted.period_id, company.functional_currency, currency.minor_units
    ON CONFLICT (company_id, account_id, fiscal_period_id, currency_id)
    DO UPDATE SET
        period_debit = account_balances.period_debit + EXCLUDED.period_debit,
        period_credit = account_balances.period_credit + EXCLUDED.period_credit,
        version = account_balances.version + 1,
        updated_at = now();

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_update_balances_on_posting ON account_moves;
CREATE TRIGGER trg_update_balances_on_posting
    AFTER UPDATE ON account_moves
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION update_account_balance_on_posting();
