package ias12

import (
	"errors"
	"fmt"
	"strings"
	"time"

	financialtypes "accounting_engine/internal/types"

	"github.com/shopspring/decimal"
)

var (
	ErrInvalidInput             = errors.New("invalid IAS 12 input")
	ErrInvalidRate              = errors.New("invalid enacted tax rate")
	ErrRateNotEnacted           = errors.New("tax rate was not enacted by the measurement date")
	ErrCurrencyMismatch         = errors.New("IAS 12 amounts use different currencies")
	ErrNoBalances               = errors.New("at least one tax balance is required")
	ErrNoDeferredTaxMovement    = errors.New("no deferred tax movement to post")
	ErrInvalidMeasurementPeriod = errors.New("measurement period must be quarterly or annual with an end date")
)

type BalanceKind string

const (
	Asset     BalanceKind = "asset"
	Liability BalanceKind = "liability"
)

type PeriodKind string

const (
	Quarterly PeriodKind = "quarterly"
	Annual    PeriodKind = "annual"
)

type MeasurementPeriod struct {
	Kind PeriodKind
	AsOf time.Time
}

func (p MeasurementPeriod) Validate() error {
	if p.AsOf.IsZero() || (p.Kind != Quarterly && p.Kind != Annual) {
		return ErrInvalidMeasurementPeriod
	}
	return nil
}

// TaxRate.Value is a decimal fraction (0.25 means 25%); zero represents an enacted 0% rate.
type TaxRate struct {
	Value         decimal.Decimal
	Enacted       bool
	EnactedOn     time.Time
	EffectiveFrom time.Time
}

func (r TaxRate) Validate(asOf time.Time) error {
	if r.Value.IsNegative() || r.Value.GreaterThan(decimal.NewFromInt(1)) {
		return ErrInvalidRate
	}
	if !r.Enacted || r.EnactedOn.IsZero() || r.EffectiveFrom.IsZero() ||
		r.EnactedOn.After(asOf) || r.EffectiveFrom.After(asOf) {
		return ErrRateNotEnacted
	}
	return nil
}

type TaxBalance struct {
	ID             string
	Kind           BalanceKind
	CarryingAmount financialtypes.Money
	TaxBase        financialtypes.Money
	Rate           TaxRate
	// When non-nil, controls recognition of a DTA for this deductible
	// temporary difference based on probable future taxable profit. Nil
	// preserves the historical behavior for existing callers.
	FutureTaxableProfitProbable *bool
}

type MeasuredBalance struct {
	ID                            string
	Kind                          BalanceKind
	TaxableTemporaryDifference    financialtypes.Money
	DeductibleTemporaryDifference financialtypes.Money
	DTA                           financialtypes.Money
	DTL                           financialtypes.Money
}

type Measurement struct {
	Period   MeasurementPeriod
	Currency string
	Balances []MeasuredBalance
	DTA      financialtypes.Money
	DTL      financialtypes.Money
}

func Measure(balances []TaxBalance, period MeasurementPeriod) (Measurement, error) {
	if err := period.Validate(); err != nil {
		return Measurement{}, err
	}
	if len(balances) == 0 {
		return Measurement{}, ErrNoBalances
	}

	measurement := Measurement{Period: period}
	seen := make(map[string]struct{}, len(balances))
	var currency string
	var totalDTA, totalDTL decimal.Decimal
	for _, balance := range balances {
		if strings.TrimSpace(balance.ID) == "" || (balance.Kind != Asset && balance.Kind != Liability) {
			return Measurement{}, ErrInvalidInput
		}
		if _, exists := seen[balance.ID]; exists {
			return Measurement{}, fmt.Errorf("%w: duplicate balance ID %q", ErrInvalidInput, balance.ID)
		}
		seen[balance.ID] = struct{}{}
		if err := validateNonnegativeMoney(balance.CarryingAmount); err != nil {
			return Measurement{}, err
		}
		if err := validateNonnegativeMoney(balance.TaxBase); err != nil {
			return Measurement{}, err
		}
		if err := sameCurrency(balance.CarryingAmount, balance.TaxBase); err != nil {
			return Measurement{}, err
		}
		if err := balance.Rate.Validate(period.AsOf); err != nil {
			return Measurement{}, err
		}
		if currency == "" {
			currency = balance.CarryingAmount.Currency()
		} else if balance.CarryingAmount.Currency() != currency {
			return Measurement{}, ErrCurrencyMismatch
		}

		difference := balance.CarryingAmount.Amount().Sub(balance.TaxBase.Amount())
		var taxable, deductible decimal.Decimal
		if balance.Kind == Asset {
			if difference.IsPositive() {
				taxable = difference
			} else {
				deductible = difference.Abs()
			}
		} else if difference.IsPositive() {
			deductible = difference
		} else {
			taxable = difference.Abs()
		}
		dtaAmount := deductible.Mul(balance.Rate.Value)
		if balance.FutureTaxableProfitProbable != nil && !*balance.FutureTaxableProfitProbable {
			dtaAmount = decimal.Zero
		}
		dtlAmount := taxable.Mul(balance.Rate.Value)
		taxableMoney, err := financialtypes.NewMoney(taxable, currency)
		if err != nil {
			return Measurement{}, err
		}
		deductibleMoney, err := financialtypes.NewMoney(deductible, currency)
		if err != nil {
			return Measurement{}, err
		}
		dtaMoney, err := financialtypes.NewMoney(dtaAmount, currency)
		if err != nil {
			return Measurement{}, err
		}
		dtlMoney, err := financialtypes.NewMoney(dtlAmount, currency)
		if err != nil {
			return Measurement{}, err
		}
		measurement.Balances = append(measurement.Balances, MeasuredBalance{
			ID:                            balance.ID,
			Kind:                          balance.Kind,
			TaxableTemporaryDifference:    taxableMoney,
			DeductibleTemporaryDifference: deductibleMoney,
			DTA:                           dtaMoney,
			DTL:                           dtlMoney,
		})
		totalDTA = totalDTA.Add(dtaAmount)
		totalDTL = totalDTL.Add(dtlAmount)
	}
	measurement.Currency = currency
	var err error
	measurement.DTA, err = financialtypes.NewMoney(totalDTA, currency)
	if err != nil {
		return Measurement{}, err
	}
	measurement.DTL, err = financialtypes.NewMoney(totalDTL, currency)
	if err != nil {
		return Measurement{}, err
	}
	return measurement, nil
}

type PermanentDifference struct {
	Description string
	// Positive amounts increase taxable profit; negative amounts reduce it.
	Amount financialtypes.Money
}

type TaxReconciliationInput struct {
	Period                    MeasurementPeriod
	AccountingProfitBeforeTax financialtypes.Money
	StatutoryRate             TaxRate
	PermanentDifferences      []PermanentDifference
	CurrentTaxExpense         financialtypes.Money
	DeferredTaxExpense        financialtypes.Money
}

type TaxReconciliationReport struct {
	AccountingProfitBeforeTax  financialtypes.Money
	StatutoryTaxRate           decimal.Decimal
	ExpectedTaxExpense         financialtypes.Money
	PermanentDifferencesTax    financialtypes.Money
	TemporaryDifferencesChange financialtypes.Money
	EffectiveTaxExpense        financialtypes.Money
	EffectiveTaxRate           decimal.Decimal
	EffectiveTaxRateAvailable  bool
}

func Reconcile(input TaxReconciliationInput) (TaxReconciliationReport, error) {
	if err := input.Period.Validate(); err != nil {
		return TaxReconciliationReport{}, err
	}
	if err := validateMoney(input.AccountingProfitBeforeTax); err != nil {
		return TaxReconciliationReport{}, err
	}
	if err := validateSignedMoney(input.CurrentTaxExpense); err != nil {
		return TaxReconciliationReport{}, err
	}
	if err := validateSignedMoney(input.DeferredTaxExpense); err != nil {
		return TaxReconciliationReport{}, err
	}
	currency := input.AccountingProfitBeforeTax.Currency()
	if input.CurrentTaxExpense.Currency() != currency || input.DeferredTaxExpense.Currency() != currency {
		return TaxReconciliationReport{}, ErrCurrencyMismatch
	}
	if err := input.StatutoryRate.Validate(input.Period.AsOf); err != nil {
		return TaxReconciliationReport{}, err
	}

	permanentTax := decimal.Zero
	for _, difference := range input.PermanentDifferences {
		if strings.TrimSpace(difference.Description) == "" {
			return TaxReconciliationReport{}, ErrInvalidInput
		}
		if err := validateSignedMoney(difference.Amount); err != nil {
			return TaxReconciliationReport{}, err
		}
		if difference.Amount.Currency() != currency {
			return TaxReconciliationReport{}, ErrCurrencyMismatch
		}
		permanentTax = permanentTax.Add(difference.Amount.Amount().Mul(input.StatutoryRate.Value))
	}

	expectedTax := input.AccountingProfitBeforeTax.Amount().Mul(input.StatutoryRate.Value)
	effectiveTax := input.CurrentTaxExpense.Amount().Add(input.DeferredTaxExpense.Amount())
	expectedMoney, err := financialtypes.NewMoney(expectedTax, currency)
	if err != nil {
		return TaxReconciliationReport{}, err
	}
	permanentMoney, err := financialtypes.NewMoney(permanentTax, currency)
	if err != nil {
		return TaxReconciliationReport{}, err
	}
	effectiveMoney, err := financialtypes.NewMoney(effectiveTax, currency)
	if err != nil {
		return TaxReconciliationReport{}, err
	}
	report := TaxReconciliationReport{
		AccountingProfitBeforeTax:  input.AccountingProfitBeforeTax,
		StatutoryTaxRate:           input.StatutoryRate.Value,
		ExpectedTaxExpense:         expectedMoney,
		PermanentDifferencesTax:    permanentMoney,
		TemporaryDifferencesChange: input.DeferredTaxExpense,
		EffectiveTaxExpense:        effectiveMoney,
	}
	if !input.AccountingProfitBeforeTax.Amount().IsZero() {
		report.EffectiveTaxRate = effectiveTax.Div(input.AccountingProfitBeforeTax.Amount())
		report.EffectiveTaxRateAvailable = true
	}
	return report, nil
}

func validateMoney(m financialtypes.Money) error {
	if m.Currency() == "" {
		return ErrInvalidInput
	}
	return nil
}

func validateNonnegativeMoney(m financialtypes.Money) error {
	if err := validateMoney(m); err != nil {
		return err
	}
	if m.IsNegative() {
		return financialtypes.ErrNegativeAmountProhibited
	}
	return nil
}

func validateSignedMoney(m financialtypes.Money) error {
	return validateMoney(m)
}

func sameCurrency(a, b financialtypes.Money) error {
	if a.Currency() != b.Currency() {
		return ErrCurrencyMismatch
	}
	return nil
}
