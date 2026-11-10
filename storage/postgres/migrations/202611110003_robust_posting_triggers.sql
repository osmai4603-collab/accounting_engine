-- Task 1.2 (bulk posting performance & integrity): the statement-level posting
-- triggers written in 202611110002 join the OLD/NEW transition tables with
-- account_move_lines through regular joins. Transition tables ("named
-- tuplestore scans") carry planner estimates that are almost always wrong for
-- filtered scans (a status filter is guessed at ~0.5% selectivity, so a
-- 3000-row batch is estimated at ~15 rows), and the large account_move_lines
-- join side depends on autovacuum freshness. Under those misestimates the
-- planner occasionally chose a nested loop that rescans a wide materialized
-- tuplestore (or a full scan of account_move_lines) once per row of the other
-- side, which turned a ~40ms trigger into a ~4s trigger.
--
-- Both trigger queries are rewritten so every access path is bounded no
-- matter what the planner estimates:
--   * only the NEW transition table is referenced (REFERENCING NEW TABLE),
--     so PostgreSQL never materializes the OLD row versions at all;
--   * the draft->posted transition is detected from new_rows alone. That is
--     sound because trg_prevent_posted_move_mutation (BEFORE ROW, fires
--     before these AFTER STATEMENT triggers) plus chk_move_status make
--     new_rows.status='posted' possible only when the prior status was
--     'draft': posted moves can only transition to 'reversed', reversed
--     moves cannot be modified at all, and inserts must be drafts. Hence
--     filtering new_rows on status='posted' selects exactly the rows whose
--     prior status was 'draft' -- the same set the old_rows semi join
--     produced, without joining the two tuplestores;
--   * account_move_lines is reached exclusively through JOIN LATERAL
--     aggregates. The aggregate inside the lateral subquery prevents the
--     subquery from being flattened into a plain join, so the inner scan is
--     always a parameterized primary-key probe per posted move.

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
    LEFT JOIN LATERAL (
        SELECT SUM(lines.functional_debit_minor) AS total_debit,
               SUM(lines.functional_credit_minor) AS total_credit
        FROM account_move_lines AS lines
        WHERE lines.move_id = posted_moves.id
    ) AS line_totals ON true
    WHERE posted_moves.status = 'posted'
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
    REFERENCING NEW TABLE AS new_rows
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
    WHERE batch_moves.status = 'posted'
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
        move_totals.company_id,
        move_totals.account_id,
        posted.period_id,
        company.functional_currency,
        SUM(move_totals.debit_minor::numeric / power(10::numeric, currency.minor_units)),
        SUM(move_totals.credit_minor::numeric / power(10::numeric, currency.minor_units))
    FROM (
        SELECT id AS move_id, period_id
        FROM new_rows
        WHERE status = 'posted'
    ) AS posted
    JOIN LATERAL (
        SELECT lines.company_id AS company_id,
               lines.account_id AS account_id,
               SUM(lines.functional_debit_minor) AS debit_minor,
               SUM(lines.functional_credit_minor) AS credit_minor
        FROM account_move_lines AS lines
        WHERE lines.move_id = posted.move_id
        GROUP BY lines.company_id, lines.account_id
    ) AS move_totals ON move_totals.account_id IS NOT NULL
    JOIN companies company ON company.id = move_totals.company_id
    JOIN currencies currency ON currency.code = company.functional_currency
    GROUP BY move_totals.company_id, move_totals.account_id, posted.period_id, company.functional_currency, currency.minor_units
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
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION update_account_balance_on_posting();
