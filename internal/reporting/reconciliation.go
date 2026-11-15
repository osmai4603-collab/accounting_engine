package reporting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/observability"
	"github.com/shopspring/decimal"
)

var (
	ErrBalanceReconciliationUnavailable    = errors.New("balance reconciliation is not supported by this repository")
	ErrBalanceReconciliationDetailRequired = errors.New("detailed balance reconciliation source data is required")
	ErrBalanceReconciliationNoData         = errors.New("balance reconciliation returned no comparable accounts")
	ErrInvalidReconciliationScope          = errors.New("company, fiscal period, and currency are required for reconciliation")
	ErrBalanceDriftDetected                = errors.New("balance reconciliation drift detected")
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
	metrics    *observability.Metrics
}

func NewBalanceReconciliationEngine(repository Repository) *BalanceReconciliationEngine {
	return &BalanceReconciliationEngine{repository: repository}
}

// SetMetrics attaches the reconciliation discrepancy gauge.
func (e *BalanceReconciliationEngine) SetMetrics(metrics *observability.Metrics) {
	e.metrics = metrics
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
	if e == nil || e.repository == nil {
		return ReconciliationReport{}, fmt.Errorf("%w: reconciliation repository", ErrReportingDependencyMissing)
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
			e.setDiscrepancyCount(1)
			return ReconciliationReport{}, fmt.Errorf("reconcile account balances: %w", err)
		}
		e.setDiscrepancyCount(len(report.Drifts))
		if report.CompanyID != companyID || report.FiscalPeriodID != fiscalPeriodID || report.CurrencyID != currencyID {
			return ReconciliationReport{}, fmt.Errorf("%w: returned scope does not match request", ErrInvalidReconciliationScope)
		}
		if report.AccountsChecked <= 0 {
			return ReconciliationReport{}, ErrBalanceReconciliationNoData
		}
		if report.ZeroDrift && len(report.Drifts) != 0 {
			return ReconciliationReport{}, fmt.Errorf("%w: zero-drift result contains account exceptions", ErrBalanceDriftDetected)
		}
		return report, report.Err()
	}
	repository, ok := e.repository.(BalanceReconciliationRepository)
	if !ok {
		return ReconciliationReport{}, ErrBalanceReconciliationUnavailable
	}
	if err := repository.AssertBalancesMatchMoves(ctx, companyID, fiscalPeriodID, currencyID); err != nil {
		e.setDiscrepancyCount(1)
		return ReconciliationReport{}, fmt.Errorf("reconcile account balances: %w", err)
	}
	e.setDiscrepancyCount(0)
	return ReconciliationReport{}, ErrBalanceReconciliationDetailRequired
}

func (e *BalanceReconciliationEngine) setDiscrepancyCount(count int) {
	if e != nil && e.metrics != nil {
		e.metrics.SetReconciliationDiscrepancyCount(count)
	}
}
