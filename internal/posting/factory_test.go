package posting

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	"github.com/shopspring/decimal"
)

type factoryApprovalGate struct{}

func (factoryApprovalGate) IsApproved(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

type factoryDimensionPolicy struct{}

func (factoryDimensionPolicy) RequiresCostCenter(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

func (factoryDimensionPolicy) ValidateCostCenter(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (factoryDimensionPolicy) ValidateAnalyticalDimension(context.Context, uuid.UUID, masterdata.AnalyticalDimensionKind, uuid.UUID) error {
	return nil
}

type factoryMakerCheckerPolicy struct{}

func (factoryMakerCheckerPolicy) MakerCheckerThreshold(context.Context, uuid.UUID) (decimal.Decimal, bool, error) {
	return decimal.Zero, false, nil
}

func TestNewConfiguredEngineValidatesAndWiresRequiredCapabilities(t *testing.T) {
	config := Config{
		RequireApproval:            true,
		RequireFinancialDimensions: true,
		RequireMakerCheckerPolicy:  true,
		RequirePolicy:              true,
	}
	if _, err := NewConfiguredEngine(NewMockRepository(), nil, config, Capabilities{}); !errors.Is(err, ErrRequiredCapabilityUnavailable) {
		t.Fatalf("factory error = %v, want required capability error", err)
	}

	capabilities := Capabilities{
		ApprovalGate:        factoryApprovalGate{},
		FinancialDimensions: factoryDimensionPolicy{},
		MakerCheckerPolicy:  factoryMakerCheckerPolicy{},
		PolicyUnit:          &policy.Unit{},
	}
	engine, err := NewConfiguredEngine(NewMockRepository(), nil, Config{
		RequireApproval:            true,
		RequireFinancialDimensions: true,
		RequireMakerCheckerPolicy:  true,
	}, capabilities)
	if err != nil {
		t.Fatalf("factory with available capabilities: %v", err)
	}
	if engine.approvalGate == nil || engine.dimensions == nil || engine.makerCheckerPolicy == nil {
		t.Fatal("configured capabilities were not wired into the engine")
	}
}
