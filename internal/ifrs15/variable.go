package ifrs15

import (
	"errors"
	"fmt"
	"time"

	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidVariableConsideration = errors.New("invalid variable consideration")
	ErrVariableConstraintExceeded   = errors.New("constrained amount exceeds estimated variable consideration")
	ErrInvalidProbabilitySum        = errors.New("probabilities must sum to 1.0")
)

type VariableEstimationMethod string

const (
	ExpectedValue    VariableEstimationMethod = "expected_value"
	MostLikelyAmount VariableEstimationMethod = "most_likely_amount"
)

type ConsiderationOutcome struct {
	Amount      financialtypes.Money `json:"amount"`
	Probability decimal.Decimal      `json:"probability"`
	Description string               `json:"description,omitempty"`
}

type VariableConsiderationEstimate struct {
	Method           VariableEstimationMethod `json:"method"`
	Outcomes         []ConsiderationOutcome   `json:"outcomes"`
	EstimatedAmount  financialtypes.Money     `json:"estimated_amount"`
	IsConstrained    bool                     `json:"is_constrained"`
	ConstrainedLimit financialtypes.Money     `json:"constrained_limit,omitempty"`
	ConstraintReason string                   `json:"constraint_reason,omitempty"`
	FinalAmount      financialtypes.Money     `json:"final_amount"`
	AsOfDate         time.Time                `json:"as_of_date"`
}

// EstimateVariableConsideration calculates variable consideration in accordance with IFRS 15.53
// using either Expected Value or Most Likely Amount, and applies the constraint in IFRS 15.56-58.
func EstimateVariableConsideration(
	method VariableEstimationMethod,
	outcomes []ConsiderationOutcome,
	isConstrained bool,
	constrainedLimit financialtypes.Money,
	constraintReason string,
	asOfDate time.Time,
) (VariableConsiderationEstimate, error) {
	if len(outcomes) == 0 {
		return VariableConsiderationEstimate{}, fmt.Errorf("%w: at least one outcome is required", ErrInvalidVariableConsideration)
	}
	if method != ExpectedValue && method != MostLikelyAmount {
		return VariableConsiderationEstimate{}, fmt.Errorf("%w: unsupported estimation method %q", ErrInvalidVariableConsideration, method)
	}
	currency := outcomes[0].Amount.Currency()
	if currency == "" {
		return VariableConsiderationEstimate{}, fmt.Errorf("%w: outcome currency is empty", ErrInvalidVariableConsideration)
	}

	probSum := decimal.Zero
	for i, outcome := range outcomes {
		if outcome.Amount.Currency() != currency {
			return VariableConsiderationEstimate{}, fmt.Errorf("%w: mixed currencies in outcomes", ErrInvalidVariableConsideration)
		}
		if outcome.Amount.IsNegative() {
			return VariableConsiderationEstimate{}, fmt.Errorf("%w: outcome %d has negative amount", ErrInvalidVariableConsideration, i)
		}
		if outcome.Probability.IsNegative() || outcome.Probability.GreaterThan(decimal.NewFromInt(1)) {
			return VariableConsiderationEstimate{}, fmt.Errorf("%w: probability must be between 0 and 1", ErrInvalidVariableConsideration)
		}
		probSum = probSum.Add(outcome.Probability)
	}

	// Probabilities must sum to 1.0 (with a small numerical tolerance for rounding: 0.0001)
	diff := probSum.Sub(decimal.NewFromInt(1)).Abs()
	if diff.GreaterThan(decimal.RequireFromString("0.0001")) {
		return VariableConsiderationEstimate{}, fmt.Errorf("%w: sum is %s", ErrInvalidProbabilitySum, probSum)
	}

	var estimated financialtypes.Money
	switch method {
	case ExpectedValue:
		weightedTotal := decimal.Zero
		for _, outcome := range outcomes {
			weighted := outcome.Amount.Amount().Mul(outcome.Probability)
			weightedTotal = weightedTotal.Add(weighted)
		}
		minorUnits := financialtypes.CurrencyMinorUnit(currency)
		roundedAmount := weightedTotal.RoundBank(minorUnits)
		var err error
		estimated, err = financialtypes.NewMoney(roundedAmount, currency)
		if err != nil {
			return VariableConsiderationEstimate{}, fmt.Errorf("%w: %v", ErrInvalidVariableConsideration, err)
		}

	case MostLikelyAmount:
		maxProb := decimal.NewFromInt(-1)
		selectedIdx := 0
		for i, outcome := range outcomes {
			if outcome.Probability.GreaterThan(maxProb) {
				maxProb = outcome.Probability
				selectedIdx = i
			}
		}
		estimated = outcomes[selectedIdx].Amount
	}

	finalAmount := estimated
	if isConstrained {
		if constrainedLimit.Currency() != currency {
			return VariableConsiderationEstimate{}, fmt.Errorf("%w: constrained limit currency mismatch", ErrInvalidVariableConsideration)
		}
		if constrainedLimit.IsNegative() {
			return VariableConsiderationEstimate{}, fmt.Errorf("%w: constrained limit cannot be negative", ErrInvalidVariableConsideration)
		}
		if constrainedLimit.GreaterThan(estimated) {
			return VariableConsiderationEstimate{}, fmt.Errorf("%w: limit %s exceeds estimated %s", ErrVariableConstraintExceeded, constrainedLimit.Amount(), estimated.Amount())
		}
		finalAmount = constrainedLimit
	}

	if asOfDate.IsZero() {
		asOfDate = time.Now().UTC()
	}

	return VariableConsiderationEstimate{
		Method:           method,
		Outcomes:         append([]ConsiderationOutcome(nil), outcomes...),
		EstimatedAmount:  estimated,
		IsConstrained:    isConstrained,
		ConstrainedLimit: constrainedLimit,
		ConstraintReason: constraintReason,
		FinalAmount:      finalAmount,
		AsOfDate:         asOfDate,
	}, nil
}

// ReassessVariableConsideration updates the transaction price and reallocates across obligations
// when new facts and circumstances warrant an update in variable consideration under IFRS 15.58.
func ReassessVariableConsideration(contract Contract, fixedPrice financialtypes.Money, newEstimate VariableConsiderationEstimate) (Contract, error) {
	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	currency := contract.Currency
	if fixedPrice.Currency() != currency || fixedPrice.IsNegative() {
		return Contract{}, fmt.Errorf("%w: invalid fixed price component", ErrInvalidAmount)
	}
	if newEstimate.FinalAmount.Currency() != currency || newEstimate.FinalAmount.IsNegative() {
		return Contract{}, fmt.Errorf("%w: invalid variable consideration estimate", ErrInvalidVariableConsideration)
	}

	updatedPrice, err := fixedPrice.Add(newEstimate.FinalAmount)
	if err != nil {
		return Contract{}, err
	}
	if !updatedPrice.Amount().IsPositive() {
		return Contract{}, ErrInvalidAmount
	}
	if contract.RecognizedAmount.GreaterThan(updatedPrice) {
		return Contract{}, fmt.Errorf("%w: updated transaction price (%s) is less than already recognized revenue (%s)",
			ErrRecognitionExceeded, updatedPrice.Amount(), contract.RecognizedAmount.Amount())
	}

	// Reallocate updated transaction price across obligations based on original relative standalone prices
	newAllocations, err := AllocateTransactionPrice(updatedPrice, contract.Obligations)
	if err != nil {
		return Contract{}, err
	}

	contract.TransactionPrice = updatedPrice
	contract.AllocatedPrices = newAllocations
	contract.Status = ContractStatusModified
	return cloneContract(contract), nil
}

