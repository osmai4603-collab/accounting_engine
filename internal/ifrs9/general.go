package ifrs9

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidGeneralExposure = errors.New("invalid general ECL exposure")
	ErrInvalidRiskEstimate    = errors.New("invalid PD or LGD estimate")
	ErrRiskModelRequired      = errors.New("PD and LGD models are required")
)

// GeneralExposure contains the amortized-cost exposure and remaining life used
// by the IFRS 9 general approach.
type GeneralExposure struct {
	ID                    uuid.UUID
	Stage                 Stage
	EAD                   financialtypes.Money
	RemainingLife         time.Duration
	EffectiveInterestRate decimal.Decimal
}

// PDModel returns the scenario-weighted probability of default over horizon.
// Implementations should incorporate reasonable and supportable forward-
// looking information in their estimates.
type PDModel interface {
	ProbabilityOfDefault(GeneralExposure, time.Duration) (decimal.Decimal, error)
}

// LGDModel returns the loss-given-default fraction for an exposure.
type LGDModel interface {
	LossGivenDefault(GeneralExposure) (decimal.Decimal, error)
}

// PDScenario holds a probability-weighted forward-looking PD estimate.
type PDScenario struct {
	Name       string
	Weight     decimal.Decimal
	PD12Month  decimal.Decimal
	PDLifetime decimal.Decimal
}

// ScenarioPDModel computes an unbiased probability-weighted PD across
// reasonable and supportable economic scenarios.
type ScenarioPDModel struct {
	Scenarios []PDScenario
}

func (m ScenarioPDModel) ProbabilityOfDefault(exposure GeneralExposure, _ time.Duration) (decimal.Decimal, error) {
	if len(m.Scenarios) == 0 {
		return decimal.Zero, ErrInvalidRiskEstimate
	}
	weighted, totalWeight := decimal.Zero, decimal.Zero
	for _, scenario := range m.Scenarios {
		if scenario.Name == "" || !fraction(scenario.Weight) || !fraction(scenario.PD12Month) ||
			!fraction(scenario.PDLifetime) {
			return decimal.Zero, ErrInvalidRiskEstimate
		}
		pd := scenario.PDLifetime
		if exposure.Stage == Stage1 {
			pd = scenario.PD12Month
		}
		weighted = weighted.Add(scenario.Weight.Mul(pd))
		totalWeight = totalWeight.Add(scenario.Weight)
	}
	if !totalWeight.Equal(decimal.NewFromInt(1)) {
		return decimal.Zero, ErrInvalidRiskEstimate
	}
	return weighted, nil
}

// EIRDiscount converts a contractual effective rate and discount horizon to a
// present-value factor.
type EIRDiscount func(rate decimal.Decimal, horizon time.Duration) (decimal.Decimal, error)

type GeneralExposureResult struct {
	ExposureID uuid.UUID
	Stage      Stage
	Horizon    time.Duration
	PD         decimal.Decimal
	LGD        decimal.Decimal
	Discount   decimal.Decimal
	ECL        financialtypes.Money
}

type GeneralCalculationResult struct {
	AsOf        time.Time
	RequiredECL financialtypes.Money
	Exposures   []GeneralExposureResult
}

// CalculateGeneralECL applies 12-month ECL to Stage 1 and lifetime ECL to
// Stage 2/3 exposures, discounting the expected loss using each exposure's EIR.
func CalculateGeneralECL(
	asOf time.Time,
	exposures []GeneralExposure,
	pdModel PDModel,
	lgdModel LGDModel,
	eirDiscount EIRDiscount,
) (GeneralCalculationResult, error) {
	if asOf.IsZero() {
		return GeneralCalculationResult{}, ErrInvalidDate
	}
	if pdModel == nil || lgdModel == nil {
		return GeneralCalculationResult{}, ErrRiskModelRequired
	}
	if eirDiscount == nil {
		eirDiscount = DiscountAtEIR
	}
	currency := ""
	total := decimal.Zero
	result := GeneralCalculationResult{AsOf: asOf.UTC(), Exposures: make([]GeneralExposureResult, 0, len(exposures))}
	seen := make(map[uuid.UUID]struct{}, len(exposures))
	for _, exposure := range exposures {
		if exposure.ID == uuid.Nil || exposure.EAD.Currency() == "" || exposure.EAD.IsNegative() ||
			exposure.RemainingLife <= 0 ||
			(exposure.Stage != Stage1 && exposure.Stage != Stage2 && exposure.Stage != Stage3) ||
			!exposure.EffectiveInterestRate.GreaterThan(decimal.NewFromInt(-1)) {
			return GeneralCalculationResult{}, fmt.Errorf("%w: %s", ErrInvalidGeneralExposure, exposure.ID)
		}
		if _, ok := seen[exposure.ID]; ok {
			return GeneralCalculationResult{}, fmt.Errorf("%w: duplicate exposure %s", ErrInvalidGeneralExposure, exposure.ID)
		}
		seen[exposure.ID] = struct{}{}
		if currency == "" {
			currency = exposure.EAD.Currency()
		} else if currency != exposure.EAD.Currency() {
			return GeneralCalculationResult{}, financialtypes.ErrCurrencyMismatch
		}
		horizon := exposure.RemainingLife
		if exposure.Stage == Stage1 && horizon > 365*24*time.Hour {
			horizon = 365 * 24 * time.Hour
		}
		pd, err := pdModel.ProbabilityOfDefault(exposure, horizon)
		if err != nil {
			return GeneralCalculationResult{}, err
		}
		lgd, err := lgdModel.LossGivenDefault(exposure)
		if err != nil {
			return GeneralCalculationResult{}, err
		}
		discount, err := eirDiscount(exposure.EffectiveInterestRate, horizon)
		if err != nil {
			return GeneralCalculationResult{}, err
		}
		if !fraction(pd) || !fraction(lgd) || !discount.IsPositive() {
			return GeneralCalculationResult{}, ErrInvalidRiskEstimate
		}
		amount := exposure.EAD.Amount().Mul(pd).Mul(lgd).Mul(discount)
		ecl, err := financialtypes.NewMoney(amount, currency)
		if err != nil {
			return GeneralCalculationResult{}, err
		}
		ecl = ecl.RoundForPosting()
		total = total.Add(ecl.Amount())
		result.Exposures = append(result.Exposures, GeneralExposureResult{
			ExposureID: exposure.ID, Stage: exposure.Stage, Horizon: horizon,
			PD: pd, LGD: lgd, Discount: discount, ECL: ecl,
		})
	}
	if currency == "" {
		return GeneralCalculationResult{}, ErrInvalidGeneralExposure
	}
	result.RequiredECL, _ = financialtypes.NewMoney(total, currency)
	result.RequiredECL = result.RequiredECL.RoundForPosting()
	return result, nil
}

// DiscountAtEIR discounts at a single expected-loss date represented by the
// supplied horizon.
func DiscountAtEIR(rate decimal.Decimal, horizon time.Duration) (decimal.Decimal, error) {
	if !rate.GreaterThan(decimal.NewFromInt(-1)) || horizon < 0 {
		return decimal.Zero, ErrInvalidRiskEstimate
	}
	years := decimal.NewFromInt(int64(horizon)).Div(decimal.NewFromInt(int64(365 * 24 * time.Hour)))
	base := decimal.NewFromInt(1).Add(rate)
	factor := decimal.NewFromInt(1).Div(base.Pow(years))
	if !factor.IsPositive() {
		return decimal.Zero, ErrInvalidRiskEstimate
	}
	return factor, nil
}

func fraction(value decimal.Decimal) bool {
	return !value.IsNegative() && !value.GreaterThan(decimal.NewFromInt(1))
}
