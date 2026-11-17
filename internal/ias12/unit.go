package ias12

import (
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

// Unit exposes IAS 12 measurement, disclosure reconciliation, and journal
// generation as one optional standards service.
type Unit struct{}

func NewUnit() *Unit { return &Unit{} }

func (u *Unit) Measure(balances []TaxBalance, period MeasurementPeriod) (Measurement, error) {
	if u == nil {
		return Measurement{}, ErrInvalidInput
	}
	return Measure(balances, period)
}

func (u *Unit) MeasureWithProfile(profile TaxProfile, balances []TaxBalance, period MeasurementPeriod) (Measurement, error) {
	if u == nil {
		return Measurement{}, ErrInvalidInput
	}
	return MeasureWithProfile(profile, balances, period)
}

func (u *Unit) MeasureCurrentTax(profile TaxProfile, input CurrentTaxInput) (CurrentTaxMeasurement, error) {
	if u == nil {
		return CurrentTaxMeasurement{}, ErrInvalidInput
	}
	return MeasureCurrentTax(profile, input)
}

func (u *Unit) Reconcile(input TaxReconciliationInput) (TaxReconciliationReport, error) {
	if u == nil {
		return TaxReconciliationReport{}, ErrInvalidInput
	}
	return Reconcile(input)
}

func (u *Unit) MeasureUncertainTaxTreatment(
	treatment UncertainTaxTreatment,
	asOf time.Time,
) (UncertainTaxMeasurement, error) {
	if u == nil {
		return UncertainTaxMeasurement{}, ErrInvalidInput
	}
	return MeasureUncertainTaxTreatment(treatment, asOf)
}

func (u *Unit) ReassessUncertainTaxTreatment(
	previous UncertainTaxTreatment,
	updated UncertainTaxTreatment,
	asOf time.Time,
	reason string,
) (TaxTreatmentReassessment, error) {
	if u == nil {
		return TaxTreatmentReassessment{}, ErrInvalidInput
	}
	return ReassessUncertainTaxTreatment(previous, updated, asOf, reason)
}

func (u *Unit) GenerateConfiguredDeferredTaxEntry(
	measurement Measurement,
	prior DeferredTaxPosition,
	config ConfiguredPosting,
	journalID uuid.UUID,
	createdBy string,
) (posting.JournalEntry, error) {
	if u == nil {
		return posting.JournalEntry{}, ErrInvalidInput
	}
	return GenerateConfiguredDeferredTaxEntry(measurement, prior, config, journalID, createdBy)
}

func (u *Unit) GenerateConfiguredCurrentTaxEntry(
	measurement CurrentTaxMeasurement,
	config ConfiguredPosting,
	journalID uuid.UUID,
	createdBy string,
) (posting.JournalEntry, error) {
	if u == nil {
		return posting.JournalEntry{}, ErrInvalidInput
	}
	return GenerateConfiguredCurrentTaxEntry(measurement, config, journalID, createdBy)
}

func (u *Unit) GenerateDeferredTaxEntry(input DeferredTaxEntryInput) (posting.JournalEntry, error) {
	if u == nil {
		return posting.JournalEntry{}, ErrInvalidInput
	}
	return GenerateDeferredTaxEntry(input)
}

func (u *Unit) GenerateCurrentTaxEntry(input CurrentTaxEntryInput) (posting.JournalEntry, error) {
	if u == nil {
		return posting.JournalEntry{}, ErrInvalidInput
	}
	return GenerateCurrentTaxEntry(input)
}

// ClosePeriod measures balances, generates a deferred-tax journal draft, and
// returns both outputs for validation before central posting.
func (u *Unit) ClosePeriod(
	balances []TaxBalance,
	period MeasurementPeriod,
	prior DeferredTaxPosition,
	accounts DeferredTaxAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
) (Measurement, posting.JournalEntry, error) {
	if u == nil {
		return Measurement{}, posting.JournalEntry{}, ErrInvalidInput
	}
	measurement, err := Measure(balances, period)
	if err != nil {
		return Measurement{}, posting.JournalEntry{}, err
	}
	if prior.DTA.Currency() == "" || prior.DTL.Currency() == "" {
		zero, zeroErr := financialtypes.Zero(measurement.Currency)
		if zeroErr != nil {
			return Measurement{}, posting.JournalEntry{}, zeroErr
		}
		if prior.DTA.Currency() == "" {
			prior.DTA = zero
		}
		if prior.DTL.Currency() == "" {
			prior.DTL = zero
		}
	}
	entry, err := GenerateDeferredTaxEntry(DeferredTaxEntryInput{
		Previous: prior, Closing: DeferredTaxPosition{DTA: measurement.DTA, DTL: measurement.DTL},
		Accounts: accounts, CompanyID: companyID, JournalID: journalID,
		EntryDate: period.AsOf, CreatedBy: createdBy,
	})
	if err != nil {
		return Measurement{}, posting.JournalEntry{}, err
	}
	return measurement, entry, nil
}
