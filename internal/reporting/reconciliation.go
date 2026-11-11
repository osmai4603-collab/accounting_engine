package reporting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrBalanceReconciliationUnavailable = errors.New("balance reconciliation is not supported by this repository")
	ErrInvalidReconciliationScope       = errors.New("company, fiscal period, and currency are required for reconciliation")
	ErrBalanceDriftDetected             = errors.New("balance reconciliation drift detected")
)

// BalanceReconciliationRepository is the assert-only contract: it either
// confirms that the materialized account_balances rows match the posted move
// lines exactly, or reports the mismatch count.
type BalanceReconciliationRepository interface {
	AssertBalancesMatchMoves(context.Context, uuid.UUID, uuid.UUID, string) error
}

// BalanceReconciliationReporter is the detailed contract: repositories that
// support it return a per-account report exposing the exact ledger and
// materialized-balance positions plus the drift between them.
type BalanceReconciliationReporter interface {
	BuildBalanceReconciliationReport(context.Context, uuid.UUID, uuid.UUID, string) (ReconciliationReport, error)
}

// ReconciliationAccountDrift describes one account whose materialized
// account_balances row no longer equals the sum of its posted move lines.
// Amounts are expressed in integer minor units of the reconciliation currency
// so the comparison is exact with zero rounding tolerance.
type ReconciliationAccountDrift struct {
	AccountID          uuid.UUID
	LedgerDebitMinor   decimal.Decimal
	LedgerCreditMinor  decimal.Decimal
	BalanceDebitMinor  decimal.Decimal
	BalanceCreditMinor decimal.Decimal
	DebitDriftMinor    decimal.Decimal
	CreditDriftMinor   decimal.Decimal
}

// ReconciliationReport is the outcome of a balance reconciliation run.
// ZeroDrift is the mandatory production state: any drift, no matter how small,
// means the materialized balances diverged from the authoritative ledger.
type ReconciliationReport struct {
	CompanyID       uuid.UUID
	FiscalPeriodID  uuid.UUID
	CurrencyID      string
	AccountsChecked int
	Drifts          []ReconciliationAccountDrift
	ZeroDrift       bool
	GeneratedAt     time.Time
}

// DriftedAccounts returns how many accounts drifted from the ledger.
func (r ReconciliationReport) DriftedAccounts() int {
	return len(r.Drifts)
}

// Err returns the reconciliation verdict: nil only when zero drift holds.
func (r ReconciliationReport) Err() error {
	if r.ZeroDrift {
		return nil
	}
	return fmt.Errorf("%w: %d of %d account(s) differ between posted move lines and account_balances",
		ErrBalanceDriftDetected, len(r.Drifts), r.AccountsChecked)
}

type BalanceReconciliationEngine struct {
	repository Repository
}

func NewBalanceReconciliationEngine(repository Repository) *BalanceReconciliationEngine {
	return &BalanceReconciliationEngine{repository: repository}
}

// Reconcile compares the materialized account_balances rows of a company,
// fiscal period, and currency against the functional minor units stored on
// the posted move lines. It always returns the full report; the returned
// error is non-nil exactly when drift was detected (or the run failed), so
// callers can fail fast on the verdict while operators inspect the detail.
func (e *BalanceReconciliationEngine) Reconcile(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) (ReconciliationReport, error) {
	if ctx == nil {
		return ReconciliationReport{}, errors.New("reconciliation context is required")
	}
	if err := ctx.Err(); err != nil {
		return ReconciliationReport{}, err
	}
	if companyID == uuid.Nil || fiscalPeriodID == uuid.Nil || currencyID == "" {
		return ReconciliationReport{}, ErrInvalidReconciliationScope
	}
	if reporter, ok := e.repository.(BalanceReconciliationReporter); ok {
		report, err := reporter.BuildBalanceReconciliationReport(ctx, companyID, fiscalPeriodID, currencyID)
		if err != nil {
			return ReconciliationReport{}, fmt.Errorf("reconcile account balances: %w", err)
		}
		return report, report.Err()
	}
	repository, ok := e.repository.(BalanceReconciliationRepository)
	if !ok {
		return ReconciliationReport{}, ErrBalanceReconciliationUnavailable
	}
	if err := repository.AssertBalancesMatchMoves(ctx, companyID, fiscalPeriodID, currencyID); err != nil {
		return ReconciliationReport{}, fmt.Errorf("reconcile account balances: %w", err)
	}
	return ReconciliationReport{
		CompanyID:       companyID,
		FiscalPeriodID:  fiscalPeriodID,
		CurrencyID:      currencyID,
		AccountsChecked: 0,
		Drifts:          nil,
		ZeroDrift:       true,
		GeneratedAt:     time.Now().UTC(),
	}, nil
}
