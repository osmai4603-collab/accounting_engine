package ifrs9

import (
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

type pdByHorizon struct{}

func (pdByHorizon) ProbabilityOfDefault(_ GeneralExposure, horizon time.Duration) (decimal.Decimal, error) {
	if horizon <= 365*24*time.Hour {
		return decimal.RequireFromString("0.1"), nil
	}
	return decimal.RequireFromString("0.2"), nil
}

type fixedLGD struct{}

func (fixedLGD) LossGivenDefault(GeneralExposure) (decimal.Decimal, error) {
	return decimal.RequireFromString("0.5"), nil
}

func TestCalculateGeneralECLStageHorizonAndEIRDiscount(t *testing.T) {
	ledgerBalance, _ := financialtypes.NewMoney(decimal.NewFromInt(900), "USD")
	undrawn, _ := financialtypes.NewMoney(decimal.NewFromInt(100), "USD")
	exposure := GeneralExposure{
		ID: uuid.New(), Stage: Stage1, LedgerBalance: ledgerBalance, UndrawnCommitments: undrawn,
		RemainingLife:         2 * 365 * 24 * time.Hour,
		EffectiveInterestRate: decimal.Zero,
	}

	stage1, err := CalculateGeneralECL(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		[]GeneralExposure{exposure}, pdByHorizon{}, fixedLGD{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := stage1.RequiredECL.Amount().StringFixed(2); got != "50.00" {
		t.Fatalf("Stage 1 ECL = %s, want 50.00", got)
	}
	if got := stage1.Exposures[0].EAD.Amount().StringFixed(2); got != "1000.00" {
		t.Fatalf("calculated EAD = %s, want 1000.00", got)
	}
	exposure.Stage = Stage2
	stage2, err := CalculateGeneralECL(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		[]GeneralExposure{exposure}, pdByHorizon{}, fixedLGD{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := stage2.RequiredECL.Amount().StringFixed(2); got != "100.00" {
		t.Fatalf("Stage 2 ECL = %s, want 100.00", got)
	}
	exposure.Stage = Stage1
	exposure.EffectiveInterestRate = decimal.RequireFromString("0.1")
	discounted, err := CalculateGeneralECL(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		[]GeneralExposure{exposure}, pdByHorizon{}, fixedLGD{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := discounted.RequiredECL.Amount().StringFixed(2); got != "45.45" {
		t.Fatalf("discounted ECL = %s, want 45.45", got)
	}
}

func TestScenarioPDModelWeightsForwardLookingScenarios(t *testing.T) {
	model := ScenarioPDModel{Scenarios: []PDScenario{
		{Name: "base", Weight: decimal.RequireFromString("0.6"), PD12Month: decimal.RequireFromString("0.1"), PDLifetime: decimal.RequireFromString("0.2")},
		{Name: "downside", Weight: decimal.RequireFromString("0.4"), PD12Month: decimal.RequireFromString("0.3"), PDLifetime: decimal.RequireFromString("0.5")},
	}}
	pd, err := model.ProbabilityOfDefault(GeneralExposure{Stage: Stage1}, 365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !pd.Equal(decimal.RequireFromString("0.18")) {
		t.Fatalf("weighted 12-month PD = %s, want 0.18", pd)
	}
	pd, err = model.ProbabilityOfDefault(GeneralExposure{Stage: Stage2}, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !pd.Equal(decimal.RequireFromString("0.32")) {
		t.Fatalf("weighted lifetime PD = %s, want 0.32", pd)
	}
}

func TestDiscountAtEIRRejectsZeroCompoundingBase(t *testing.T) {
	if _, err := DiscountAtEIR(decimal.NewFromInt(-1), 365*24*time.Hour); err != ErrInvalidRiskEstimate {
		t.Fatalf("discount at -100%% EIR error = %v, want ErrInvalidRiskEstimate", err)
	}
}
