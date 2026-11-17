package ias12

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidUncertainTaxTreatment = errors.New("invalid uncertain tax treatment")
	ErrInvalidTaxOutcomes           = errors.New("invalid uncertain tax outcomes")
	ErrReassessmentNotRequired      = errors.New("uncertain tax treatment has no reassessment trigger")
)

type TaxUnitOfAccount string

const (
	IndividualTaxUnit TaxUnitOfAccount = "individual"
	GroupTaxUnit      TaxUnitOfAccount = "group"
)

type UncertaintyMeasurementMethod string

const (
	MostLikelyAmount UncertaintyMeasurementMethod = "most_likely_amount"
	ExpectedValue    UncertaintyMeasurementMethod = "expected_value"
)

type TaxOutcome struct {
	ID string
	// TaxAmount is signed: positive is a liability and negative is a tax asset.
	TaxAmount       financialtypes.Money
	Probability     decimal.Decimal
	SourceReference string
}

// UncertainTaxTreatment records the host's tax analysis and the evidence
// needed to apply IFRIC 23. It is not a tax-law or legal-opinion engine.
type UncertainTaxTreatment struct {
	ID                                string
	Description                       string
	UnitOfAccount                     TaxUnitOfAccount
	UnitOfAccountReference            string
	UnitOfAccountRationale            string
	ExaminationAssumptionAcknowledged bool
	AcceptanceProbable                bool
	AssessmentDate                    time.Time
	AssessmentReference               string
	ReportedTaxAmount                 financialtypes.Money
	Method                            UncertaintyMeasurementMethod
	MethodRationale                   string
	Outcomes                          []TaxOutcome
	FactsAndCircumstancesReference    string
	LimitationDate                    time.Time
	ReassessmentReason                string
}

type UncertainTaxMeasurement struct {
	TreatmentID                    string
	Description                    string
	UnitOfAccount                  TaxUnitOfAccount
	UnitOfAccountReference         string
	AcceptanceProbable             bool
	Method                         UncertaintyMeasurementMethod
	ReportedTaxAmount              financialtypes.Money
	RecognizedTaxAmount            financialtypes.Money
	UncertaintyAdjustment          financialtypes.Money
	LiabilityIncrease              financialtypes.Money
	LiabilityDecrease              financialtypes.Money
	TaxAssetIncrease               financialtypes.Money
	TaxAssetDecrease               financialtypes.Money
	SelectedOutcomeID              string
	AssessmentDate                 time.Time
	AssessmentReference            string
	FactsAndCircumstancesReference string
	LimitationDate                 time.Time
	ReassessmentDue                bool
}

type TaxTreatmentReassessment struct {
	PreviousTreatment   UncertainTaxTreatment
	UpdatedTreatment    UncertainTaxTreatment
	PreviousMeasurement UncertainTaxMeasurement
	UpdatedMeasurement  UncertainTaxMeasurement
	Reason              string
}

func MeasureUncertainTaxTreatment(
	treatment UncertainTaxTreatment,
	asOf time.Time,
) (UncertainTaxMeasurement, error) {
	if err := validateUncertainTaxTreatment(treatment, asOf); err != nil {
		return UncertainTaxMeasurement{}, err
	}

	recognized := treatment.ReportedTaxAmount
	selectedOutcome := ""
	method := UncertaintyMeasurementMethod("")
	if !treatment.AcceptanceProbable {
		method = treatment.Method
		amount, outcomeID, err := measureTaxOutcomes(treatment)
		if err != nil {
			return UncertainTaxMeasurement{}, err
		}
		recognized, err = financialtypes.NewMoney(amount, treatment.ReportedTaxAmount.Currency())
		if err != nil {
			return UncertainTaxMeasurement{}, err
		}
		selectedOutcome = outcomeID
	}

	adjustmentAmount := recognized.Amount().Sub(treatment.ReportedTaxAmount.Amount())
	adjustment, err := financialtypes.NewMoney(adjustmentAmount, recognized.Currency())
	if err != nil {
		return UncertainTaxMeasurement{}, err
	}
	reportedLiability := decimal.Max(treatment.ReportedTaxAmount.Amount(), decimal.Zero)
	recognizedLiability := decimal.Max(recognized.Amount(), decimal.Zero)
	reportedAsset := decimal.Max(treatment.ReportedTaxAmount.Amount().Neg(), decimal.Zero)
	recognizedAsset := decimal.Max(recognized.Amount().Neg(), decimal.Zero)
	liabilityIncrease, err := financialtypes.NewMoney(
		decimal.Max(recognizedLiability.Sub(reportedLiability), decimal.Zero), recognized.Currency(),
	)
	if err != nil {
		return UncertainTaxMeasurement{}, err
	}
	liabilityDecrease, err := financialtypes.NewMoney(
		decimal.Max(reportedLiability.Sub(recognizedLiability), decimal.Zero), recognized.Currency(),
	)
	if err != nil {
		return UncertainTaxMeasurement{}, err
	}
	taxAssetIncrease, err := financialtypes.NewMoney(
		decimal.Max(recognizedAsset.Sub(reportedAsset), decimal.Zero), recognized.Currency(),
	)
	if err != nil {
		return UncertainTaxMeasurement{}, err
	}
	taxAssetDecrease, err := financialtypes.NewMoney(
		decimal.Max(reportedAsset.Sub(recognizedAsset), decimal.Zero), recognized.Currency(),
	)
	if err != nil {
		return UncertainTaxMeasurement{}, err
	}

	return UncertainTaxMeasurement{
		TreatmentID:                    treatment.ID,
		Description:                    treatment.Description,
		UnitOfAccount:                  treatment.UnitOfAccount,
		UnitOfAccountReference:         treatment.UnitOfAccountReference,
		AcceptanceProbable:             treatment.AcceptanceProbable,
		Method:                         method,
		ReportedTaxAmount:              treatment.ReportedTaxAmount,
		RecognizedTaxAmount:            recognized,
		UncertaintyAdjustment:          adjustment,
		LiabilityIncrease:              liabilityIncrease,
		LiabilityDecrease:              liabilityDecrease,
		TaxAssetIncrease:               taxAssetIncrease,
		TaxAssetDecrease:               taxAssetDecrease,
		SelectedOutcomeID:              selectedOutcome,
		AssessmentDate:                 treatment.AssessmentDate,
		AssessmentReference:            treatment.AssessmentReference,
		FactsAndCircumstancesReference: treatment.FactsAndCircumstancesReference,
		LimitationDate:                 treatment.LimitationDate,
		ReassessmentDue:                !asOf.Before(treatment.LimitationDate),
	}, nil
}

func ReassessmentRequired(
	previous UncertainTaxTreatment,
	updated UncertainTaxTreatment,
	asOf time.Time,
) (bool, error) {
	if strings.TrimSpace(previous.ID) == "" || previous.ID != updated.ID || asOf.IsZero() ||
		previous.AssessmentDate.IsZero() || updated.AssessmentDate.IsZero() ||
		previous.LimitationDate.IsZero() || updated.LimitationDate.IsZero() ||
		updated.AssessmentDate.Before(previous.AssessmentDate) {
		return false, ErrInvalidUncertainTaxTreatment
	}
	if asOf.Before(previous.AssessmentDate) || updated.AssessmentDate.After(asOf) {
		return false, ErrInvalidUncertainTaxTreatment
	}
	previousMaterial := previous
	updatedMaterial := updated
	previousMaterial.AssessmentDate = time.Time{}
	updatedMaterial.AssessmentDate = time.Time{}
	previousMaterial.AssessmentReference = ""
	updatedMaterial.AssessmentReference = ""
	previousMaterial.ReassessmentReason = ""
	updatedMaterial.ReassessmentReason = ""
	if sameDate(previousMaterial.LimitationDate, updatedMaterial.LimitationDate) {
		updatedMaterial.LimitationDate = previousMaterial.LimitationDate
	}
	return asOf.Equal(previous.LimitationDate) || asOf.After(previous.LimitationDate) ||
		!reflect.DeepEqual(previousMaterial, updatedMaterial), nil
}

// ReassessUncertainTaxTreatment produces a new, separately auditable
// measurement and never mutates the previous assessment.
func ReassessUncertainTaxTreatment(
	previous UncertainTaxTreatment,
	updated UncertainTaxTreatment,
	asOf time.Time,
	reason string,
) (TaxTreatmentReassessment, error) {
	reason = strings.TrimSpace(reason)
	if previous.ID != updated.ID || reason == "" ||
		updated.AssessmentDate.Before(previous.AssessmentDate) {
		return TaxTreatmentReassessment{}, ErrInvalidUncertainTaxTreatment
	}
	required, err := ReassessmentRequired(
		previous, updated, asOf,
	)
	if err != nil {
		return TaxTreatmentReassessment{}, err
	}

	if !required {
		return TaxTreatmentReassessment{}, ErrReassessmentNotRequired
	}

	previousMeasurement, err := MeasureUncertainTaxTreatment(previous, previous.AssessmentDate)
	if err != nil {
		return TaxTreatmentReassessment{}, fmt.Errorf("measure previous tax assessment: %w", err)
	}
	updated.ReassessmentReason = reason
	updatedMeasurement, err := MeasureUncertainTaxTreatment(updated, asOf)
	if err != nil {
		return TaxTreatmentReassessment{}, fmt.Errorf("measure reassessed tax treatment: %w", err)
	}
	return TaxTreatmentReassessment{
		PreviousTreatment: previous, UpdatedTreatment: updated,
		PreviousMeasurement: previousMeasurement, UpdatedMeasurement: updatedMeasurement,
		Reason: reason,
	}, nil
}

func validateUncertainTaxTreatment(treatment UncertainTaxTreatment, asOf time.Time) error {
	if strings.TrimSpace(treatment.ID) == "" || strings.TrimSpace(treatment.Description) == "" ||
		(treatment.UnitOfAccount != IndividualTaxUnit && treatment.UnitOfAccount != GroupTaxUnit) ||
		strings.TrimSpace(treatment.UnitOfAccountReference) == "" ||
		strings.TrimSpace(treatment.UnitOfAccountRationale) == "" ||
		!treatment.ExaminationAssumptionAcknowledged ||
		treatment.AssessmentDate.IsZero() || treatment.AssessmentDate.After(asOf) ||
		strings.TrimSpace(treatment.AssessmentReference) == "" ||
		strings.TrimSpace(treatment.FactsAndCircumstancesReference) == "" ||
		treatment.LimitationDate.IsZero() || validateSignedMoney(treatment.ReportedTaxAmount) != nil {
		return ErrInvalidUncertainTaxTreatment
	}
	if treatment.AcceptanceProbable {
		return nil
	}
	if (treatment.Method != MostLikelyAmount && treatment.Method != ExpectedValue) ||
		strings.TrimSpace(treatment.MethodRationale) == "" {
		return ErrInvalidUncertainTaxTreatment
	}
	return validateTaxOutcomes(treatment.Outcomes, treatment.ReportedTaxAmount.Currency())
}

func validateUncertainTaxMeasurement(measurement UncertainTaxMeasurement, currency string) error {
	if strings.TrimSpace(measurement.TreatmentID) == "" ||
		strings.TrimSpace(measurement.Description) == "" ||
		(measurement.UnitOfAccount != IndividualTaxUnit && measurement.UnitOfAccount != GroupTaxUnit) ||
		strings.TrimSpace(measurement.UnitOfAccountReference) == "" ||
		strings.TrimSpace(measurement.AssessmentReference) == "" ||
		strings.TrimSpace(measurement.FactsAndCircumstancesReference) == "" ||
		measurement.AssessmentDate.IsZero() || measurement.LimitationDate.IsZero() {
		return ErrInvalidUncertainTaxTreatment
	}
	amounts := []financialtypes.Money{
		measurement.ReportedTaxAmount, measurement.RecognizedTaxAmount,
		measurement.UncertaintyAdjustment, measurement.LiabilityIncrease,
		measurement.LiabilityDecrease, measurement.TaxAssetIncrease,
		measurement.TaxAssetDecrease,
	}
	for _, amount := range amounts {
		if amount.Currency() != currency {
			return ErrCurrencyMismatch
		}
	}
	adjustment := measurement.RecognizedTaxAmount.Amount().Sub(measurement.ReportedTaxAmount.Amount())
	if !adjustment.Equal(measurement.UncertaintyAdjustment.Amount()) {
		return ErrInvalidUncertainTaxTreatment
	}
	reportedLiability := decimal.Max(measurement.ReportedTaxAmount.Amount(), decimal.Zero)
	recognizedLiability := decimal.Max(measurement.RecognizedTaxAmount.Amount(), decimal.Zero)
	reportedAsset := decimal.Max(measurement.ReportedTaxAmount.Amount().Neg(), decimal.Zero)
	recognizedAsset := decimal.Max(measurement.RecognizedTaxAmount.Amount().Neg(), decimal.Zero)
	if !measurement.LiabilityIncrease.Amount().Equal(
		decimal.Max(recognizedLiability.Sub(reportedLiability), decimal.Zero),
	) || !measurement.LiabilityDecrease.Amount().Equal(
		decimal.Max(reportedLiability.Sub(recognizedLiability), decimal.Zero),
	) || !measurement.TaxAssetIncrease.Amount().Equal(
		decimal.Max(recognizedAsset.Sub(reportedAsset), decimal.Zero),
	) || !measurement.TaxAssetDecrease.Amount().Equal(
		decimal.Max(reportedAsset.Sub(recognizedAsset), decimal.Zero),
	) {
		return ErrInvalidUncertainTaxTreatment
	}
	if measurement.AcceptanceProbable {
		if !measurement.RecognizedTaxAmount.Amount().Equal(measurement.ReportedTaxAmount.Amount()) ||
			measurement.Method != "" {
			return ErrInvalidUncertainTaxTreatment
		}
	} else if measurement.Method != MostLikelyAmount && measurement.Method != ExpectedValue {
		return ErrInvalidUncertainTaxTreatment
	}
	return nil
}

func validateTaxOutcomes(outcomes []TaxOutcome, currency string) error {
	if len(outcomes) == 0 {
		return ErrInvalidTaxOutcomes
	}
	seen := make(map[string]struct{}, len(outcomes))
	totalProbability := decimal.Zero
	for _, outcome := range outcomes {
		id := strings.TrimSpace(outcome.ID)
		if id == "" || strings.TrimSpace(outcome.SourceReference) == "" ||
			validateSignedMoney(outcome.TaxAmount) != nil ||
			outcome.TaxAmount.Currency() != currency ||
			outcome.Probability.IsNegative() || outcome.Probability.GreaterThan(decimal.NewFromInt(1)) {
			return ErrInvalidTaxOutcomes
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: duplicate outcome %q", ErrInvalidTaxOutcomes, id)
		}
		seen[id] = struct{}{}
		totalProbability = totalProbability.Add(outcome.Probability)
	}
	if !totalProbability.Equal(decimal.NewFromInt(1)) {
		return fmt.Errorf("%w: probabilities sum to %s, want 1", ErrInvalidTaxOutcomes, totalProbability)
	}
	return nil
}

func measureTaxOutcomes(treatment UncertainTaxTreatment) (decimal.Decimal, string, error) {
	if err := validateTaxOutcomes(treatment.Outcomes, treatment.ReportedTaxAmount.Currency()); err != nil {
		return decimal.Zero, "", err
	}
	if treatment.Method == ExpectedValue {
		expected := decimal.Zero
		for _, outcome := range treatment.Outcomes {
			expected = expected.Add(outcome.TaxAmount.Amount().Mul(outcome.Probability))
		}
		return expected, "", nil
	}

	var selected TaxOutcome
	found := false
	tied := false
	for _, outcome := range treatment.Outcomes {
		if !found || outcome.Probability.GreaterThan(selected.Probability) {
			selected, found, tied = outcome, true, false
		} else if outcome.Probability.Equal(selected.Probability) {
			tied = true
		}
	}
	if !found || tied {
		return decimal.Zero, "", fmt.Errorf("%w: most likely outcome is not unique", ErrInvalidTaxOutcomes)
	}
	return selected.TaxAmount.Amount(), selected.ID, nil
}

func sameDate(left, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return left.IsZero() && right.IsZero()
	}
	left = time.Date(left.UTC().Year(), left.UTC().Month(), left.UTC().Day(), 0, 0, 0, 0, time.UTC)
	right = time.Date(right.UTC().Year(), right.UTC().Month(), right.UTC().Day(), 0, 0, 0, 0, time.UTC)
	return left.Equal(right)
}
