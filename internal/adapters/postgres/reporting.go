package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/governance"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

var errTraceStatementItemNotFound = reporting.ErrTraceItemNotFound

func (d *Driver) SaveFinancialStatementNote(
	ctx context.Context,
	companyID uuid.UUID,
	note reporting.NoteDisclosure,
) error {
	if companyID == uuid.Nil || note.StatementDate.IsZero() ||
		strings.TrimSpace(note.ReportCode) == "" || strings.TrimSpace(note.Code) == "" ||
		strings.TrimSpace(note.Title) == "" || strings.TrimSpace(note.Currency) == "" ||
		note.CompanyID != uuid.Nil && note.CompanyID != companyID {
		return errors.New("invalid or mismatched financial statement note")
	}
	if note.ID == uuid.Nil {
		note.ID = uuid.New()
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO financial_statement_notes(
			id,company_id,report_code,statement_date,code,title,description,amount,currency_id
		) VALUES($1,$2,$3,$4::date,$5,$6,$7,$8,$9)
		ON CONFLICT(company_id,report_code,statement_date,code)
		DO UPDATE SET title=EXCLUDED.title,description=EXCLUDED.description,
			amount=EXCLUDED.amount,currency_id=EXCLUDED.currency_id
		RETURNING id`,
			note.ID, companyID, note.ReportCode, note.StatementDate, note.Code, note.Title,
			note.Description, note.Amount, note.Currency).Scan(&note.ID)
	})
}

func (d *Driver) ListFinancialStatementNotesForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	reportCode string,
	statementDate time.Time,
) ([]reporting.NoteDisclosure, error) {
	if companyID == uuid.Nil || strings.TrimSpace(reportCode) == "" || statementDate.IsZero() {
		return nil, errors.New("financial statement note scope is incomplete")
	}
	notes := make([]reporting.NoteDisclosure, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,company_id,report_code,statement_date,
			code,title,description,amount,currency_id
			FROM financial_statement_notes
			WHERE company_id=$1 AND report_code=$2 AND statement_date=$3::date
			ORDER BY code`, companyID, reportCode, statementDate)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var note reporting.NoteDisclosure
			if err := rows.Scan(&note.ID, &note.CompanyID, &note.ReportCode, &note.StatementDate,
				&note.Code, &note.Title, &note.Description, &note.Amount, &note.Currency); err != nil {
				return err
			}
			notes = append(notes, note)
		}
		return rows.Err()
	})
	return notes, err
}

func (d *Driver) ReadAccountPeriodBalancesForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
	includeAdjusting bool,
) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error) {
	if companyID == uuid.Nil || end.IsZero() || (!start.IsZero() && end.Before(start)) {
		return nil, false, nil
	}
	var result map[uuid.UUID]posting.AccountPeriodBalance
	available := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var functionalCurrency string
		if err := tx.QueryRow(ctx, `SELECT functional_currency FROM companies WHERE id=$1`, companyID).
			Scan(&functionalCurrency); err != nil {
			return err
		}
		if functionalCurrency != currency {
			return nil
		}

		var yearID uuid.UUID
		var yearStart time.Time
		if err := tx.QueryRow(ctx, `SELECT id,start_date FROM fiscal_years
			WHERE company_id=$1 AND start_date <= $2::date AND end_date >= $2::date
			ORDER BY start_date DESC LIMIT 1`, companyID, end).
			Scan(&yearID, &yearStart); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if !start.IsZero() && start.Before(yearStart) {
			return nil
		}

		type periodRange struct {
			id     uuid.UUID
			start  time.Time
			end    time.Time
			number int
		}
		rows, err := tx.Query(ctx, `SELECT id,start_date,end_date,COALESCE(period_number,0)
			FROM fiscal_periods WHERE company_id=$1 AND year_id=$2
			ORDER BY start_date,period_number`, companyID, yearID)
		if err != nil {
			return err
		}
		periods := make([]periodRange, 0)
		for rows.Next() {
			var period periodRange
			if err := rows.Scan(&period.id, &period.start, &period.end, &period.number); err != nil {
				rows.Close()
				return err
			}
			periods = append(periods, period)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(periods) == 0 {
			return nil
		}

		rangeStart := start
		if rangeStart.IsZero() {
			rangeStart = yearStart
		}
		first, last := -1, -1
		for i, period := range periods {
			if period.start.Before(rangeStart) && !period.end.Before(rangeStart) {
				return nil
			}
			if !period.start.After(end) && period.end.After(end) {
				return nil
			}
			if !period.start.Before(rangeStart) && !period.end.After(end) {
				if first == -1 {
					first = i
				}
				last = i
			}
		}
		if first == -1 || last == -1 {
			return nil
		}
		if !includeAdjusting {
			var hasAdjusting bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM account_moves moves
				JOIN fiscal_periods period
				  ON period.company_id=moves.company_id AND period.id=moves.period_id
				WHERE moves.company_id=$1 AND period.year_id=$2
				  AND period.end_date <= $3 AND moves.status IN ('posted','reversed')
				  AND moves.classification='adjusting'
			)`, companyID, yearID, periods[last].end).Scan(&hasAdjusting); err != nil {
				return err
			}
			if hasAdjusting {
				return nil
			}
		}

		rows, err = tx.Query(ctx, `SELECT balances.account_id,balances.beginning_debit,
			balances.beginning_credit,balances.period_debit,balances.period_credit,
			period.id,period.start_date,period.end_date
			FROM account_balances balances
			JOIN fiscal_periods period
			  ON period.company_id=balances.company_id AND period.id=balances.fiscal_period_id
			WHERE balances.company_id=$1 AND period.year_id=$2
			  AND period.end_date <= $3 AND balances.currency_id=$4
			ORDER BY period.start_date,period.period_number`, companyID, yearID, periods[last].end, currency)
		if err != nil {
			return err
		}
		balanceMap := make(map[uuid.UUID]posting.AccountPeriodBalance)
		periodIndexes := make(map[uuid.UUID]int, len(periods))
		for i, period := range periods {
			periodIndexes[period.id] = i
		}
		for rows.Next() {
			var accountID, periodID uuid.UUID
			var beginningDebit, beginningCredit, periodDebit, periodCredit decimal.Decimal
			var periodStart, periodEnd time.Time
			if err := rows.Scan(&accountID, &beginningDebit, &beginningCredit, &periodDebit, &periodCredit,
				&periodID, &periodStart, &periodEnd); err != nil {
				rows.Close()
				return err
			}
			index, ok := periodIndexes[periodID]
			if !ok {
				continue
			}
			balance := balanceMap[accountID]
			balance.AccountID = accountID
			if !beginningDebit.IsZero() {
				balance.BeginningDebit = balance.BeginningDebit.Add(beginningDebit)
			}
			if !beginningCredit.IsZero() {
				balance.BeginningCredit = balance.BeginningCredit.Add(beginningCredit)
			}
			if index < first {
				if !periodDebit.IsZero() {
					balance.BeginningDebit = balance.BeginningDebit.Add(periodDebit)
				}
				if !periodCredit.IsZero() {
					balance.BeginningCredit = balance.BeginningCredit.Add(periodCredit)
				}
			} else {
				if balance.PeriodDebit.IsZero() {
					balance.PeriodDebit = periodDebit
				} else if !periodDebit.IsZero() {
					balance.PeriodDebit = balance.PeriodDebit.Add(periodDebit)
				}
				if balance.PeriodCredit.IsZero() {
					balance.PeriodCredit = periodCredit
				} else if !periodCredit.IsZero() {
					balance.PeriodCredit = balance.PeriodCredit.Add(periodCredit)
				}
			}
			balance.EndingDebit = balance.BeginningDebit
			if !balance.PeriodDebit.IsZero() {
				if balance.EndingDebit.IsZero() {
					balance.EndingDebit = balance.PeriodDebit
				} else {
					balance.EndingDebit = balance.EndingDebit.Add(balance.PeriodDebit)
				}
			}
			balance.EndingCredit = balance.BeginningCredit
			if !balance.PeriodCredit.IsZero() {
				if balance.EndingCredit.IsZero() {
					balance.EndingCredit = balance.PeriodCredit
				} else {
					balance.EndingCredit = balance.EndingCredit.Add(balance.PeriodCredit)
				}
			}
			balanceMap[accountID] = balance
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		result = balanceMap
		available = true
		return nil
	})
	return result, available, err
}

func (d *Driver) ListPostedEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	return d.ListEntries(ctx, start, end)
}
func (d *Driver) ListEntries(ctx context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	return nil, masterdata.ErrCompanyScopeRequired
}

func (d *Driver) ListEntriesForCompany(ctx context.Context, companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	result := make([]posting.JournalEntry, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT entry FROM account_moves
			WHERE company_id=$1 AND status IN ($2,$3) AND entry_date BETWEEN $4 AND $5
			ORDER BY entry_date`, companyID, posting.EntryPosted, posting.EntryReversed, start, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var entry posting.JournalEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				return err
			}
			result = append(result, entry)
		}
		return rows.Err()
	})
	return result, err
}

func (d *Driver) GetAccountBalance(ctx context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error) {
	return d.GetAccountBalanceAsOf(ctx, companyID, accountID, time.Now().UTC())
}

func (d *Driver) GetAccountBalanceAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	if asOf.IsZero() {
		return financialtypes.Money{}, subledgers.ErrReconciliationHistoryUnavailable
	}
	account, err := d.GetAccountByIDForCompany(ctx, companyID, accountID)
	if err != nil {
		return financialtypes.Money{}, err
	}
	var functionalCurrency string
	if err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT functional_currency FROM companies WHERE id=$1`, companyID).Scan(&functionalCurrency)
	}); err != nil {
		return financialtypes.Money{}, err
	}
	entries, err := d.ListEntriesForCompany(ctx, companyID, time.Time{}, asOf)
	if err != nil {
		return financialtypes.Money{}, err
	}
	return subledgers.ControlAccountBalanceFromEntries(account, entries, functionalCurrency)
}

// AssertBalancesMatchMoves reconciles the materialized account_balances rows
// of a company/fiscal period/functional currency against the functional minor
// units stored on the posted move lines. Both sides are compared in integer
// minor units, so the assertion is exact: a single sub-minor-unit drift or a
// missing balance row is reported as a reconciliation failure.
//
// Moves with status 'reversed' are included because their lines were already
// materialized when they were posted; the reversing move is counted separately
// as a posted move, so the pair nets to zero.
func (d *Driver) AssertBalancesMatchMoves(ctx context.Context, companyID, fiscalPeriodID uuid.UUID, currencyID string) error {
	report, err := d.BuildBalanceReconciliationReport(ctx, companyID, fiscalPeriodID, currencyID)
	if err != nil {
		return err
	}
	if !report.ZeroDrift {
		return fmt.Errorf("balance reconciliation failed: %d account(s) differ between posted move lines and account_balances",
			len(report.Drifts))
	}
	return nil
}

// BuildBalanceReconciliationReport returns the detailed per-account
// reconciliation between the authoritative posted move lines (ledger side)
// and the materialized account_balances rows (balance side). Every amount is
// expressed in exact integer minor units of the reconciliation currency, so
// the drift columns expose even a single sub-minor-unit divergence.
func (d *Driver) BuildBalanceReconciliationReport(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) (reporting.ReconciliationReport, error) {
	if companyID == uuid.Nil || fiscalPeriodID == uuid.Nil || currencyID == "" {
		return reporting.ReconciliationReport{}, posting.ErrInvalidEntry
	}
	result := reporting.ReconciliationReport{
		CompanyID:      companyID,
		FiscalPeriodID: fiscalPeriodID,
		CurrencyID:     currencyID,
		ZeroDrift:      true,
		GeneratedAt:    time.Now().UTC(),
	}
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
WITH line_totals AS (
    SELECT lines.account_id,
           SUM(lines.functional_debit_minor)::numeric AS debit_minor,
           SUM(lines.functional_credit_minor)::numeric AS credit_minor
    FROM account_move_lines AS lines
    JOIN account_moves AS moves
      ON moves.company_id = lines.company_id AND moves.id = lines.move_id
    WHERE lines.company_id = $1
      AND moves.period_id = $2
      AND moves.status IN ('posted', 'reversed')
    GROUP BY lines.account_id
),
scale AS (
    SELECT power(10::numeric, minor_units) AS factor
    FROM currencies WHERE code = $3
),
balance_totals AS (
    SELECT balances.account_id,
           balances.period_debit * scale.factor AS debit_minor,
           balances.period_credit * scale.factor AS credit_minor
    FROM account_balances AS balances
    CROSS JOIN scale
    WHERE balances.company_id = $1
      AND balances.fiscal_period_id = $2
      AND balances.currency_id = $3
),
combined AS (
    SELECT COALESCE(lines.account_id, balances.account_id) AS account_id,
           COALESCE(lines.debit_minor, 0) AS ledger_debit,
           COALESCE(lines.credit_minor, 0) AS ledger_credit,
           COALESCE(balances.debit_minor, 0) AS balance_debit,
           COALESCE(balances.credit_minor, 0) AS balance_credit
    FROM line_totals AS lines
    FULL OUTER JOIN balance_totals AS balances ON balances.account_id = lines.account_id
)
SELECT account_id, ledger_debit, ledger_credit, balance_debit, balance_credit
FROM combined
ORDER BY account_id`,
			companyID, fiscalPeriodID, currencyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var accountID uuid.UUID
			var ledgerDebit, ledgerCredit, balanceDebit, balanceCredit string
			if err := rows.Scan(&accountID, &ledgerDebit, &ledgerCredit, &balanceDebit, &balanceCredit); err != nil {
				return err
			}
			result.AccountsChecked++
			ledgerDebitMinor, err := decimal.NewFromString(ledgerDebit)
			if err != nil {
				return fmt.Errorf("account %s ledger debit %q: %w", accountID, ledgerDebit, err)
			}
			ledgerCreditMinor, err := decimal.NewFromString(ledgerCredit)
			if err != nil {
				return fmt.Errorf("account %s ledger credit %q: %w", accountID, ledgerCredit, err)
			}
			balanceDebitMinor, err := decimal.NewFromString(balanceDebit)
			if err != nil {
				return fmt.Errorf("account %s balance debit %q: %w", accountID, balanceDebit, err)
			}
			balanceCreditMinor, err := decimal.NewFromString(balanceCredit)
			if err != nil {
				return fmt.Errorf("account %s balance credit %q: %w", accountID, balanceCredit, err)
			}
			debitDrift := ledgerDebitMinor.Sub(balanceDebitMinor)
			creditDrift := ledgerCreditMinor.Sub(balanceCreditMinor)
			if debitDrift.IsZero() && creditDrift.IsZero() {
				continue
			}
			result.ZeroDrift = false
			result.Drifts = append(result.Drifts, reporting.ReconciliationAccountDrift{
				AccountID:          accountID,
				LedgerDebitMinor:   ledgerDebitMinor,
				LedgerCreditMinor:  ledgerCreditMinor,
				BalanceDebitMinor:  balanceDebitMinor,
				BalanceCreditMinor: balanceCreditMinor,
				DebitDriftMinor:    debitDrift,
				CreditDriftMinor:   creditDrift,
			})
		}
		return rows.Err()
	})
	if err != nil {
		return reporting.ReconciliationReport{}, err
	}
	return result, nil
}

// RebuildBalancesFromLines atomically repairs the activity columns for one
// fiscal period from the posted ledger. The table locks prevent a posting from
// interleaving with the rebuild; beginning balances are intentionally retained.
func (d *Driver) RebuildBalancesFromLines(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) error {
	if companyID == uuid.Nil || fiscalPeriodID == uuid.Nil || strings.TrimSpace(currencyID) == "" {
		return posting.ErrInvalidEntry
	}
	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var functionalCurrency string
		if err := tx.QueryRow(ctx, `SELECT company.functional_currency
			FROM companies company
			JOIN fiscal_periods period ON period.company_id=company.id
			WHERE company.id=$1 AND period.id=$2
			FOR UPDATE OF period`, companyID, fiscalPeriodID).Scan(&functionalCurrency); err != nil {
			return fmt.Errorf("validate fiscal period: %w", err)
		}
		if functionalCurrency != currencyID {
			return fmt.Errorf("rebuild currency %q does not match company functional currency %q", currencyID, functionalCurrency)
		}
		if _, err := tx.Exec(ctx, `LOCK TABLE account_moves IN SHARE MODE`); err != nil {
			return fmt.Errorf("lock journal moves for balance rebuild: %w", err)
		}
		if _, err := tx.Exec(ctx, `LOCK TABLE account_move_lines IN SHARE MODE`); err != nil {
			return fmt.Errorf("lock journal lines for balance rebuild: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE account_balances
			SET period_debit=0, period_credit=0, version=version+1, updated_at=now()
			WHERE company_id=$1 AND fiscal_period_id=$2 AND currency_id=$3`,
			companyID, fiscalPeriodID, currencyID); err != nil {
			return fmt.Errorf("clear materialized period activity: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO account_balances (
    company_id, account_id, fiscal_period_id, currency_id, period_debit, period_credit
)
SELECT lines.company_id, lines.account_id, moves.period_id, $3,
       SUM(lines.functional_debit_minor::numeric / power(10::numeric, currency.minor_units)),
       SUM(lines.functional_credit_minor::numeric / power(10::numeric, currency.minor_units))
FROM account_move_lines lines
JOIN account_moves moves ON moves.company_id=lines.company_id AND moves.id=lines.move_id
JOIN currencies currency ON currency.code=$3
WHERE lines.company_id=$1 AND moves.period_id=$2 AND moves.status IN ('posted','reversed')
GROUP BY lines.company_id, lines.account_id, moves.period_id, currency.minor_units
ON CONFLICT (company_id, account_id, fiscal_period_id, currency_id)
DO UPDATE SET period_debit=EXCLUDED.period_debit,
              period_credit=EXCLUDED.period_credit,
              version=account_balances.version+1,
              updated_at=now()`, companyID, fiscalPeriodID, currencyID); err != nil {
			return fmt.Errorf("rebuild materialized period activity from posted lines: %w", err)
		}
		return nil
	})
}

// RecordBalanceDriftAlert appends the full reconciliation evidence to the
// company's tamper-evident audit chain.
func (d *Driver) RecordBalanceDriftAlert(ctx context.Context, report reporting.ReconciliationReport) error {
	if report.CompanyID == uuid.Nil || report.FiscalPeriodID == uuid.Nil || report.ZeroDrift {
		return posting.ErrInvalidEntry
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode balance drift alert: %w", err)
	}
	return d.WithCompanyTx(ctx, report.CompanyID, func(tx pgx.Tx) error {
		_, err := appendAuditLogTx(ctx, tx, governance.FinancialAuditLog{
			CompanyID:    report.CompanyID,
			EntityType:   "account_balance_integrity_alert",
			EntityID:     report.FiscalPeriodID,
			Action:       "critical_balance_drift_detected",
			ActorID:      uuid.Nil,
			PayloadAfter: payload,
			Timestamp:    time.Now().UTC(),
		})
		if err != nil {
			return fmt.Errorf("append critical balance drift alert: %w", err)
		}
		return nil
	})
}

type traceAccountSelector struct {
	accountTypes      []string
	subclasses        []string
	notSubclasses     []string
	category          string
	categories        []string
	tag               string
	tags              []string
	alternateTypes    []string
	alternateCategory string
}

var traceSelectors = map[string]traceAccountSelector{
	"CURRENT_ASSETS":                  {accountTypes: []string{"asset"}, subclasses: []string{"cash_and_equivalents", "trade_receivables", "inventories", "prepayments_and_other_debit", "current_assets", "accounts_receivable", "bank", "cash"}},
	"NONCURRENT_ASSETS":               {accountTypes: []string{"asset"}, notSubclasses: []string{"cash_and_equivalents", "trade_receivables", "inventories", "prepayments_and_other_debit", "current_assets", "accounts_receivable", "bank", "cash"}},
	"TOTAL_ASSETS":                    {accountTypes: []string{"asset"}},
	"CURRENT_LIABILITIES":             {accountTypes: []string{"liability"}, subclasses: []string{"trade_payables", "short_term_borrowings", "current_lease_liabilities", "accruals_and_other_credit", "current_tax_liabilities", "current_liabilities", "accounts_payable", "payable"}},
	"NONCURRENT_LIABILITIES":          {accountTypes: []string{"liability"}, notSubclasses: []string{"trade_payables", "short_term_borrowings", "current_lease_liabilities", "accruals_and_other_credit", "current_tax_liabilities", "current_liabilities", "accounts_payable", "payable"}},
	"TOTAL_LIABILITIES":               {accountTypes: []string{"liability"}},
	"EQUITY":                          {accountTypes: []string{"equity", "income", "expense"}},
	"TOTAL_LIABILITIES_AND_EQUITY":    {accountTypes: []string{"liability", "equity", "income", "expense"}},
	"REVENUE":                         {accountTypes: []string{"income"}, categories: []string{"operating"}, tag: "revenue"},
	"INVESTING_INCOME":                {accountTypes: []string{"income", "expense"}, categories: []string{"investing"}},
	"FINANCING_INCOME":                {accountTypes: []string{"income"}, categories: []string{"financing"}},
	"FINANCE_INCOME":                  {accountTypes: []string{"income", "expense"}, categories: []string{"investing"}, alternateTypes: []string{"income"}, alternateCategory: "financing"},
	"COST_OF_SALES":                   {accountTypes: []string{"expense"}, subclasses: []string{"cost_of_sales"}, tag: "cost_of_sales"},
	"SELLING_EXPENSES":                {accountTypes: []string{"expense"}, subclasses: []string{"selling_distribution_expense"}, tag: "selling_expense"},
	"ADMIN_EXPENSES":                  {accountTypes: []string{"expense"}, subclasses: []string{"general_admin_expense"}, tag: "administrative_expense"},
	"DEPRECIATION_EXPENSES":           {accountTypes: []string{"expense"}, subclasses: []string{"depreciation_amortization_expense"}, tag: "depreciation_expense"},
	"OTHER_OPERATING_EXPENSES":        {accountTypes: []string{"expense"}, subclasses: []string{"other_operating_expense"}, tag: "other_operating_expense"},
	"FINANCE_COSTS":                   {accountTypes: []string{"expense"}, subclasses: []string{"finance_cost"}, category: "financing", tag: "finance_cost"},
	"INCOME_TAX_ZAKAT":                {accountTypes: []string{"income", "expense"}, categories: []string{"tax", "income_taxes"}, tag: "income_tax"},
	"GROSS_PROFIT":                    {accountTypes: []string{"income", "expense"}, subclasses: []string{"operating_revenue", "cost_of_sales"}, categories: []string{"operating"}, tags: []string{"revenue", "cost_of_sales"}},
	"OPERATING_PROFIT":                {accountTypes: []string{"income", "expense"}, categories: []string{"operating"}},
	"PROFIT_BEFORE_FINANCING_AND_TAX": {accountTypes: []string{"income", "expense"}, categories: []string{"operating", "investing"}},
	"PROFIT_BEFORE_TAX":               {accountTypes: []string{"income", "expense"}, categories: []string{"operating", "investing", "financing"}},
	"DISCONTINUED_OPERATIONS":         {accountTypes: []string{"income", "expense"}, categories: []string{"discontinued", "discontinued_operations"}},
	"NET_PROFIT":                      {accountTypes: []string{"income", "expense"}, categories: []string{"operating", "investing", "financing", "tax", "income_taxes", "discontinued", "discontinued_operations"}},
	"OCI":                             {accountTypes: []string{"income", "expense"}, categories: []string{"equity_movements"}},
	"TOTAL_COMPREHENSIVE_INCOME":      {accountTypes: []string{"income", "expense"}, categories: []string{"operating", "investing", "financing", "tax", "income_taxes", "discontinued", "discontinued_operations", "equity_movements"}},
}

func traceStatementAccounts(
	ctx context.Context,
	tx pgx.Tx,
	companyID uuid.UUID,
	lineItem string,
) ([]uuid.UUID, map[uuid.UUID]traceAccountAttrs, error) {
	var accountID uuid.UUID
	item := strings.TrimSpace(lineItem)
	if parsed, err := uuid.Parse(item); err == nil {
		err = tx.QueryRow(ctx, `SELECT id FROM accounts WHERE company_id=$1 AND id=$2`, companyID, parsed).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
	} else {
		err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE company_id=$1 AND code=$2`, companyID, item).Scan(&accountID)
		if err == nil {
			return traceAccountAttributes(ctx, tx, companyID, []uuid.UUID{accountID})
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, err
		}
	}
	if accountID != uuid.Nil {
		return traceAccountAttributes(ctx, tx, companyID, []uuid.UUID{accountID})
	}

	selector, ok := traceSelectors[strings.ToUpper(strings.ReplaceAll(item, "-", "_"))]
	if !ok {
		return nil, nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT account.id
		FROM accounts account
		LEFT JOIN reporting_account_tags tag
		  ON tag.company_id=account.company_id AND tag.account_id=account.id
		WHERE account.company_id=$1
		  AND (
		    (
		      (COALESCE(cardinality($2::text[]),0)=0 OR account.account_type=ANY($2::text[]))
		      AND (COALESCE(cardinality($3::text[]),0)=0 OR account.sub_classification=ANY($3::text[]))
		      AND (COALESCE(cardinality($4::text[]),0)=0 OR account.sub_classification<>ALL($4::text[]))
		      AND ($5='' OR account.ifrs_category=$5)
		      AND (COALESCE(cardinality($6::text[]),0)=0 OR account.ifrs_category=ANY($6::text[]))
		    )
		    OR (
		      (COALESCE(cardinality($2::text[]),0)=0 OR account.account_type=ANY($2::text[]))
		      AND $7<>'' AND tag.tag=$7
		    )
		    OR (
		      (COALESCE(cardinality($2::text[]),0)=0 OR account.account_type=ANY($2::text[]))
		      AND COALESCE(cardinality($8::text[]),0)>0 AND tag.tag=ANY($8::text[])
		    )
		    OR (
		      (COALESCE(cardinality($9::text[]),0)=0 OR account.account_type=ANY($9::text[]))
		      AND $10<>'' AND account.ifrs_category=$10
		    )
		  )
		ORDER BY account.id`, companyID, selector.accountTypes, selector.subclasses, selector.notSubclasses,
		selector.category, selector.categories, selector.tag, selector.tags,
		selector.alternateTypes, selector.alternateCategory)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	_, attrs, err := traceAccountAttributes(ctx, tx, companyID, ids)
	if err != nil {
		return nil, nil, err
	}
	return ids, attrs, nil
}

type traceAccountAttrs struct {
	normalBalance string
	contra        bool
}

func traceAccountAttributes(
	ctx context.Context,
	tx pgx.Tx,
	companyID uuid.UUID,
	ids []uuid.UUID,
) ([]uuid.UUID, map[uuid.UUID]traceAccountAttrs, error) {
	if len(ids) == 0 {
		return nil, map[uuid.UUID]traceAccountAttrs{}, nil
	}
	rows, err := tx.Query(ctx, `SELECT id,normal_balance,is_contra_account
		FROM accounts WHERE company_id=$1 AND id=ANY($2::uuid[]) ORDER BY id`, companyID, ids)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	found := make([]uuid.UUID, 0, len(ids))
	attrs := make(map[uuid.UUID]traceAccountAttrs, len(ids))
	for rows.Next() {
		var id uuid.UUID
		var attr traceAccountAttrs
		if err := rows.Scan(&id, &attr.normalBalance, &attr.contra); err != nil {
			return nil, nil, err
		}
		found = append(found, id)
		attrs[id] = attr
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return found, attrs, nil
}

func (d *Driver) TraceFinancialStatementItem(
	ctx context.Context,
	companyID uuid.UUID,
	reportID, lineItem string,
	start, end time.Time,
	currency string,
) (reporting.TraceResult, error) {
	if companyID == uuid.Nil || strings.TrimSpace(reportID) == "" || strings.TrimSpace(lineItem) == "" {
		return reporting.TraceResult{}, posting.ErrInvalidEntry
	}
	result := reporting.TraceResult{
		StatementItem:  strings.TrimSpace(lineItem),
		MoveLines:      make([]reporting.TracedMoveLine, 0),
		JournalEntries: make([]posting.JournalEntry, 0),
		SourceEvents:   make([]reporting.TracedEconomicEvent, 0),
	}
	statementAmount, hasStatementAmount, err := d.statementAmountForTrace(
		ctx, companyID, reportID, lineItem, start, end, currency,
	)
	if err != nil {
		return reporting.TraceResult{}, fmt.Errorf("read reported statement amount: %w", err)
	}
	result.StatementAmount = statementAmount
	err = d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		accounts, attrs, err := traceStatementAccounts(ctx, tx, companyID, lineItem)
		if err != nil {
			return fmt.Errorf("resolve statement item accounts: %w", err)
		}
		if len(accounts) == 0 {
			return errTraceStatementItemNotFound
		}
		result.AccountIDs = accounts
		var functionalCurrency string
		if err := tx.QueryRow(ctx, `SELECT functional_currency FROM companies WHERE id=$1`, companyID).
			Scan(&functionalCurrency); err != nil {
			return fmt.Errorf("load trace company currency: %w", err)
		}
		if currency != "" {
			if !strings.EqualFold(currency, functionalCurrency) {
				return fmt.Errorf("trace currency %q differs from functional currency %q", currency, functionalCurrency)
			}
		}
		var currencyScale int
		if err := tx.QueryRow(ctx, `SELECT minor_units FROM currencies WHERE code=$1`, functionalCurrency).Scan(&currencyScale); err != nil {
			return fmt.Errorf("load trace currency scale: %w", err)
		}
		balanceSheet := strings.EqualFold(strings.TrimSpace(reportID), "balance_sheet") ||
			strings.EqualFold(strings.TrimSpace(reportID), "BALANCE_SHEET") ||
			strings.Contains(strings.ToUpper(reportID), "BALANCE")
		if balanceSheet {
			start = time.Time{}
		}
		query := `SELECT moves.id,moves.entry_date,moves.entry,lines.line_id,lines.account_id,
			lines.functional_debit_minor,lines.functional_credit_minor,company.functional_currency
			FROM account_move_lines lines
			JOIN account_moves moves ON moves.company_id=lines.company_id AND moves.id=lines.move_id
			JOIN companies company ON company.id=moves.company_id
			WHERE moves.company_id=$1 AND moves.status IN ('posted','reversed')
			  AND ($2::timestamptz IS NULL OR moves.entry_date >= $2)
			  AND ($3::timestamptz IS NULL OR moves.entry_date <= $3)
			  AND lines.account_id=ANY($4::uuid[])
			ORDER BY moves.entry_date,moves.id,lines.line_id`
		var from, through any
		if !start.IsZero() {
			from = start
		}
		if !end.IsZero() {
			through = end
		}
		rows, err := tx.Query(ctx, query, companyID, from, through, accounts)
		if err != nil {
			return fmt.Errorf("query traced journal lines: %w", err)
		}
		entriesByID := make(map[uuid.UUID]posting.JournalEntry)
		moveIDs := make(map[uuid.UUID]struct{})
		for rows.Next() {
			var moveID, accountID, lineID uuid.UUID
			var date time.Time
			var entryData []byte
			var debitMinor, creditMinor int64
			var functionalCurrency string
			if err := rows.Scan(&moveID, &date, &entryData, &lineID, &accountID,
				&debitMinor, &creditMinor, &functionalCurrency); err != nil {
				rows.Close()
				return err
			}
			var entry posting.JournalEntry
			if err := json.Unmarshal(entryData, &entry); err != nil {
				rows.Close()
				return fmt.Errorf("decode journal entry %s: %w", moveID, err)
			}
			debit := decimal.NewFromInt(debitMinor).Shift(int32(-currencyScale))
			credit := decimal.NewFromInt(creditMinor).Shift(int32(-currencyScale))
			amount := debit.Sub(credit)
			if attrs[accountID].normalBalance == "credit" {
				amount = amount.Neg()
			}
			if attrs[accountID].contra {
				amount = amount.Neg()
			}
			result.LedgerAmount = result.LedgerAmount.Add(amount)
			if !hasStatementAmount {
				result.StatementAmount = result.StatementAmount.Add(amount)
			}
			result.MoveLines = append(result.MoveLines, reporting.TracedMoveLine{
				MoveID: moveID, LineID: lineID, AccountID: accountID,
				Debit: debit, Credit: credit, Currency: functionalCurrency,
				EntryDate: date, JournalEntry: entry,
			})
			if _, found := entriesByID[entry.ID]; !found {
				entriesByID[entry.ID] = entry
			}
			moveIDs[moveID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, entry := range entriesByID {
			result.JournalEntries = append(result.JournalEntries, entry)
		}
		sort.Slice(result.JournalEntries, func(i, j int) bool {
			return result.JournalEntries[i].ID.String() < result.JournalEntries[j].ID.String()
		})
		if len(moveIDs) > 0 {
			eventRows, err := tx.Query(ctx, `SELECT events.event_id,events.idempotency_key,
				events.event_type,events.external_id,events.payload_bytes
				FROM subledger_events events
				JOIN account_moves moves ON moves.company_id=events.company_id
				  AND moves.source_event_id=events.event_id
				WHERE events.company_id=$1 AND moves.id=ANY($2::uuid[])
				ORDER BY events.event_id`, companyID, uuidSliceFromSet(moveIDs))
			if err != nil {
				return fmt.Errorf("query traced economic events: %w", err)
			}
			for eventRows.Next() {
				var event reporting.TracedEconomicEvent
				var payload []byte
				if err := eventRows.Scan(&event.EventID, &event.IdempotencyKey, &event.EventType,
					&event.ExternalID, &payload); err != nil {
					eventRows.Close()
					return err
				}
				event.Payload = append([]byte(nil), payload...)
				result.SourceEvents = append(result.SourceEvents, event)
			}
			if err := eventRows.Err(); err != nil {
				eventRows.Close()
				return err
			}
			eventRows.Close()

			auditRows, err := tx.Query(ctx, `SELECT id,previous_log_hash,current_log_hash
				FROM financial_audit_logs
				WHERE company_id=$1 AND entity_id=ANY($2::uuid[])
				ORDER BY id`, companyID, uuidSliceFromSet(moveIDs))
			if err != nil {
				return fmt.Errorf("query financial audit chain proof: %w", err)
			}
			proofParts := make([]string, 0)
			for auditRows.Next() {
				var id int64
				var previousHash, currentHash string
				if err := auditRows.Scan(&id, &previousHash, &currentHash); err != nil {
					auditRows.Close()
					return err
				}
				proofParts = append(proofParts, fmt.Sprintf("%d:%s:%s", id, previousHash, currentHash))
			}
			if err := auditRows.Err(); err != nil {
				auditRows.Close()
				return err
			}
			auditRows.Close()
			result.AuditChainProof = strings.Join(proofParts, "|")
		}
		result.Reconciled = result.StatementAmount.Equal(result.LedgerAmount)
		return nil
	})
	if err != nil {
		return reporting.TraceResult{}, err
	}
	result.Reconciled = result.StatementAmount.Equal(result.LedgerAmount)
	return result, nil
}

func (d *Driver) TraceEconomicEvent(
	ctx context.Context,
	companyID, eventID uuid.UUID,
) (reporting.TraceResult, error) {
	if companyID == uuid.Nil || eventID == uuid.Nil {
		return reporting.TraceResult{}, posting.ErrInvalidEntry
	}
	result := reporting.TraceResult{
		StatementItem:  "EVENT:" + eventID.String(),
		AccountIDs:     make([]uuid.UUID, 0),
		MoveLines:      make([]reporting.TracedMoveLine, 0),
		JournalEntries: make([]posting.JournalEntry, 0),
		SourceEvents:   make([]reporting.TracedEconomicEvent, 0, 1),
		StatementItems: make([]string, 0),
	}
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var event reporting.TracedEconomicEvent
		if err := tx.QueryRow(ctx, `SELECT event_id,idempotency_key,event_type,external_id,payload_bytes
			FROM subledger_events WHERE company_id=$1 AND event_id=$2`,
			companyID, eventID).Scan(&event.EventID, &event.IdempotencyKey, &event.EventType,
			&event.ExternalID, &event.Payload); err != nil {
			return fmt.Errorf("load economic event: %w", err)
		}
		event.Payload = append([]byte(nil), event.Payload...)
		result.SourceEvents = append(result.SourceEvents, event)

		var functionalCurrency string
		if err := tx.QueryRow(ctx, `SELECT functional_currency FROM companies WHERE id=$1`,
			companyID).Scan(&functionalCurrency); err != nil {
			return err
		}
		var scale int
		if err := tx.QueryRow(ctx, `SELECT minor_units FROM currencies WHERE code=$1`,
			functionalCurrency).Scan(&scale); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT moves.id,moves.entry_date,moves.entry,
			lines.line_id,lines.account_id,lines.functional_debit_minor,lines.functional_credit_minor,
			accounts.account_type,accounts.normal_balance,accounts.sub_classification,
			accounts.ifrs_category,accounts.is_contra_account,accounts.cash_flow_activity,
			accounts.is_cash_equivalent
			FROM account_moves moves
			JOIN account_move_lines lines ON lines.company_id=moves.company_id AND lines.move_id=moves.id
			JOIN accounts ON accounts.company_id=lines.company_id AND accounts.id=lines.account_id
			WHERE moves.company_id=$1 AND moves.source_event_id=$2
			  AND moves.status IN ('posted','reversed')
			ORDER BY moves.entry_date,moves.id,lines.line_id`, companyID, eventID)
		if err != nil {
			return fmt.Errorf("query event journal lines: %w", err)
		}
		entries := make(map[uuid.UUID]posting.JournalEntry)
		entryIDs := make(map[uuid.UUID]struct{})
		accountSet := make(map[uuid.UUID]struct{})
		statementItems := make(map[string]struct{})
		for rows.Next() {
			var moveID, accountID, lineID uuid.UUID
			var date time.Time
			var entryData []byte
			var debitMinor, creditMinor int64
			var accountType, normalBalance, subclass, category, cashFlowActivity string
			var contra, cashEquivalent bool
			if err := rows.Scan(&moveID, &date, &entryData, &lineID, &accountID,
				&debitMinor, &creditMinor, &accountType, &normalBalance, &subclass, &category,
				&contra, &cashFlowActivity, &cashEquivalent); err != nil {
				rows.Close()
				return err
			}
			var entry posting.JournalEntry
			if err := json.Unmarshal(entryData, &entry); err != nil {
				rows.Close()
				return fmt.Errorf("decode event journal entry %s: %w", moveID, err)
			}
			debit := decimal.NewFromInt(debitMinor).Shift(int32(-scale))
			credit := decimal.NewFromInt(creditMinor).Shift(int32(-scale))
			amount := debit.Sub(credit)
			if normalBalance == "credit" {
				amount = amount.Neg()
			}
			if contra {
				amount = amount.Neg()
			}
			result.LedgerAmount = result.LedgerAmount.Add(amount)
			result.MoveLines = append(result.MoveLines, reporting.TracedMoveLine{
				MoveID: moveID, LineID: lineID, AccountID: accountID,
				Debit: debit, Credit: credit, Currency: functionalCurrency,
				EntryDate: date, JournalEntry: entry,
			})
			entries[entry.ID] = entry
			entryIDs[entry.ID] = struct{}{}
			accountSet[accountID] = struct{}{}
			for _, code := range statementCodesForAccount(accountType, subclass, category, cashFlowActivity, cashEquivalent) {
				statementItems[code] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(result.MoveLines) == 0 {
			return errTraceStatementItemNotFound
		}
		result.AccountIDs = uuidSliceFromSet(accountSet)
		for _, entry := range entries {
			result.JournalEntries = append(result.JournalEntries, entry)
		}
		sort.Slice(result.AccountIDs, func(i, j int) bool { return result.AccountIDs[i].String() < result.AccountIDs[j].String() })
		sort.Slice(result.JournalEntries, func(i, j int) bool {
			return result.JournalEntries[i].ID.String() < result.JournalEntries[j].ID.String()
		})
		for code := range statementItems {
			result.StatementItems = append(result.StatementItems, code)
		}
		sort.Strings(result.StatementItems)

		auditRows, err := tx.Query(ctx, `SELECT id,previous_log_hash,current_log_hash
			FROM financial_audit_logs WHERE company_id=$1 AND entity_id=ANY($2::uuid[]) ORDER BY id`,
			companyID, uuidSliceFromSet(entryIDs))
		if err != nil {
			return fmt.Errorf("query event audit chain proof: %w", err)
		}
		proof := make([]string, 0)
		for auditRows.Next() {
			var id int64
			var previousHash, currentHash string
			if err := auditRows.Scan(&id, &previousHash, &currentHash); err != nil {
				auditRows.Close()
				return err
			}
			proof = append(proof, fmt.Sprintf("%d:%s:%s", id, previousHash, currentHash))
		}
		if err := auditRows.Err(); err != nil {
			auditRows.Close()
			return err
		}
		auditRows.Close()
		result.AuditChainProof = strings.Join(proof, "|")
		return nil
	})
	if err != nil {
		return reporting.TraceResult{}, err
	}
	return result, nil
}

func statementCodesForAccount(
	accountType, subclass, category, cashFlowActivity string,
	cashEquivalent bool,
) []string {
	codes := make([]string, 0, 3)
	switch accountType {
	case "asset":
		codes = append(codes, "TOTAL_ASSETS")
		if isCurrentAssetSubclass(subclass) {
			codes = append(codes, "CURRENT_ASSETS")
		} else {
			codes = append(codes, "NONCURRENT_ASSETS")
		}
	case "liability":
		codes = append(codes, "TOTAL_LIABILITIES", "TOTAL_LIABILITIES_AND_EQUITY")
		if isCurrentLiabilitySubclass(subclass) {
			codes = append(codes, "CURRENT_LIABILITIES")
		} else {
			codes = append(codes, "NONCURRENT_LIABILITIES")
		}
	case "equity":
		codes = append(codes, "EQUITY", "TOTAL_LIABILITIES_AND_EQUITY")
	case "income":
		codes = append(codes, "NET_PROFIT", "PROFIT_BEFORE_TAX", "TOTAL_COMPREHENSIVE_INCOME")
		if subclass == "operating_revenue" || category == "operating" {
			codes = append(codes, "REVENUE")
		}
		if category == "investing" {
			codes = append(codes, "INVESTING_INCOME")
		}
		if category == "financing" {
			codes = append(codes, "FINANCING_INCOME")
		}
	case "expense":
		codes = append(codes, "NET_PROFIT", "PROFIT_BEFORE_TAX", "TOTAL_COMPREHENSIVE_INCOME")
		switch subclass {
		case "cost_of_sales":
			codes = append(codes, "COST_OF_SALES")
		case "selling_distribution_expense":
			codes = append(codes, "SELLING_EXPENSES")
		case "general_admin_expense":
			codes = append(codes, "ADMIN_EXPENSES")
		case "depreciation_amortization_expense":
			codes = append(codes, "DEPRECIATION_EXPENSES")
		case "other_operating_expense":
			codes = append(codes, "OTHER_OPERATING_EXPENSES")
		case "finance_cost":
			codes = append(codes, "FINANCE_COSTS")
		case "tax_expense", "income_tax_expense":
			codes = append(codes, "INCOME_TAX_ZAKAT")
		}
	}
	switch cashFlowActivity {
	case "operating":
		codes = append(codes, "OPERATING_CASH_FLOW")
	case "investing":
		codes = append(codes, "INVESTING_CASH_FLOW")
	case "financing":
		codes = append(codes, "FINANCING_CASH_FLOW")
	}
	if cashEquivalent {
		codes = append(codes, "OPENING_CASH", "CLOSING_CASH")
	}
	return codes
}

func isCurrentAssetSubclass(subclass string) bool {
	switch strings.ToLower(strings.TrimSpace(subclass)) {
	case "cash_and_equivalents", "trade_receivables", "inventories", "prepayments_and_other_debit",
		"current_assets", "accounts_receivable", "bank", "cash":
		return true
	default:
		return false
	}
}

func isCurrentLiabilitySubclass(subclass string) bool {
	switch strings.ToLower(strings.TrimSpace(subclass)) {
	case "trade_payables", "short_term_borrowings", "current_lease_liabilities",
		"accruals_and_other_credit", "current_tax_liabilities", "current_liabilities",
		"accounts_payable", "payable":
		return true
	default:
		return false
	}
}

func (d *Driver) statementAmountForTrace(
	ctx context.Context,
	companyID uuid.UUID,
	reportID, lineItem string,
	start, end time.Time,
	currency string,
) (decimal.Decimal, bool, error) {
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(lineItem), "-", "_"))
	code = strings.ReplaceAll(code, " ", "_")
	report := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(reportID), "-", "_"))
	report = strings.ReplaceAll(report, " ", "_")
	switch {
	case strings.Contains(report, "BALANCE") || strings.Contains(report, "_BS_"):
		if end.IsZero() {
			return decimal.Zero, false, nil
		}
		statement, err := reporting.NewBalanceSheetEngine(d).GenerateForCompanyContext(ctx, companyID, end, currency)
		if err != nil {
			return decimal.Zero, false, err
		}
		values := map[string]decimal.Decimal{
			"CURRENT_ASSETS":               statement.CurrentAssets,
			"NONCURRENT_ASSETS":            statement.NonCurrentAssets,
			"TOTAL_ASSETS":                 statement.TotalAssets,
			"CURRENT_LIABILITIES":          statement.CurrentLiabilities,
			"NONCURRENT_LIABILITIES":       statement.NonCurrentLiabilities,
			"TOTAL_LIABILITIES":            statement.TotalLiabilities,
			"EQUITY":                       statement.Equity,
			"TRANSLATION_RESERVE":          statement.TranslationReserve,
			"TOTAL_LIABILITIES_AND_EQUITY": statement.TotalLiabilitiesAndEquity,
		}
		amount, ok := values[code]
		return amount, ok, nil
	case strings.Contains(report, "INCOME") || strings.Contains(report, "_IS_"):
		statement, err := reporting.NewIncomeStatementEngine(d).GenerateForCompanyContext(ctx, companyID, start, end, currency)
		if err != nil {
			return decimal.Zero, false, err
		}
		values := map[string]decimal.Decimal{
			"REVENUE":                         statement.Revenue,
			"INVESTING_INCOME":                statement.InvestingIncome,
			"FINANCING_INCOME":                statement.FinancingIncome,
			"COST_OF_SALES":                   statement.CostOfSales,
			"GROSS_PROFIT":                    statement.GrossProfit,
			"SELLING_EXPENSES":                statement.SellingExpenses,
			"ADMIN_EXPENSES":                  statement.AdministrativeExpenses,
			"DEPRECIATION_EXPENSES":           statement.DepreciationAndProvisionExpenses,
			"OTHER_OPERATING_EXPENSES":        statement.OtherOperatingExpenses,
			"OPERATING_PROFIT":                statement.OperatingProfit,
			"PROFIT_BEFORE_FINANCING_AND_TAX": statement.ProfitBeforeFinancingAndTax,
			"FINANCE_INCOME":                  statement.FinanceIncome,
			"FINANCE_COSTS":                   statement.FinanceCosts,
			"PROFIT_BEFORE_TAX":               statement.ProfitBeforeTax,
			"INCOME_TAX_ZAKAT":                statement.TaxExpense,
			"DISCONTINUED_OPERATIONS":         statement.DiscontinuedOperations,
			"NET_PROFIT":                      statement.NetProfit,
			"OCI":                             statement.OCI,
			"TOTAL_COMPREHENSIVE_INCOME":      statement.TotalComprehensiveIncome,
		}
		amount, ok := values[code]
		return amount, ok, nil
	case strings.Contains(report, "CASH_FLOW"):
		statement, err := reporting.NewCashFlowEngine(d).GenerateForCompanyContext(ctx, companyID, start, end, currency)
		if err != nil {
			return decimal.Zero, false, err
		}
		values := map[string]decimal.Decimal{
			"OPERATING_CASH_FLOW":      statement.OperatingCashFlow,
			"INVESTING_CASH_FLOW":      statement.InvestingCashFlow,
			"FINANCING_CASH_FLOW":      statement.FinancingCashFlow,
			"NET_CHANGE_IN_CASH":       statement.NetChangeInCash,
			"EFFECT_OF_EXCHANGE_RATES": statement.EffectOfExchangeRatesOnCash,
			"OPENING_CASH":             statement.OpeningCash,
			"CLOSING_CASH":             statement.ClosingCash,
		}
		amount, ok := values[code]
		return amount, ok, nil
	default:
		return decimal.Zero, false, nil
	}
}

func uuidSliceFromSet(values map[uuid.UUID]struct{}) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	return ids
}
