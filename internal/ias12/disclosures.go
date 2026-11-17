package ias12

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	apitypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

var ErrInvalidTaxReconciliationDisclosure = errors.New("invalid IAS 12 tax reconciliation disclosure")

const (
	TaxReconciliationDisclosureCode    = "IAS12-TAX-RATE-RECONCILIATION"
	TaxReconciliationDisclosureVersion = "1.0.0"
	TaxReconciliationStandardReference = "IAS 12"
)

type TaxReconciliationDisclosureInput struct {
	CompanyID        apitypes.CompanyID
	PeriodStart      time.Time
	PeriodEnd        time.Time
	Report           TaxReconciliationReport
	SourceReferences []publicreporting.DisclosureSource
	Narrative        string
}

func BuildTaxReconciliationDisclosure(
	input TaxReconciliationDisclosureInput,
) (publicreporting.StructuredDisclosure, error) {
	currency := strings.ToUpper(strings.TrimSpace(input.Report.AccountingProfitBeforeTax.Currency()))
	if input.CompanyID == (apitypes.CompanyID{}) || input.PeriodStart.IsZero() ||
		input.PeriodEnd.IsZero() || input.PeriodEnd.Before(input.PeriodStart) || currency == "" ||
		input.Report.Period.Validate() != nil ||
		!sameDate(input.Report.Period.AsOf, input.PeriodEnd) {
		return publicreporting.StructuredDisclosure{}, ErrInvalidTaxReconciliationDisclosure
	}
	if err := validateTaxReconciliationReport(input.Report, currency); err != nil {
		return publicreporting.StructuredDisclosure{}, err
	}

	amounts := []struct {
		name           string
		amountCurrency string
	}{
		{"expected tax expense", input.Report.ExpectedTaxExpense.Currency()},
		{"permanent difference tax", input.Report.PermanentDifferencesTax.Currency()},
		{"current tax expense", input.Report.CurrentTaxExpense.Currency()},
		{"deferred tax expense", input.Report.DeferredTaxExpense.Currency()},
		{"effective tax expense", input.Report.EffectiveTaxExpense.Currency()},
	}
	for _, amount := range amounts {
		if amount.amountCurrency != currency {
			return publicreporting.StructuredDisclosure{}, fmt.Errorf(
				"%w: %s currency mismatch", ErrInvalidTaxReconciliationDisclosure, amount.name,
			)
		}
	}

	permanentDifferences := make([]map[string]string, 0, len(input.Report.PermanentDifferences))
	for _, difference := range input.Report.PermanentDifferences {
		if strings.TrimSpace(difference.Description) == "" ||
			difference.Amount.Currency() != currency || difference.TaxEffect.Currency() != currency {
			return publicreporting.StructuredDisclosure{}, ErrInvalidTaxReconciliationDisclosure
		}
		permanentDifferences = append(permanentDifferences, map[string]string{
			"description": strings.TrimSpace(difference.Description),
			"amount":      difference.Amount.Amount().String(),
			"tax_effect":  difference.TaxEffect.Amount().String(),
		})
	}

	uncertainTreatments := make([]map[string]any, 0, len(input.Report.UncertainTaxTreatments))
	uncertainTaxEffect := decimal.Zero
	for _, treatment := range input.Report.UncertainTaxTreatments {
		uncertainTreatments = append(uncertainTreatments, map[string]any{
			"treatment_id":                      treatment.TreatmentID,
			"description":                       treatment.Description,
			"unit_of_account":                   treatment.UnitOfAccount,
			"unit_of_account_reference":         treatment.UnitOfAccountReference,
			"acceptance_probable":               treatment.AcceptanceProbable,
			"measurement_method":                treatment.Method,
			"reported_tax_position":             treatment.ReportedTaxAmount.Amount().String(),
			"recognized_tax_position":           treatment.RecognizedTaxAmount.Amount().String(),
			"uncertainty_adjustment":            treatment.UncertaintyAdjustment.Amount().String(),
			"assessment_reference":              treatment.AssessmentReference,
			"facts_and_circumstances_reference": treatment.FactsAndCircumstancesReference,
		})
		uncertainTaxEffect = uncertainTaxEffect.Add(treatment.UncertaintyAdjustment.Amount())
	}
	expected := input.Report.ExpectedTaxExpense.Amount()
	knownEffects := expected.Add(input.Report.PermanentDifferencesTax.Amount()).
		Add(input.Report.DeferredTaxExpense.Amount()).Add(uncertainTaxEffect)
	unallocated := input.Report.EffectiveTaxExpense.Amount().Sub(knownEffects)
	effectiveRate := ""
	if input.Report.EffectiveTaxRateAvailable {
		effectiveRate = input.Report.EffectiveTaxRate.String()
	}
	data := map[string]any{
		"accounting_profit_before_tax":        input.Report.AccountingProfitBeforeTax.Amount().String(),
		"statutory_tax_rate":                  input.Report.StatutoryTaxRate.String(),
		"expected_tax_expense":                expected.String(),
		"permanent_differences":               permanentDifferences,
		"permanent_differences_tax_effect":    input.Report.PermanentDifferencesTax.Amount().String(),
		"current_tax_expense":                 input.Report.CurrentTaxExpense.Amount().String(),
		"deferred_tax_expense":                input.Report.DeferredTaxExpense.Amount().String(),
		"uncertain_tax_treatments":            uncertainTreatments,
		"uncertain_tax_treatments_adjustment": uncertainTaxEffect.String(),
		"unallocated_reconciling_difference":  unallocated.String(),
		"effective_tax_expense":               input.Report.EffectiveTaxExpense.Amount().String(),
		"effective_tax_rate":                  effectiveRate,
		"effective_tax_rate_available":        input.Report.EffectiveTaxRateAvailable,
	}

	sources := []publicreporting.DisclosureSource{{Kind: "standard-module", Reference: "ias12"}}
	sources = append(sources, input.SourceReferences...)
	if len(input.SourceReferences) == 0 {
		return publicreporting.StructuredDisclosure{}, fmt.Errorf(
			"%w: source references are required", ErrInvalidTaxReconciliationDisclosure,
		)
	}
	for _, source := range sources {
		if strings.TrimSpace(source.Kind) == "" || strings.TrimSpace(source.Reference) == "" {
			return publicreporting.StructuredDisclosure{}, fmt.Errorf(
				"%w: source kind and reference are required", ErrInvalidTaxReconciliationDisclosure,
			)
		}
	}

	return publicreporting.StructuredDisclosure{
		CompanyID:         input.CompanyID,
		Code:              TaxReconciliationDisclosureCode,
		Title:             "Income tax rate reconciliation",
		StandardReference: TaxReconciliationStandardReference,
		Period: publicreporting.DisclosurePeriod{
			StartDate: disclosureDate(input.PeriodStart),
			EndDate:   disclosureDate(input.PeriodEnd),
		},
		Currency:  currency,
		Version:   TaxReconciliationDisclosureVersion,
		Source:    sources,
		Data:      data,
		Narrative: strings.TrimSpace(input.Narrative),
	}, nil
}

func validateTaxReconciliationReport(report TaxReconciliationReport, currency string) error {
	if report.StatutoryTaxRate.IsNegative() || report.StatutoryTaxRate.GreaterThan(decimal.NewFromInt(1)) {
		return ErrInvalidTaxReconciliationDisclosure
	}
	for _, amount := range []struct {
		name     string
		value    decimal.Decimal
		currency string
	}{
		{"accounting profit", report.AccountingProfitBeforeTax.Amount(), report.AccountingProfitBeforeTax.Currency()},
		{"expected tax expense", report.ExpectedTaxExpense.Amount(), report.ExpectedTaxExpense.Currency()},
		{"permanent difference tax", report.PermanentDifferencesTax.Amount(), report.PermanentDifferencesTax.Currency()},
		{"current tax expense", report.CurrentTaxExpense.Amount(), report.CurrentTaxExpense.Currency()},
		{"deferred tax expense", report.DeferredTaxExpense.Amount(), report.DeferredTaxExpense.Currency()},
		{"temporary differences change", report.TemporaryDifferencesChange.Amount(), report.TemporaryDifferencesChange.Currency()},
		{"effective tax expense", report.EffectiveTaxExpense.Amount(), report.EffectiveTaxExpense.Currency()},
	} {
		if amount.currency != currency {
			return fmt.Errorf("%w: %s currency mismatch", ErrInvalidTaxReconciliationDisclosure, amount.name)
		}
	}

	if !report.ExpectedTaxExpense.Amount().Equal(
		report.AccountingProfitBeforeTax.Amount().Mul(report.StatutoryTaxRate),
	) || !report.TemporaryDifferencesChange.Amount().Equal(report.DeferredTaxExpense.Amount()) ||
		!report.EffectiveTaxExpense.Amount().Equal(
			report.CurrentTaxExpense.Amount().Add(report.DeferredTaxExpense.Amount()),
		) {
		return ErrInvalidTaxReconciliationDisclosure
	}

	permanentTax := decimal.Zero
	for _, difference := range report.PermanentDifferences {
		if strings.TrimSpace(difference.Description) == "" ||
			difference.Amount.Currency() != currency || difference.TaxEffect.Currency() != currency ||
			!difference.TaxEffect.Amount().Equal(difference.Amount.Amount().Mul(report.StatutoryTaxRate)) {
			return ErrInvalidTaxReconciliationDisclosure
		}
		permanentTax = permanentTax.Add(difference.TaxEffect.Amount())
	}
	if !permanentTax.Equal(report.PermanentDifferencesTax.Amount()) {
		return ErrInvalidTaxReconciliationDisclosure
	}
	seenTreatments := make(map[string]struct{}, len(report.UncertainTaxTreatments))
	for _, treatment := range report.UncertainTaxTreatments {
		if err := validateUncertainTaxMeasurement(treatment, currency); err != nil {
			return fmt.Errorf("%w: invalid uncertain tax treatment: %v", ErrInvalidTaxReconciliationDisclosure, err)
		}
		if _, exists := seenTreatments[treatment.TreatmentID]; exists {
			return fmt.Errorf("%w: duplicate uncertain tax treatment %q", ErrInvalidTaxReconciliationDisclosure, treatment.TreatmentID)
		}
		seenTreatments[treatment.TreatmentID] = struct{}{}
	}

	if report.AccountingProfitBeforeTax.Amount().IsZero() {
		if report.EffectiveTaxRateAvailable || !report.EffectiveTaxRate.IsZero() {
			return ErrInvalidTaxReconciliationDisclosure
		}
	} else if !report.EffectiveTaxRateAvailable ||
		!report.EffectiveTaxRate.Equal(
			report.EffectiveTaxExpense.Amount().Div(report.AccountingProfitBeforeTax.Amount()),
		) {
		return ErrInvalidTaxReconciliationDisclosure
	}
	return nil
}

type TaxReconciliationDisclosureProvider struct {
	disclosures []publicreporting.StructuredDisclosure
}

func NewTaxReconciliationDisclosureProvider(
	inputs ...TaxReconciliationDisclosureInput,
) (*TaxReconciliationDisclosureProvider, error) {
	provider := &TaxReconciliationDisclosureProvider{
		disclosures: make([]publicreporting.StructuredDisclosure, 0, len(inputs)),
	}
	for _, input := range inputs {
		disclosure, err := BuildTaxReconciliationDisclosure(input)
		if err != nil {
			return nil, fmt.Errorf("build IAS 12 disclosure: %w", err)
		}
		provider.disclosures = append(provider.disclosures, disclosure)
	}
	return provider, nil
}

func (p *TaxReconciliationDisclosureProvider) Name() string {
	return "IAS 12 tax reconciliation"
}

func (p *TaxReconciliationDisclosureProvider) GetDisclosures(
	ctx context.Context,
	query publicreporting.DisclosureQuery,
) ([]publicreporting.StructuredDisclosure, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidTaxReconciliationDisclosure)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || query.CompanyID == (apitypes.CompanyID{}) ||
		query.Period.StartDate.IsZero() || query.Period.EndDate.IsZero() ||
		query.Period.EndDate.Before(query.Period.StartDate) {
		return nil, ErrInvalidTaxReconciliationDisclosure
	}

	currency := strings.ToUpper(strings.TrimSpace(query.Currency))
	standard := strings.TrimSpace(query.StandardReference)
	version := strings.TrimSpace(query.Version)
	result := make([]publicreporting.StructuredDisclosure, 0)
	for _, disclosure := range p.disclosures {
		if disclosure.CompanyID != query.CompanyID ||
			!sameDate(disclosure.Period.StartDate, query.Period.StartDate) ||
			!sameDate(disclosure.Period.EndDate, query.Period.EndDate) ||
			(currency != "" && disclosure.Currency != currency) ||
			(standard != "" && disclosure.StandardReference != standard) ||
			(version != "" && disclosure.Version != version) {
			continue
		}
		result = append(result, disclosure)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func disclosureDate(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
