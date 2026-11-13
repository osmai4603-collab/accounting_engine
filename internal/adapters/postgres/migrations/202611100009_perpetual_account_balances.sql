CREATE TABLE IF NOT EXISTS account_balances (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL,
    fiscal_period_id uuid NOT NULL,
    currency_id varchar(3) NOT NULL REFERENCES currencies(code) ON DELETE RESTRICT,
    beginning_debit numeric(28,4) NOT NULL DEFAULT 0 CHECK (beginning_debit >= 0),
    beginning_credit numeric(28,4) NOT NULL DEFAULT 0 CHECK (beginning_credit >= 0),
    period_debit numeric(28,4) NOT NULL DEFAULT 0 CHECK (period_debit >= 0),
    period_credit numeric(28,4) NOT NULL DEFAULT 0 CHECK (period_credit >= 0),
    ending_debit numeric(28,4) GENERATED ALWAYS AS (beginning_debit + period_debit) STORED,
    ending_credit numeric(28,4) GENERATED ALWAYS AS (beginning_credit + period_credit) STORED,
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, account_id, fiscal_period_id, currency_id),
    FOREIGN KEY (company_id, account_id) REFERENCES accounts(company_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (company_id, fiscal_period_id) REFERENCES fiscal_periods(company_id, id) ON DELETE RESTRICT,
    CHECK (version > 0)
);

CREATE INDEX IF NOT EXISTS idx_account_balances_account
    ON account_balances(company_id, account_id, fiscal_period_id);
CREATE INDEX IF NOT EXISTS idx_account_balances_period
    ON account_balances(company_id, fiscal_period_id);

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

INSERT INTO account_balances (
    company_id, account_id, fiscal_period_id, currency_id, period_debit, period_credit
)
SELECT
    lines.company_id,
    lines.account_id,
    moves.period_id,
    company.functional_currency,
    SUM(lines.functional_debit_minor::numeric / power(10::numeric, currency.minor_units)),
    SUM(lines.functional_credit_minor::numeric / power(10::numeric, currency.minor_units))
FROM account_move_lines lines
JOIN account_moves moves ON moves.id = lines.move_id
JOIN companies company ON company.id = lines.company_id
JOIN currencies currency ON currency.code = company.functional_currency
WHERE moves.status IN ('posted', 'reversed')
  AND moves.period_id IS NOT NULL
GROUP BY lines.company_id, lines.account_id, moves.period_id,
    company.functional_currency, currency.minor_units
ON CONFLICT (company_id, account_id, fiscal_period_id, currency_id)
DO UPDATE SET
    period_debit = EXCLUDED.period_debit,
    period_credit = EXCLUDED.period_credit,
    version = account_balances.version + 1,
    updated_at = now();

ALTER TABLE account_balances ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_balances FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation_policy ON account_balances;
CREATE POLICY tenant_isolation_policy ON account_balances
    USING (company_id = app_current_company_id())
    WITH CHECK (company_id = app_current_company_id());
