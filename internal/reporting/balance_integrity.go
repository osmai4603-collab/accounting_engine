package reporting

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrBalanceRebuildUnavailable  = errors.New("balance rebuild is not supported by this repository")
	ErrInvalidBalanceRebuildScope = errors.New("company, fiscal period, and currency are required for balance rebuild")
)

type BalanceRebuildRepository interface {
	RebuildBalancesFromLines(context.Context, uuid.UUID, uuid.UUID, string) error
}

type BalanceDriftAlertRecorder interface {
	RecordBalanceDriftAlert(context.Context, ReconciliationReport) error
}

// BalanceIntegrityUnit combines the existing exact reconciliation report with
// an opt-in repair operation sourced exclusively from posted ledger lines.
type BalanceIntegrityUnit struct {
	repository Repository
}

func NewBalanceIntegrityUnit(repository Repository) *BalanceIntegrityUnit {
	return &BalanceIntegrityUnit{repository: repository}
}

func (u *BalanceIntegrityUnit) Reconcile(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) (ReconciliationReport, error) {
	report, err := NewBalanceReconciliationEngine(u.repository).Reconcile(ctx, companyID, fiscalPeriodID, currencyID)
	if errors.Is(err, ErrBalanceDriftDetected) {
		if recorder, ok := u.repository.(BalanceDriftAlertRecorder); ok {
			if auditErr := recorder.RecordBalanceDriftAlert(ctx, report); auditErr != nil {
				return report, errors.Join(err, fmt.Errorf("record critical balance drift alert: %w", auditErr))
			}
		}
	}
	return report, err
}

// RebuildBalancesFromLines atomically replaces one company's materialized
// period activity with the amounts in its posted journal lines.
func (u *BalanceIntegrityUnit) RebuildBalancesFromLines(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) error {
	if ctx == nil {
		return errors.New("balance rebuild context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if companyID == uuid.Nil || fiscalPeriodID == uuid.Nil || currencyID == "" {
		return ErrInvalidBalanceRebuildScope
	}
	repository, ok := u.repository.(BalanceRebuildRepository)
	if !ok {
		return ErrBalanceRebuildUnavailable
	}
	if err := repository.RebuildBalancesFromLines(ctx, companyID, fiscalPeriodID, currencyID); err != nil {
		return fmt.Errorf("rebuild materialized account balances: %w", err)
	}
	return nil
}
