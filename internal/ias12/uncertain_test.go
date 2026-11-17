package ias12

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func uncertainTreatment(t *testing.T) UncertainTaxTreatment {
	t.Helper()
	return UncertainTaxTreatment{
		ID:                                "UT-2026-01",
		Description:                       "Deductibility of a research expense",
		UnitOfAccount:                     IndividualTaxUnit,
		UnitOfAccountReference:            "tax-return-line-42",
		UnitOfAccountRationale:            "The item is assessed separately because its facts and resolution are distinct.",
		ExaminationAssumptionAcknowledged: true,
		AssessmentDate:                    testPeriod(Annual).AsOf,
		AssessmentReference:               "tax-position-memo-v1",
		ReportedTaxAmount:                 testMoney(t, "100", "USD"),
		Method:                            MostLikelyAmount,
		MethodRationale:                   "The outcome is binary and one outcome best predicts resolution.",
		FactsAndCircumstancesReference:    "case-facts-v1",
		LimitationDate:                    time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC),
	}
}

func TestMeasureUncertainTaxTreatmentProbableAcceptanceUsesFiledAmount(t *testing.T) {
	treatment := uncertainTreatment(t)
	treatment.AcceptanceProbable = true
	measurement, err := MeasureUncertainTaxTreatment(treatment, testPeriod(Annual).AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if !measurement.RecognizedTaxAmount.Amount().Equal(decimal.NewFromInt(100)) ||
		!measurement.UncertaintyAdjustment.Amount().IsZero() ||
		!measurement.LiabilityIncrease.Amount().IsZero() ||
		!measurement.TaxAssetIncrease.Amount().IsZero() {
		t.Fatalf("probable acceptance should retain the filed amount without adjustment: %+v", measurement)
	}
	if measurement.Method != "" {
		t.Fatalf("method = %q, want no uncertainty method when acceptance is probable", measurement.Method)
	}
}

func TestMeasureUncertainTaxTreatmentMostLikelyAmountBinaryScenario(t *testing.T) {
	treatment := uncertainTreatment(t)
	treatment.Outcomes = []TaxOutcome{
		{ID: "accepted", TaxAmount: testMoney(t, "100", "USD"), Probability: decimal.RequireFromString("0.45"), SourceReference: "position-analysis"},
		{ID: "rejected", TaxAmount: testMoney(t, "180", "USD"), Probability: decimal.RequireFromString("0.55"), SourceReference: "position-analysis"},
	}
	measurement, err := MeasureUncertainTaxTreatment(treatment, testPeriod(Annual).AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.AcceptanceProbable || measurement.Method != MostLikelyAmount ||
		measurement.SelectedOutcomeID != "rejected" ||
		measurement.RecognizedTaxAmount.Amount().String() != "180" ||
		measurement.UncertaintyAdjustment.Amount().String() != "80" ||
		measurement.LiabilityIncrease.Amount().String() != "80" ||
		!measurement.TaxAssetIncrease.Amount().IsZero() {
		t.Fatalf("unexpected most-likely-amount measurement: %+v", measurement)
	}
}

func TestMeasureUncertainTaxTreatmentExpectedValueMultipleOutcomes(t *testing.T) {
	treatment := uncertainTreatment(t)
	treatment.UnitOfAccount = GroupTaxUnit
	treatment.UnitOfAccountReference = "tax-return-lines-42-44"
	treatment.UnitOfAccountRationale = "The positions are assessed together because a combined resolution is more predictive."
	treatment.Method = ExpectedValue
	treatment.MethodRationale = "The outcomes span a broad range and expected value best predicts resolution."
	treatment.Outcomes = []TaxOutcome{
		{ID: "low", TaxAmount: testMoney(t, "100", "USD"), Probability: decimal.RequireFromString("0.2"), SourceReference: "scenario-model"},
		{ID: "middle", TaxAmount: testMoney(t, "200", "USD"), Probability: decimal.RequireFromString("0.5"), SourceReference: "scenario-model"},
		{ID: "high", TaxAmount: testMoney(t, "400", "USD"), Probability: decimal.RequireFromString("0.3"), SourceReference: "scenario-model"},
	}
	measurement, err := MeasureUncertainTaxTreatment(treatment, testPeriod(Annual).AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.Method != ExpectedValue || measurement.SelectedOutcomeID != "" ||
		measurement.RecognizedTaxAmount.Amount().String() != "240" ||
		measurement.UncertaintyAdjustment.Amount().String() != "140" ||
		measurement.LiabilityIncrease.Amount().String() != "140" {
		t.Fatalf("unexpected expected-value measurement: %+v", measurement)
	}
}

func TestMeasureUncertainTaxTreatmentReducesReportedLiabilityWithoutInventingAsset(t *testing.T) {
	treatment := uncertainTreatment(t)
	treatment.Outcomes = []TaxOutcome{
		{ID: "partial-acceptance", TaxAmount: testMoney(t, "60", "USD"), Probability: decimal.RequireFromString("0.7"), SourceReference: "tax-analysis"},
		{ID: "rejection", TaxAmount: testMoney(t, "100", "USD"), Probability: decimal.RequireFromString("0.3"), SourceReference: "tax-analysis"},
	}
	measurement, err := MeasureUncertainTaxTreatment(treatment, testPeriod(Annual).AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.UncertaintyAdjustment.Amount().String() != "-40" ||
		!measurement.LiabilityIncrease.Amount().IsZero() ||
		measurement.LiabilityDecrease.Amount().String() != "40" ||
		!measurement.TaxAssetIncrease.Amount().IsZero() {
		t.Fatalf("unexpected negative adjustment: %+v", measurement)
	}
}

func TestMeasureUncertainTaxTreatmentCalculatesTaxAssetIncrease(t *testing.T) {
	treatment := uncertainTreatment(t)
	treatment.ReportedTaxAmount = testMoney(t, "-100", "USD")
	treatment.Outcomes = []TaxOutcome{
		{ID: "higher-refund", TaxAmount: testMoney(t, "-180", "USD"), Probability: decimal.RequireFromString("0.6"), SourceReference: "refund-analysis"},
		{ID: "same-refund", TaxAmount: testMoney(t, "-100", "USD"), Probability: decimal.RequireFromString("0.4"), SourceReference: "refund-analysis"},
	}
	measurement, err := MeasureUncertainTaxTreatment(treatment, testPeriod(Annual).AsOf)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.RecognizedTaxAmount.Amount().String() != "-180" ||
		measurement.UncertaintyAdjustment.Amount().String() != "-80" ||
		measurement.TaxAssetIncrease.Amount().String() != "80" ||
		!measurement.TaxAssetDecrease.Amount().IsZero() ||
		!measurement.LiabilityIncrease.Amount().IsZero() {
		t.Fatalf("unexpected tax asset measurement: %+v", measurement)
	}
}

func TestUncertainTaxTreatmentReassessmentOnFactsAndLimitation(t *testing.T) {
	previous := uncertainTreatment(t)
	previous.Outcomes = []TaxOutcome{
		{ID: "accepted", TaxAmount: testMoney(t, "100", "USD"), Probability: decimal.RequireFromString("0.45"), SourceReference: "position-analysis-v1"},
		{ID: "rejected", TaxAmount: testMoney(t, "180", "USD"), Probability: decimal.RequireFromString("0.55"), SourceReference: "position-analysis-v1"},
	}
	asOf := previous.AssessmentDate.AddDate(0, 0, 1)

	updatedForFacts := previous
	updatedForFacts.AssessmentDate = asOf
	updatedForFacts.FactsAndCircumstancesReference = "case-facts-v2"
	required, err := ReassessmentRequired(previous, updatedForFacts, asOf)
	if err != nil || !required {
		t.Fatalf("changed facts should trigger reassessment: required=%v, err=%v", required, err)
	}
	required, err = ReassessmentRequired(previous, previous, previous.LimitationDate)
	if err != nil || !required {
		t.Fatalf("limitation date should trigger reassessment: required=%v, err=%v", required, err)
	}

	updated := previous
	updated.AssessmentDate = asOf
	updated.AssessmentReference = "tax-position-memo-v2"
	updated.FactsAndCircumstancesReference = "case-facts-v2"
	updated.Outcomes = []TaxOutcome{
		{ID: "accepted", TaxAmount: testMoney(t, "100", "USD"), Probability: decimal.RequireFromString("0.6"), SourceReference: "position-analysis-v2"},
		{ID: "rejected", TaxAmount: testMoney(t, "180", "USD"), Probability: decimal.RequireFromString("0.4"), SourceReference: "position-analysis-v2"},
	}
	reassessment, err := ReassessUncertainTaxTreatment(previous, updated, asOf, "New authority guidance changed the acceptance assessment.")
	if err != nil {
		t.Fatal(err)
	}
	if reassessment.PreviousMeasurement.RecognizedTaxAmount.Amount().String() != "180" ||
		reassessment.UpdatedMeasurement.RecognizedTaxAmount.Amount().String() != "100" ||
		reassessment.UpdatedMeasurement.UncertaintyAdjustment.Amount().String() != "0" ||
		reassessment.UpdatedTreatment.ReassessmentReason == "" ||
		reassessment.PreviousTreatment.FactsAndCircumstancesReference == reassessment.UpdatedTreatment.FactsAndCircumstancesReference {
		t.Fatalf("reassessment did not preserve both assessments: %+v", reassessment)
	}

	updated.LimitationDate = updated.LimitationDate.AddDate(0, 0, 1)
	required, err = ReassessmentRequired(previous, updated, asOf)
	if err != nil || !required {
		t.Fatalf("changed limitation date should trigger reassessment: required=%v, err=%v", required, err)
	}
}

func TestUncertainTaxTreatmentRejectsMissingEvidenceAndInvalidOutcomes(t *testing.T) {
	base := uncertainTreatment(t)
	base.Outcomes = []TaxOutcome{
		{ID: "first", TaxAmount: testMoney(t, "100", "USD"), Probability: decimal.RequireFromString("0.5"), SourceReference: "scenarios"},
		{ID: "second", TaxAmount: testMoney(t, "200", "USD"), Probability: decimal.RequireFromString("0.5"), SourceReference: "scenarios"},
	}
	tests := []struct {
		name   string
		mutate func(*UncertainTaxTreatment)
		want   error
	}{
		{
			name: "missing full-knowledge assumption",
			mutate: func(value *UncertainTaxTreatment) {
				value.ExaminationAssumptionAcknowledged = false
			},
			want: ErrInvalidUncertainTaxTreatment,
		},
		{
			name: "probabilities do not sum to one",
			mutate: func(value *UncertainTaxTreatment) {
				value.Outcomes[0].Probability = decimal.RequireFromString("0.4")
			},
			want: ErrInvalidTaxOutcomes,
		},
		{
			name: "ambiguous most likely outcome",
			mutate: func(value *UncertainTaxTreatment) {
				value.Outcomes[0].Probability = decimal.RequireFromString("0.5")
				value.Outcomes[1].Probability = decimal.RequireFromString("0.5")
			},
			want: ErrInvalidTaxOutcomes,
		},
		{
			name: "mixed outcome currencies",
			mutate: func(value *UncertainTaxTreatment) {
				value.Outcomes[1].TaxAmount = testMoney(t, "200", "EUR")
			},
			want: ErrInvalidTaxOutcomes,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			treatment := base
			treatment.Outcomes = append([]TaxOutcome(nil), base.Outcomes...)
			test.mutate(&treatment)
			if _, err := MeasureUncertainTaxTreatment(treatment, testPeriod(Annual).AsOf); !errors.Is(err, test.want) {
				t.Fatalf("MeasureUncertainTaxTreatment error = %v, want %v", err, test.want)
			}
		})
	}

	probable := base
	probable.AcceptanceProbable = true
	probable.Outcomes = nil
	if _, err := MeasureUncertainTaxTreatment(probable, probable.AssessmentDate.AddDate(0, 0, -1)); !errors.Is(err, ErrInvalidUncertainTaxTreatment) {
		t.Fatalf("future assessment error = %v, want %v", err, ErrInvalidUncertainTaxTreatment)
	}
}
