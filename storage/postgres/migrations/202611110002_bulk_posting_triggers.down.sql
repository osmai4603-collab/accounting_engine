-- Revert to the per-row balance maintenance trigger (202611100009) and the
-- per-statement balance check prior to the set-based rewrite (202611110001).

CREATE OR REPLACE FUNCTION update_account_balance_on_posting()
RETURNS trigger AS $$
BEGIN
    IF NEW.period_id IS NULL THEN
        RAISE EXCEPTION 'Posted move % has no fiscal period', NEW.id
            USING ERRCODE = 'check_violation';
    END IF;

    INSERT INTO account_balances (
        company_id, account_id, fiscal_period_id, currency_id, period_debit, period_credit
    )
    SELECT
        lines.company_id,
        lines.account_id,
        NEW.period_id,
        company.functional_currency,
        SUM(lines.functional_debit_minor::numeric / power(10::numeric, currency.minor_units)),
        SUM(lines.functional_credit_minor::numeric / power(10::numeric, currency.minor_units))
    FROM account_move_lines lines
    JOIN companies company ON company.id = lines.company_id
    JOIN currencies currency ON currency.code = company.functional_currency
    WHERE lines.move_id = NEW.id
    GROUP BY lines.company_id, lines.account_id, company.functional_currency, currency.minor_units
    ON CONFLICT (company_id, account_id, fiscal_period_id, currency_id)
    DO UPDATE SET
        period_debit = account_balances.period_debit + EXCLUDED.period_debit,
        period_credit = account_balances.period_credit + EXCLUDED.period_credit,
        version = account_balances.version + 1,
        updated_at = now();

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_update_balances_on_posting ON account_moves;
CREATE TRIGGER trg_update_balances_on_posting
    AFTER UPDATE ON account_moves
    FOR EACH ROW
    WHEN (OLD.status = 'draft' AND NEW.status = 'posted')
    EXECUTE FUNCTION update_account_balance_on_posting();

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

DROP TRIGGER IF EXISTS trg_check_posted_move_balance_statement ON account_moves;
CREATE TRIGGER trg_check_posted_move_balance_statement
    AFTER UPDATE ON account_moves
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION check_posted_move_balance_statement();
