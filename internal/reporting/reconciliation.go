package reporting

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrBalanceReconciliationUnavailable = errors.New("balance reconciliation is not supported by this repository")
	ErrInvalidReconciliationScope       = errors.New("company, fiscal period, and currency are required for reconciliation")
)

type BalanceReconciliationRepository interface {
	AssertBalancesMatchMoves(context.Context, uuid.UUID, uuid.UUID, string) error
}

type BalanceReconciliationEngine struct {
	repository Repository
}

func NewBalanceReconciliationEngine(repository Repository) *BalanceReconciliationEngine {
	return &BalanceReconciliationEngine{repository: repository}
}

func (e *BalanceReconciliationEngine) Reconcile(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) error {
	if ctx == nil {
		return errors.New("reconciliation context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if companyID == uuid.Nil || fiscalPeriodID == uuid.Nil || currencyID == "" {
		return ErrInvalidReconciliationScope
	}
	repository, ok := e.repository.(BalanceReconciliationRepository)
	if !ok {
		return ErrBalanceReconciliationUnavailable
	}
	if err := repository.AssertBalancesMatchMoves(ctx, companyID, fiscalPeriodID, currencyID); err != nil {
		return fmt.Errorf("reconcile account balances: %w", err)
	}
	return nil
}
