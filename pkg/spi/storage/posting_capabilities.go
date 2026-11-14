package storage

import (
	"context"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

// ApprovalGate is an optional application-level approval decision source.
type ApprovalGate interface {
	IsApproved(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (bool, error)
}

// FinancialDimensionPolicy is an optional source for account dimension
// requirements and dimension validation. Dimension kind values are the
// canonical lower-case names: project, segment, or business_unit.
type FinancialDimensionPolicy interface {
	RequiresCostCenter(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (bool, error)
	ValidateCostCenter(ctx context.Context, companyID types.CompanyID, costCenterID types.CostCenterID) error
	ValidateAnalyticalDimension(ctx context.Context, companyID types.CompanyID, kind string, dimensionID uuid.UUID) error
}

// MakerCheckerPolicy supplies the configured functional-amount threshold for
// a company. configured=false means no threshold is configured.
type MakerCheckerPolicy interface {
	MakerCheckerThreshold(ctx context.Context, companyID types.CompanyID) (decimal.Decimal, bool, error)
}

// PostingCapabilities contains optional posting controls supplied by an SDK
// composition root. Nil fields mean that the capability is unavailable.
type PostingCapabilities struct {
	ApprovalGate        ApprovalGate
	FinancialDimensions FinancialDimensionPolicy
	MakerCheckerPolicy  MakerCheckerPolicy
}
