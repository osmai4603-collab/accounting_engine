package ifrs15

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func TestVariableConsideration_ExpectedValue(t *testing.T) {
	// Scenario: A company sells goods with a performance bonus dependent on customer satisfaction:
	// - 60% probability of $10,000 bonus
	// - 30% probability of $5,000 bonus
	// - 10% probability of $0 bonus
	// Expected Value = 10,000*0.6 + 5,000*0.3 + 0*0.1 = 6,000 + 1,500 + 0 = $7,500
	m10k, _ := financialtypes.NewMoneyFromString("10000.00", "USD")
	m5k, _ := financialtypes.NewMoneyFromString("5000.00", "USD")
	m0, _ := financialtypes.NewMoneyFromString("0.00", "USD")

	outcomes := []ConsiderationOutcome{
		{Amount: m10k, Probability: decimal.RequireFromString("0.60"), Description: "Full bonus"},
		{Amount: m5k, Probability: decimal.RequireFromString("0.30"), Description: "Partial bonus"},
		{Amount: m0, Probability: decimal.RequireFromString("0.10"), Description: "No bonus"},
	}

	zeroLimit, _ := financialtypes.Zero("USD")
	estimate, err := EstimateVariableConsideration(ExpectedValue, outcomes, false, zeroLimit, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("EstimateVariableConsideration failed: %v", err)
	}

	wantAmount := decimal.RequireFromString("7500.00")
	if !estimate.EstimatedAmount.Amount().Equal(wantAmount) {
		t.Errorf("estimated amount = %s, want %s", estimate.EstimatedAmount.Amount(), wantAmount)
	}
	if !estimate.FinalAmount.Amount().Equal(wantAmount) {
		t.Errorf("final amount = %s, want %s", estimate.FinalAmount.Amount(), wantAmount)
	}
}

func TestVariableConsideration_MostLikelyAmount(t *testing.T) {
	// Scenario: An entity achieves a $20,000 milestone bonus if completed on time (80% probability), or $0 (20% probability)
	// Most likely amount = $20,000
	m20k, _ := financialtypes.NewMoneyFromString("20000.00", "USD")
	m0, _ := financialtypes.NewMoneyFromString("0.00", "USD")

	outcomes := []ConsiderationOutcome{
		{Amount: m20k, Probability: decimal.RequireFromString("0.80"), Description: "Milestone achieved"},
		{Amount: m0, Probability: decimal.RequireFromString("0.20"), Description: "Milestone missed"},
	}

	zeroLimit, _ := financialtypes.Zero("USD")
	estimate, err := EstimateVariableConsideration(MostLikelyAmount, outcomes, false, zeroLimit, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("EstimateVariableConsideration failed: %v", err)
	}

	wantAmount := decimal.RequireFromString("20000.00")
	if !estimate.EstimatedAmount.Amount().Equal(wantAmount) {
		t.Errorf("estimated amount = %s, want %s", estimate.EstimatedAmount.Amount(), wantAmount)
	}
	if !estimate.FinalAmount.Amount().Equal(wantAmount) {
		t.Errorf("final amount = %s, want %s", estimate.FinalAmount.Amount(), wantAmount)
	}
}

func TestVariableConsideration_Constraining(t *testing.T) {
	// IFRS 15.56-58: If it is NOT highly probable that a significant reversal will not occur,
	// the amount recognized must be constrained.
	// Estimated expected value is $7,500, but only $3,000 is highly probable not to reverse.
	m10k, _ := financialtypes.NewMoneyFromString("10000.00", "USD")
	m5k, _ := financialtypes.NewMoneyFromString("5000.00", "USD")
	m0, _ := financialtypes.NewMoneyFromString("0.00", "USD")

	outcomes := []ConsiderationOutcome{
		{Amount: m10k, Probability: decimal.RequireFromString("0.60")},
		{Amount: m5k, Probability: decimal.RequireFromString("0.30")},
		{Amount: m0, Probability: decimal.RequireFromString("0.10")},
	}

	limit, _ := financialtypes.NewMoneyFromString("3000.00", "USD")
	estimate, err := EstimateVariableConsideration(
		ExpectedValue,
		outcomes,
		true,
		limit,
		"Market volatility creates significant risk of reversal",
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("EstimateVariableConsideration with constraint failed: %v", err)
	}

	if !estimate.EstimatedAmount.Amount().Equal(decimal.RequireFromString("7500.00")) {
		t.Errorf("unconstrained estimate = %s, want 7500.00", estimate.EstimatedAmount.Amount())
	}
	if !estimate.FinalAmount.Amount().Equal(decimal.RequireFromString("3000.00")) {
		t.Errorf("final constrained amount = %s, want 3000.00", estimate.FinalAmount.Amount())
	}
	if !estimate.IsConstrained {
		t.Error("expected IsConstrained to be true")
	}

	// Constraint exceeding estimated amount must fail
	tooHighLimit, _ := financialtypes.NewMoneyFromString("8000.00", "USD")
	_, err = EstimateVariableConsideration(ExpectedValue, outcomes, true, tooHighLimit, "invalid", time.Now().UTC())
	if !errors.Is(err, ErrVariableConstraintExceeded) {
		t.Errorf("expected ErrVariableConstraintExceeded, got %v", err)
	}
}

func TestVariableConsideration_ValidationErrors(t *testing.T) {
	// Empty outcomes
	zero, _ := financialtypes.Zero("USD")
	if _, err := EstimateVariableConsideration(ExpectedValue, nil, false, zero, "", time.Now().UTC()); !errors.Is(err, ErrInvalidVariableConsideration) {
		t.Errorf("expected ErrInvalidVariableConsideration for empty outcomes, got %v", err)
	}

	// Probabilities not summing to 1
	m100, _ := financialtypes.NewMoneyFromString("100.00", "USD")
	invalidOutcomes := []ConsiderationOutcome{
		{Amount: m100, Probability: decimal.RequireFromString("0.50")},
		{Amount: m100, Probability: decimal.RequireFromString("0.20")},
	}
	if _, err := EstimateVariableConsideration(ExpectedValue, invalidOutcomes, false, zero, "", time.Now().UTC()); !errors.Is(err, ErrInvalidProbabilitySum) {
		t.Errorf("expected ErrInvalidProbabilitySum, got %v", err)
	}
}

func TestVariableConsideration_SubsequentReassessment(t *testing.T) {
	// IFRS 15.58: End of reporting period reassessment
	obl := PerformanceObligation{
		ID:                 uuid.New(),
		Description:        "Consulting Services",
		StandalonePrice:    money(t, "1000.00", "USD"),
		RecognitionPattern: OverTime,
		StartDate:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:            time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	basePrice := money(t, "1000.00", "USD")
	contract, err := NewContract(uuid.New(), uuid.New(), basePrice, []PerformanceObligation{obl})
	if err != nil {
		t.Fatal(err)
	}

	// Initial estimate was constrained at $200 bonus
	m500, _ := financialtypes.NewMoneyFromString("500.00", "USD")
	m0, _ := financialtypes.NewMoneyFromString("0.00", "USD")
	outcomes := []ConsiderationOutcome{
		{Amount: m500, Probability: decimal.RequireFromString("0.80")},
		{Amount: m0, Probability: decimal.RequireFromString("0.20")},
	}
	limit200, _ := financialtypes.NewMoneyFromString("200.00", "USD")
	initEst, err := EstimateVariableConsideration(MostLikelyAmount, outcomes, true, limit200, "Initial constraint", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	reassessedContract, err := ReassessVariableConsideration(contract, basePrice, initEst)
	if err != nil {
		t.Fatalf("ReassessVariableConsideration failed: %v", err)
	}
	if !reassessedContract.TransactionPrice.Amount().Equal(decimal.RequireFromString("1200.00")) {
		t.Errorf("reassessed price = %s, want 1200.00", reassessedContract.TransactionPrice.Amount())
	}
	if reassessedContract.Status != ContractStatusModified {
		t.Errorf("status = %s, want %s", reassessedContract.Status, ContractStatusModified)
	}

	// At year end, uncertainty resolved: full $500 unconstrained
	unconstrainedEst, err := EstimateVariableConsideration(MostLikelyAmount, outcomes, false, limit200, "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	finalContract, err := ReassessVariableConsideration(reassessedContract, basePrice, unconstrainedEst)
	if err != nil {
		t.Fatalf("final ReassessVariableConsideration failed: %v", err)
	}
	if !finalContract.TransactionPrice.Amount().Equal(decimal.RequireFromString("1500.00")) {
		t.Errorf("final price = %s, want 1500.00", finalContract.TransactionPrice.Amount())
	}
}

