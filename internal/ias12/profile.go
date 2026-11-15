package ias12

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

var (
	ErrInvalidTaxProfile    = errors.New("invalid IAS 12 tax profile")
	ErrMissingTaxProvenance = errors.New("IAS 12 tax data provenance is required")
	ErrMissingDTAEvidence   = errors.New("probable-profit evidence is required for deductible differences")
	ErrRateUnavailable      = errors.New("no enacted tax rate is effective on the required date")
	ErrPostingNotConfigured = errors.New("IAS 12 posting requires a configured application and journal mapping")
)

// TaxProfile is an explicitly scoped, versioned set of company/jurisdiction tax
// rates. It is not a tax-law engine; callers remain responsible for selecting
// the applicable local law and classifying tax bases.
type TaxProfile struct {
	ID              string
	CompanyID       uuid.UUID
	Jurisdiction    string
	Currency        string
	Version         string
	EffectiveFrom   time.Time
	SourceReference string
	Rates           []TaxRate
}

func (p TaxProfile) Validate(asOf time.Time) error {
	if strings.TrimSpace(p.ID) == "" || p.CompanyID == uuid.Nil ||
		strings.TrimSpace(p.Jurisdiction) == "" || strings.TrimSpace(p.Currency) == "" ||
		strings.TrimSpace(p.Version) == "" || p.EffectiveFrom.IsZero() ||
		p.EffectiveFrom.After(asOf) || strings.TrimSpace(p.SourceReference) == "" ||
		len(p.Rates) == 0 {
		return ErrInvalidTaxProfile
	}
	seen := make(map[int64]struct{}, len(p.Rates))
	for _, rate := range p.Rates {
		if err := rate.Validate(asOf); err != nil {
			return err
		}
		if strings.TrimSpace(rate.SourceReference) == "" {
			return ErrMissingTaxProvenance
		}
		key := rate.EffectiveFrom.UTC().UnixNano()
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: duplicate rate effective date %s", ErrInvalidTaxProfile, rate.EffectiveFrom.Format(time.RFC3339))
		}
		seen[key] = struct{}{}
	}
	return nil
}

// RateFor returns the most recently effective rate that is enacted by asOf and
// effective no later than the date the temporary difference is expected to
// reverse. Later rates are ignored, even when already enacted.
func (p TaxProfile) RateFor(asOf, effectiveDate time.Time) (TaxRate, error) {
	if err := p.Validate(asOf); err != nil {
		return TaxRate{}, err
	}
	var selected TaxRate
	found := false
	for _, rate := range p.Rates {
		if rate.EffectiveFrom.After(effectiveDate) || rate.EnactedOn.After(asOf) {
			continue
		}
		if !found || rate.EffectiveFrom.After(selected.EffectiveFrom) {
			selected, found = rate, true
		}
	}
	if !found {
		return TaxRate{}, ErrRateUnavailable
	}
	return selected, nil
}

// MeasureWithProfile measures balances using versioned, effective-date rates
// and requires explicit source references and bounded DTA evidence.
func MeasureWithProfile(profile TaxProfile, balances []TaxBalance, period MeasurementPeriod) (Measurement, error) {
	if err := period.Validate(); err != nil {
		return Measurement{}, err
	}
	if err := profile.Validate(period.AsOf); err != nil {
		return Measurement{}, err
	}
	prepared := make([]TaxBalance, len(balances))
	copy(prepared, balances)
	for i := range prepared {
		balance := &prepared[i]
		if strings.TrimSpace(balance.CarryingAmountSource) == "" ||
			strings.TrimSpace(balance.TaxBaseSource) == "" {
			return Measurement{}, ErrMissingTaxProvenance
		}
		if balance.CarryingAmount.Currency() != profile.Currency ||
			balance.TaxBase.Currency() != profile.Currency {
			return Measurement{}, ErrCurrencyMismatch
		}
		if balance.ExpectedReversalDate.IsZero() || balance.ExpectedReversalDate.Before(period.AsOf) {
			return Measurement{}, ErrInvalidInput
		}
		rate, err := profile.RateFor(period.AsOf, balance.ExpectedReversalDate)
		if err != nil {
			return Measurement{}, err
		}
		balance.Rate = rate
		difference := balance.CarryingAmount.Amount().Sub(balance.TaxBase.Amount())
		hasDeductibleDifference := balance.Kind == Asset && difference.IsNegative() ||
			balance.Kind == Liability && difference.IsPositive()
		if hasDeductibleDifference {
			evidence := balance.RecognitionEvidence
			if evidence == nil || strings.TrimSpace(evidence.Reference) == "" ||
				evidence.AssessedOn.IsZero() || evidence.AssessedOn.After(period.AsOf) {
				return Measurement{}, ErrMissingDTAEvidence
			}
			if err := validateNonnegativeMoney(evidence.SupportedDeductibleAmount); err != nil {
				return Measurement{}, err
			}
			if evidence.SupportedDeductibleAmount.Currency() != profile.Currency {
				return Measurement{}, ErrCurrencyMismatch
			}
			balance.FutureTaxableProfitProbable = nil
		}
	}
	measurement, err := Measure(prepared, period)
	if err != nil {
		return Measurement{}, err
	}
	measurement.TaxProfileID = profile.ID
	measurement.CompanyID = profile.CompanyID
	measurement.Jurisdiction = profile.Jurisdiction
	measurement.ProfileVersion = profile.Version
	measurement.ProfileSourceReference = profile.SourceReference
	return measurement, nil
}

type CurrentTaxInput struct {
	Period          MeasurementPeriod
	TaxableProfit   financialtypes.Money
	SourceReference string
}

type CurrentTaxMeasurement struct {
	ProfileID              string
	CompanyID              uuid.UUID
	Jurisdiction           string
	ProfileVersion         string
	ProfileSourceReference string
	Period                 MeasurementPeriod
	TaxableProfit          financialtypes.Money
	Rate                   TaxRate
	TaxExpense             financialtypes.Money
	SourceReference        string
}

// MeasureCurrentTax computes a simple current-tax estimate from a supplied
// nonnegative taxable-profit base and the profile rate effective at period end.
// Tax losses, credits, uncertain positions, and filing rules are out of scope.
func MeasureCurrentTax(profile TaxProfile, input CurrentTaxInput) (CurrentTaxMeasurement, error) {
	if err := input.Period.Validate(); err != nil {
		return CurrentTaxMeasurement{}, err
	}
	if err := profile.Validate(input.Period.AsOf); err != nil {
		return CurrentTaxMeasurement{}, err
	}
	if strings.TrimSpace(input.SourceReference) == "" {
		return CurrentTaxMeasurement{}, ErrMissingTaxProvenance
	}
	if err := validateNonnegativeMoney(input.TaxableProfit); err != nil {
		return CurrentTaxMeasurement{}, err
	}
	if input.TaxableProfit.Currency() != profile.Currency {
		return CurrentTaxMeasurement{}, ErrCurrencyMismatch
	}
	rate, err := profile.RateFor(input.Period.AsOf, input.Period.AsOf)
	if err != nil {
		return CurrentTaxMeasurement{}, err
	}
	expense, err := financialtypes.NewMoney(input.TaxableProfit.Amount().Mul(rate.Value), profile.Currency)
	if err != nil {
		return CurrentTaxMeasurement{}, err
	}
	return CurrentTaxMeasurement{
		ProfileID: profile.ID, CompanyID: profile.CompanyID, Jurisdiction: profile.Jurisdiction,
		ProfileVersion: profile.Version, ProfileSourceReference: profile.SourceReference, Period: input.Period,
		TaxableProfit: input.TaxableProfit, Rate: rate, TaxExpense: expense,
		SourceReference: input.SourceReference,
	}, nil
}

// ConfiguredPosting is host-supplied evidence that a real journal mapping and
// application posting workflow are configured. This package creates drafts
// only; it does not authorize or persist entries.
type ConfiguredPosting struct {
	CompanyID                         uuid.UUID
	JournalMappingReference           string
	ApplicationConfigurationReference string
	CurrentExpenseAccountID           uuid.UUID
	CurrentPayableAccountID           uuid.UUID
	DeferredAccounts                  DeferredTaxAccounts
}

func (c ConfiguredPosting) validate(profile TaxProfile) error {
	if c.CompanyID == uuid.Nil || c.CompanyID != profile.CompanyID ||
		strings.TrimSpace(c.JournalMappingReference) == "" ||
		strings.TrimSpace(c.ApplicationConfigurationReference) == "" {
		return ErrPostingNotConfigured
	}
	return nil
}

func GenerateConfiguredCurrentTaxEntry(
	measurement CurrentTaxMeasurement,
	config ConfiguredPosting,
	journalID uuid.UUID,
	createdBy string,
) (posting.JournalEntry, error) {
	profile := TaxProfile{ID: measurement.ProfileID, CompanyID: measurement.CompanyID}
	if err := config.validate(profile); err != nil ||
		measurement.ProfileID == "" || measurement.ProfileVersion == "" ||
		measurement.Jurisdiction == "" || measurement.ProfileSourceReference == "" ||
		measurement.SourceReference == "" || measurement.Rate.SourceReference == "" ||
		measurement.TaxableProfit.Currency() == "" || measurement.TaxExpense.Currency() != measurement.TaxableProfit.Currency() ||
		measurement.Rate.Validate(measurement.Period.AsOf) != nil ||
		!measurement.TaxExpense.Amount().Equal(measurement.TaxableProfit.Amount().Mul(measurement.Rate.Value)) ||
		measurement.Period.Validate() != nil {
		return posting.JournalEntry{}, ErrPostingNotConfigured
	}
	if config.CurrentExpenseAccountID == uuid.Nil || config.CurrentPayableAccountID == uuid.Nil ||
		config.CurrentExpenseAccountID == config.CurrentPayableAccountID {
		return posting.JournalEntry{}, ErrPostingNotConfigured
	}
	entry, err := GenerateCurrentTaxEntry(CurrentTaxEntryInput{
		CompanyID: config.CompanyID, JournalID: journalID,
		ExpenseAccountID: config.CurrentExpenseAccountID,
		PayableAccountID: config.CurrentPayableAccountID,
		EntryDate:        measurement.Period.AsOf, CreatedBy: createdBy,
		Amount: measurement.TaxExpense,
	})
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.Reference = fmt.Sprintf("Current tax estimate: profile=%s version=%s mapping=%s application=%s source=%s rate-source=%s",
		measurement.ProfileID, measurement.ProfileVersion, config.JournalMappingReference,
		config.ApplicationConfigurationReference, measurement.SourceReference, measurement.Rate.SourceReference)
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func GenerateConfiguredDeferredTaxEntry(
	measurement Measurement,
	prior DeferredTaxPosition,
	config ConfiguredPosting,
	journalID uuid.UUID,
	createdBy string,
) (posting.JournalEntry, error) {
	profile := TaxProfile{ID: measurement.TaxProfileID, CompanyID: measurement.CompanyID}
	if err := config.validate(profile); err != nil || measurement.TaxProfileID == "" {
		return posting.JournalEntry{}, ErrPostingNotConfigured
	}
	if err := validateProfiledMeasurement(measurement); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := validateDeferredAccounts(config.DeferredAccounts); err != nil {
		return posting.JournalEntry{}, ErrPostingNotConfigured
	}
	if prior.DTA.Currency() == "" || prior.DTL.Currency() == "" {
		zero, err := financialtypes.Zero(measurement.Currency)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		if prior.DTA.Currency() == "" {
			prior.DTA = zero
		}
		if prior.DTL.Currency() == "" {
			prior.DTL = zero
		}
	}
	entry, err := GenerateDeferredTaxEntry(DeferredTaxEntryInput{
		CompanyID: config.CompanyID, JournalID: journalID, EntryDate: measurement.Period.AsOf,
		CreatedBy: createdBy, Previous: prior,
		Closing:  DeferredTaxPosition{DTA: measurement.DTA, DTL: measurement.DTL},
		Accounts: config.DeferredAccounts,
	})
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.Reference = fmt.Sprintf("Deferred tax measurement: profile=%s version=%s mapping=%s application=%s",
		measurement.TaxProfileID, measurement.ProfileVersion, config.JournalMappingReference,
		config.ApplicationConfigurationReference)
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

func validateProfiledMeasurement(measurement Measurement) error {
	if measurement.Period.Validate() != nil || measurement.Currency == "" ||
		measurement.CompanyID == uuid.Nil || measurement.TaxProfileID == "" ||
		measurement.Jurisdiction == "" || measurement.ProfileVersion == "" ||
		measurement.ProfileSourceReference == "" || len(measurement.Balances) == 0 {
		return ErrPostingNotConfigured
	}
	for _, balance := range measurement.Balances {
		if balance.ID == "" || balance.CarryingAmountSource == "" || balance.TaxBaseSource == "" ||
			balance.ExpectedReversalDate.IsZero() || balance.ExpectedReversalDate.Before(measurement.Period.AsOf) ||
			balance.Rate.SourceReference == "" || balance.Rate.Validate(measurement.Period.AsOf) != nil {
			return ErrMissingTaxProvenance
		}
		if balance.DTA.Amount().IsPositive() && balance.RecognitionEvidenceReference == "" {
			return ErrMissingDTAEvidence
		}
	}
	return nil
}

func validateDeferredAccounts(accounts DeferredTaxAccounts) error {
	if accounts.Expense == uuid.Nil || accounts.DTA == uuid.Nil || accounts.DTL == uuid.Nil ||
		accounts.Expense == accounts.DTA || accounts.Expense == accounts.DTL ||
		accounts.DTA == accounts.DTL ||
		(accounts.OCI != uuid.Nil && (accounts.OCI == accounts.Expense ||
			accounts.OCI == accounts.DTA || accounts.OCI == accounts.DTL)) {
		return ErrInvalidInput
	}
	return nil
}

func evidenceReference(evidence *DTARecognitionEvidence) string {
	if evidence == nil {
		return ""
	}
	return evidence.Reference
}
