package posting

import (
	"context"
	"errors"

	"accounting_engine/internal/masterdata"
	"github.com/google/uuid"
)

var (
	ErrCostCenterRequired         = errors.New("cost center is required for this account")
	ErrDimensionPolicyUnavailable = errors.New("financial dimension policy is required")
)

type FinancialDimensionPolicy interface {
	RequiresCostCenter(ctx context.Context, companyID, accountID uuid.UUID) (bool, error)
	ValidateCostCenter(ctx context.Context, companyID, costCenterID uuid.UUID) error
	ValidateAnalyticalDimension(ctx context.Context, companyID uuid.UUID, kind masterdata.AnalyticalDimensionKind, dimensionID uuid.UUID) error
}

type ApprovalGate interface {
	IsApproved(ctx context.Context, companyID, entryID uuid.UUID) (bool, error)
}

type FinancialDimensionsEngine struct {
	policy FinancialDimensionPolicy
}

func NewFinancialDimensionsEngine(policy FinancialDimensionPolicy) *FinancialDimensionsEngine {
	return &FinancialDimensionsEngine{policy: policy}
}

func (e *FinancialDimensionsEngine) ValidateEntry(ctx context.Context, entry JournalEntry) error {
	if e == nil || e.policy == nil {
		return ErrDimensionPolicyUnavailable
	}
	if entry.CompanyID == uuid.Nil {
		return masterdata.ErrCompanyScopeRequired
	}
	for _, line := range entry.Lines {
		required, err := e.policy.RequiresCostCenter(ctx, entry.CompanyID, line.AccountID)
		if err != nil {
			return err
		}
		if required && line.CostCenterID == nil {
			return ErrCostCenterRequired
		}
		if line.CostCenterID != nil {
			if err := e.policy.ValidateCostCenter(ctx, entry.CompanyID, *line.CostCenterID); err != nil {
				return err
			}
		}
		for _, dimension := range []struct {
			kind masterdata.AnalyticalDimensionKind
			id   *uuid.UUID
		}{
			{kind: masterdata.DimensionProject, id: line.ProjectID},
			{kind: masterdata.DimensionSegment, id: line.SegmentID},
			{kind: masterdata.DimensionBusinessUnit, id: line.BusinessUnitID},
		} {
			if dimension.id == nil {
				continue
			}
			if err := e.policy.ValidateAnalyticalDimension(ctx, entry.CompanyID, dimension.kind, *dimension.id); err != nil {
				return err
			}
		}
	}
	return nil
}
