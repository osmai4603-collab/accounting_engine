package ifrs16

import (
	"errors"
	"fmt"
	"time"

	"accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidContract     = errors.New("invalid lease contract")
	ErrInvalidPayment      = errors.New("invalid lease payment")
	ErrInvalidAccounts     = errors.New("invalid lease journal accounts")
	ErrInvalidCarrying     = errors.New("invalid lease carrying amount")
	ErrNegativeLiability   = errors.New("lease payment exceeds accrued liability")
	ErrDepreciationUnavail = errors.New("no depreciation due for this period")
)

var (
	zero     = decimal.Zero
	one      = decimal.NewFromInt(1)
	twelve   = decimal.NewFromInt(12)
	scale    = int32(types.FinancialScale)
	internal = int32(types.InternalScale)
)

// LeasePayment is a contractual payment due at the end of one monthly period.
type LeasePayment struct {
	DueDate time.Time
	Amount  types.Money
}

// LeaseContract contains the inputs and calculated commencement balances for a lease.
type LeaseContract struct {
	CommencementDate      time.Time
	Currency              string
	AnnualDiscountRate    decimal.Decimal
	Payments              []LeasePayment
	InitialDirectCosts    types.Money
	Prepayments           types.Money
	LeaseIncentives       types.Money
	UsefulLifePeriods     int
	InitialLeaseLiability types.Money
	InitialROUAsset       types.Money
}

// SchedulePeriod is one monthly liability and ROU depreciation roll-forward.
type SchedulePeriod struct {
	Period                  int
	DueDate                 time.Time
	Payment                 types.Money
	OpeningLiability        types.Money
	InterestExpense         types.Money
	PrincipalReduction      types.Money
	ClosingLiability        types.Money
	ROUDepreciation         types.Money
	AccumulatedDepreciation types.Money
	ClosingROUAsset         types.Money
}

// NewLeaseContract validates lease terms and calculates commencement balances.
func NewLeaseContract(
	commencementDate time.Time,
	annualDiscountRate decimal.Decimal,
	payments []LeasePayment,
	initialDirectCosts, prepayments, leaseIncentives types.Money,
	usefulLifePeriods int,
) (LeaseContract, error) {
	if commencementDate.IsZero() || len(payments) == 0 || annualDiscountRate.IsNegative() ||
		usefulLifePeriods < 0 {
		return LeaseContract{}, ErrInvalidContract
	}
	currency := initialDirectCosts.Currency()
	if currency == "" {
		currency = prepayments.Currency()
	}
	if currency == "" {
		currency = leaseIncentives.Currency()
	}
	if currency == "" && len(payments) != 0 {
		currency = payments[0].Amount.Currency()
	}
	if currency == "" {
		return LeaseContract{}, ErrInvalidContract
	}
	if err := validateNonnegativeMoney(initialDirectCosts, currency); err != nil {
		return LeaseContract{}, err
	}
	if err := validateNonnegativeMoney(prepayments, currency); err != nil {
		return LeaseContract{}, err
	}
	if err := validateNonnegativeMoney(leaseIncentives, currency); err != nil {
		return LeaseContract{}, err
	}

	copyPayments := append([]LeasePayment(nil), payments...)
	lastDate := commencementDate.UTC()
	for i := range copyPayments {
		payment := &copyPayments[i]
		if payment.DueDate.IsZero() || !payment.DueDate.After(lastDate) ||
			payment.Amount.Currency() != currency || !payment.Amount.Amount().IsPositive() {
			return LeaseContract{}, ErrInvalidPayment
		}
		if _, err := types.NewMoney(payment.Amount.Amount(), currency); err != nil {
			return LeaseContract{}, err
		}
		payment.DueDate = payment.DueDate.UTC()
		lastDate = payment.DueDate
	}

	liability, err := presentValue(copyPayments, annualDiscountRate, currency)
	if err != nil {
		return LeaseContract{}, err
	}
	rou, err := liability.Add(initialDirectCosts)
	if err != nil {
		return LeaseContract{}, err
	}
	rou, err = rou.Add(prepayments)
	if err != nil {
		return LeaseContract{}, err
	}
	rou, err = rou.Subtract(leaseIncentives)
	if err != nil {
		return LeaseContract{}, err
	}
	if !rou.Amount().IsPositive() {
		return LeaseContract{}, ErrInvalidContract
	}

	return LeaseContract{
		CommencementDate:      commencementDate.UTC(),
		Currency:              currency,
		AnnualDiscountRate:    annualDiscountRate,
		Payments:              copyPayments,
		InitialDirectCosts:    initialDirectCosts,
		Prepayments:           prepayments,
		LeaseIncentives:       leaseIncentives,
		UsefulLifePeriods:     usefulLifePeriods,
		InitialLeaseLiability: liability,
		InitialROUAsset:       rou,
	}, nil
}

// AmortizationSchedule returns the complete schedule without mutating the contract.
func (c LeaseContract) AmortizationSchedule() ([]SchedulePeriod, error) {
	if c.InitialLeaseLiability.Currency() == "" || len(c.Payments) == 0 {
		return nil, ErrInvalidContract
	}
	periodicRate := c.AnnualDiscountRate.DivRound(twelve, internal)
	balance := c.InitialLeaseLiability
	rouBalance := c.InitialROUAsset
	accumulated := mustZero(c.Currency)
	depreciationPeriods := c.UsefulLifePeriods
	if depreciationPeriods == 0 || depreciationPeriods > len(c.Payments) {
		depreciationPeriods = len(c.Payments)
	}
	baseDepreciation := c.InitialROUAsset.Amount().DivRound(decimal.NewFromInt(int64(depreciationPeriods)), scale)
	result := make([]SchedulePeriod, 0, len(c.Payments))
	for i, payment := range c.Payments {
		interestAmount := balance.Amount().Mul(periodicRate).Round(scale)
		interest, err := types.NewMoney(interestAmount, c.Currency)
		if err != nil {
			return nil, err
		}
		principal, err := payment.Amount.Subtract(interest)
		if err != nil {
			return nil, err
		}
		closing, err := balance.Subtract(principal)
		if err != nil {
			return nil, err
		}
		if closing.IsNegative() {
			return nil, ErrNegativeLiability
		}

		depreciation := mustZero(c.Currency)
		if i < depreciationPeriods {
			amount := baseDepreciation
			if i == depreciationPeriods-1 {
				amount = c.InitialROUAsset.Amount().Sub(accumulated.Amount())
			}
			depreciation, err = types.NewMoney(amount, c.Currency)
			if err != nil {
				return nil, err
			}
			rouBalance, err = rouBalance.Subtract(depreciation)
			if err != nil {
				return nil, err
			}
			accumulated, err = accumulated.Add(depreciation)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, SchedulePeriod{
			Period:                  i + 1,
			DueDate:                 payment.DueDate,
			Payment:                 payment.Amount,
			OpeningLiability:        balance,
			InterestExpense:         interest,
			PrincipalReduction:      principal,
			ClosingLiability:        closing,
			ROUDepreciation:         depreciation,
			AccumulatedDepreciation: accumulated,
			ClosingROUAsset:         rouBalance,
		})
		balance = closing
	}
	return result, nil
}

func presentValue(payments []LeasePayment, annualRate decimal.Decimal, currency string) (types.Money, error) {
	monthlyRate := annualRate.DivRound(twelve, internal)
	factor := one
	total := zero
	for _, payment := range payments {
		factor = factor.Mul(one.Add(monthlyRate))
		if !factor.IsPositive() {
			return types.Money{}, ErrInvalidContract
		}
		total = total.Add(payment.Amount.Amount().DivRound(factor, internal))
	}
	return types.NewMoney(total.Round(scale), currency)
}

func mustZero(currency string) types.Money {
	m, _ := types.Zero(currency)
	return m
}

func validateNonnegativeMoney(m types.Money, currency string) error {
	if m.Currency() != currency || m.IsNegative() {
		return fmt.Errorf("%w: currency or amount", ErrInvalidContract)
	}
	_, err := types.NewMoney(m.Amount(), currency)
	return err
}
