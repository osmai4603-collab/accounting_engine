UPDATE account_move_lines AS line
SET
    transaction_currency = COALESCE(NULLIF(line.currency_id, ''), company.functional_currency),
    currency_id = COALESCE(NULLIF(line.currency_id, ''), company.functional_currency),
    transaction_amount_minor = CASE
        WHEN line.transaction_amount_minor <> 0 AND line.functional_credit_minor > 0
            THEN -ABS(line.transaction_amount_minor)
        WHEN line.transaction_amount_minor <> 0 THEN ABS(line.transaction_amount_minor)
        WHEN line.credit > 0 THEN -ROUND(
            line.credit * power(10::numeric, transaction_currency.minor_units)
        )::bigint
        ELSE ROUND(
            line.debit * power(10::numeric, transaction_currency.minor_units)
        )::bigint
    END,
    functional_debit_minor = CASE
        WHEN line.functional_debit_minor <> 0 THEN line.functional_debit_minor
        WHEN line.debit > 0 THEN ROUND(
            COALESCE(NULLIF(line.amount_currency, 0), line.debit) *
                power(10::numeric, functional_currency.minor_units)
        )::bigint
        ELSE 0
    END,
    functional_credit_minor = CASE
        WHEN line.functional_credit_minor <> 0 THEN line.functional_credit_minor
        WHEN line.credit > 0 THEN ROUND(
            COALESCE(NULLIF(line.amount_currency, 0), line.credit) *
                power(10::numeric, functional_currency.minor_units)
        )::bigint
        ELSE 0
    END
FROM companies AS company,
     currencies AS transaction_currency,
     currencies AS functional_currency,
     account_moves AS move
WHERE company.id = line.company_id
  AND transaction_currency.code = COALESCE(NULLIF(line.currency_id, ''), company.functional_currency)
  AND functional_currency.code = company.functional_currency
  AND move.id = line.move_id
  AND move.company_id = line.company_id
  AND move.status = 'draft'
  AND (line.transaction_amount_minor = 0 OR
       line.functional_debit_minor = 0 OR line.functional_credit_minor = 0);

ALTER TABLE account_move_lines
    DROP CONSTRAINT IF EXISTS chk_currency_layers_consistency;

ALTER TABLE account_move_lines
    ADD CONSTRAINT chk_currency_layers_consistency
    CHECK (
        exchange_rate > 0 AND
        transaction_currency IS NOT NULL AND
        (
            (functional_debit_minor > 0 AND functional_credit_minor = 0) OR
            (functional_credit_minor > 0 AND functional_debit_minor = 0)
        )
    );

CREATE OR REPLACE VIEW v_account_move_lines_dual_currency AS
SELECT
    line.move_id,
    line.line_id,
    line.company_id,
    line.account_id,
    line.partner_id,
    line.transaction_currency,
    COALESCE(
        NULLIF(CASE
            WHEN line.functional_credit_minor > 0 THEN -ABS(line.transaction_amount_minor)
            ELSE ABS(line.transaction_amount_minor)
        END, 0)::numeric /
            power(10::numeric, transaction_currency.minor_units),
        CASE WHEN line.credit > 0 THEN -line.credit ELSE line.debit END
    ) AS transaction_amount,
    line.exchange_rate,
    company.functional_currency,
    line.functional_debit_minor::numeric /
        power(10::numeric, functional_currency.minor_units) AS functional_debit,
    line.functional_credit_minor::numeric /
        power(10::numeric, functional_currency.minor_units) AS functional_credit
FROM account_move_lines AS line
JOIN companies AS company ON company.id = line.company_id
JOIN currencies AS transaction_currency ON transaction_currency.code = line.transaction_currency
JOIN currencies AS functional_currency ON functional_currency.code = company.functional_currency;
