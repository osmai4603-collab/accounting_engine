package posting

import (
	"context"
	"errors"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
)

var ErrCostCenterRequired = errors.New("cost center is required for this account")

type FinancialDimensionPolicy interface {
	RequiresCostCenter(ctx context.Context, companyID, accountID uuid.UUID) (bool, error)
	ValidateCostCenter(ctx context.Context, companyID, costCenterID uuid.UUID) error
}

type FinancialDimensionsEngine struct {
	policy FinancialDimensionPolicy
}

func NewFinancialDimensionsEngine(policy FinancialDimensionPolicy) *FinancialDimensionsEngine {
	return &FinancialDimensionsEngine{policy: policy}
}

func (e *FinancialDimensionsEngine) ValidateEntry(ctx context.Context, entry JournalEntry) error {
	if entry.CompanyID == uuid.Nil {
		return masterdata.ErrCompanyScopeRequired
	}
	for _, line := range entry.Lines {
		required, err := e.policy.RequiresCostCenter(ctx, entry.CompanyID, line.AccountID)
		if err != nil {
			return err
		}
		if !required && line.CostCenterID == nil {
			continue
		}
		if line.CostCenterID == nil {
			return ErrCostCenterRequired
		}
		if err := e.policy.ValidateCostCenter(ctx, entry.CompanyID, *line.CostCenterID); err != nil {
			return err
		}
	}
	return nil
}
