package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/reporting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

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
			WHERE company_id=$1 AND status=$2 AND entry_date BETWEEN $3 AND $4
			ORDER BY entry_date`, companyID, posting.EntryPosted, start, end)
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
