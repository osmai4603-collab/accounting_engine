package ifrs15

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidFinancingComponent = errors.New("invalid significant financing component")
	ErrInvalidDiscountRate       = errors.New("discount rate must be positive")
)

type FinancingType string

const (
	// CustomerFinancing represents advance payments where customer finances the entity (IFRS 15.60-65).
	// Entity recognizes interest expense increasing contract liability.
	CustomerFinancing FinancingType = "customer_financing"

	// EntityFinancing represents payment in arrears where entity finances the customer (IFRS 15.60-65).
	// Entity recognizes revenue at present value and earns interest income over time.
	EntityFinancing FinancingType = "entity_financing"
)

type FinancingSchedulePeriod struct {
	PeriodNumber    int                  `json:"period_number"`
	StartDate       time.Time            `json:"start_date"`
	EndDate         time.Time            `json:"end_date"`
	BeginningAmount financialtypes.Money `json:"beginning_amount"`
	InterestAmount  financialtypes.Money `json:"interest_amount"`
	EndingAmount    financialtypes.Money `json:"ending_amount"`
}

type FinancingComponent struct {
	ContractID         uuid.UUID                 `json:"contract_id"`
	Type               FinancingType             `json:"type"`
	NominalAmount      financialtypes.Money      `json:"nominal_amount"`
	DiscountRate       decimal.Decimal           `json:"discount_rate"` // Annual discount rate (e.g. 0.06 for 6%)
	TransferDate       time.Time                 `json:"transfer_date"`
	PaymentDate        time.Time                 `json:"payment_date"`
	DurationDays       int                       `json:"duration_days"`
	PracticalExpedient bool                      `json:"practical_expedient"`
	TransactionPrice   financialtypes.Money      `json:"transaction_price"` // Revenue recognized upon transfer
	TotalInterest      financialtypes.Money      `json:"total_interest"`
	Schedule           []FinancingSchedulePeriod `json:"schedule"`
}

// IsPracticalExpedientApplicable checks whether the practical expedient under IFRS 15.63 applies
// (period between performance obligation transfer and payment is one year or less, i.e. <= 365 days).
func IsPracticalExpedientApplicable(transferDate, paymentDate time.Time) bool {
	t1 := dateOnly(transferDate)
	t2 := dateOnly(paymentDate)
	diff := t2.Sub(t1)
	if diff < 0 {
		diff = -diff
	}
	days := int(diff.Hours() / 24)
	return days <= 365
}

// CalculateFinancingComponent determines the significant financing component for contracts exceeding one year
// between transfer and payment date in accordance with IFRS 15.60-65.
func CalculateFinancingComponent(
	contractID uuid.UUID,
	nominalAmount financialtypes.Money,
	annualDiscountRate decimal.Decimal,
	transferDate, paymentDate time.Time,
	financingType FinancingType,
) (FinancingComponent, error) {
	if contractID == uuid.Nil || nominalAmount.Currency() == "" || !nominalAmount.Amount().IsPositive() {
		return FinancingComponent{}, ErrInvalidFinancingComponent
	}
	if transferDate.IsZero() || paymentDate.IsZero() {
		return FinancingComponent{}, ErrInvalidPeriod
	}
	if financingType != CustomerFinancing && financingType != EntityFinancing {
		return FinancingComponent{}, fmt.Errorf("%w: unknown financing type %q", ErrInvalidFinancingComponent, financingType)
	}

	tTransfer := dateOnly(transferDate)
	tPayment := dateOnly(paymentDate)
	currency := nominalAmount.Currency()
	minorUnits := financialtypes.CurrencyMinorUnit(currency)

	var days int
	if financingType == CustomerFinancing {
		// Customer pays before transfer: paymentDate < transferDate
		if !tPayment.Before(tTransfer) {
			return FinancingComponent{}, fmt.Errorf("%w: customer financing requires payment date before transfer date", ErrInvalidPeriod)
		}
		days = int(tTransfer.Sub(tPayment).Hours() / 24)
	} else {
		// Entity finances customer: transferDate < paymentDate
		if !tTransfer.Before(tPayment) {
			return FinancingComponent{}, fmt.Errorf("%w: entity financing requires transfer date before payment date", ErrInvalidPeriod)
		}
		days = int(tPayment.Sub(tTransfer).Hours() / 24)
	}

	if days <= 365 {
		// Practical expedient applies: no adjustment
		zero, err := financialtypes.Zero(currency)
		if err != nil {
			return FinancingComponent{}, err
		}
		return FinancingComponent{
			ContractID:         contractID,
			Type:               financingType,
			NominalAmount:      nominalAmount,
			DiscountRate:       annualDiscountRate,
			TransferDate:       tTransfer,
			PaymentDate:        tPayment,
			DurationDays:       days,
			PracticalExpedient: true,
			TransactionPrice:   nominalAmount,
			TotalInterest:      zero,
			Schedule:           nil,
		}, nil
	}

	if !annualDiscountRate.IsPositive() {
		return FinancingComponent{}, ErrInvalidDiscountRate
	}

	// Calculate compound periods: whole years plus fractional remainder
	years := decimal.NewFromInt(int64(days)).Div(decimal.NewFromInt(365))
	rFloat, _ := annualDiscountRate.Float64()
	yFloat, _ := years.Float64()

	numPeriods := int(math.Ceil(yFloat))
	if numPeriods < 1 {
		numPeriods = 1
	}

	schedule := make([]FinancingSchedulePeriod, 0, numPeriods)
	var transactionPrice financialtypes.Money
	var totalInterest financialtypes.Money

	if financingType == CustomerFinancing {
		// Customer pays in advance:
		// Advance cash received = NominalAmount.
		// Future Value (Transaction Price / Revenue at transfer date) = Nominal * (1 + r)^years
		fvFactor := decimal.NewFromFloat(math.Pow(1.0+rFloat, yFloat))
		fvAmount := nominalAmount.Amount().Mul(fvFactor).RoundBank(minorUnits)
		var err error
		transactionPrice, err = financialtypes.NewMoney(fvAmount, currency)
		if err != nil {
			return FinancingComponent{}, err
		}
		totalInterest, err = transactionPrice.Subtract(nominalAmount)
		if err != nil {
			return FinancingComponent{}, err
		}

		// Amortization schedule (contract liability increases by interest expense)
		currentBeginning := nominalAmount
		startDate := tPayment
		for i := 1; i <= numPeriods; i++ {
			periodEndDate := startDate.AddDate(1, 0, 0)
			if periodEndDate.After(tTransfer) || i == numPeriods {
				periodEndDate = tTransfer
			}
			periodDays := int(periodEndDate.Sub(startDate).Hours() / 24)
			periodFraction := decimal.NewFromInt(int64(periodDays)).Div(decimal.NewFromInt(365))

			var interestAmount financialtypes.Money
			if i == numPeriods {
				// Reconcile rounding to exact totalInterest
				interestAmount, err = transactionPrice.Subtract(currentBeginning)
				if err != nil {
					return FinancingComponent{}, err
				}
			} else {
				interestDec := currentBeginning.Amount().Mul(annualDiscountRate).Mul(periodFraction).RoundBank(minorUnits)
				interestAmount, err = financialtypes.NewMoney(interestDec, currency)
				if err != nil {
					return FinancingComponent{}, err
				}
			}

			endingAmount, err := currentBeginning.Add(interestAmount)
			if err != nil {
				return FinancingComponent{}, err
			}

			schedule = append(schedule, FinancingSchedulePeriod{
				PeriodNumber:    i,
				StartDate:       startDate,
				EndDate:         periodEndDate,
				BeginningAmount: currentBeginning,
				InterestAmount:  interestAmount,
				EndingAmount:    endingAmount,
			})
			currentBeginning = endingAmount
			startDate = periodEndDate
		}

	} else {
		// Entity financing (arrears):
		// Customer pays nominal at future date.
		// Present Value (Cash Selling Price / Revenue at transfer date) = Nominal / (1 + r)^years
		pvFactor := decimal.NewFromFloat(math.Pow(1.0+rFloat, yFloat))
		pvAmount := nominalAmount.Amount().Div(pvFactor).RoundBank(minorUnits)
		var err error
		transactionPrice, err = financialtypes.NewMoney(pvAmount, currency)
		if err != nil {
			return FinancingComponent{}, err
		}
		totalInterest, err = nominalAmount.Subtract(transactionPrice)
		if err != nil {
			return FinancingComponent{}, err
		}

		// Amortization schedule (receivable increases by interest income up to nominal)
		currentBeginning := transactionPrice
		startDate := tTransfer
		for i := 1; i <= numPeriods; i++ {
			periodEndDate := startDate.AddDate(1, 0, 0)
			if periodEndDate.After(tPayment) || i == numPeriods {
				periodEndDate = tPayment
			}
			periodDays := int(periodEndDate.Sub(startDate).Hours() / 24)
			periodFraction := decimal.NewFromInt(int64(periodDays)).Div(decimal.NewFromInt(365))

			var interestAmount financialtypes.Money
			if i == numPeriods {
				// Reconcile rounding to nominalAmount
				interestAmount, err = nominalAmount.Subtract(currentBeginning)
				if err != nil {
					return FinancingComponent{}, err
				}
			} else {
				interestDec := currentBeginning.Amount().Mul(annualDiscountRate).Mul(periodFraction).RoundBank(minorUnits)
				interestAmount, err = financialtypes.NewMoney(interestDec, currency)
				if err != nil {
					return FinancingComponent{}, err
				}
			}

			endingAmount, err := currentBeginning.Add(interestAmount)
			if err != nil {
				return FinancingComponent{}, err
			}

			schedule = append(schedule, FinancingSchedulePeriod{
				PeriodNumber:    i,
				StartDate:       startDate,
				EndDate:         periodEndDate,
				BeginningAmount: currentBeginning,
				InterestAmount:  interestAmount,
				EndingAmount:    endingAmount,
			})
			currentBeginning = endingAmount
			startDate = periodEndDate
		}
	}

	return FinancingComponent{
		ContractID:         contractID,
		Type:               financingType,
		NominalAmount:      nominalAmount,
		DiscountRate:       annualDiscountRate,
		TransferDate:       tTransfer,
		PaymentDate:        tPayment,
		DurationDays:       days,
		PracticalExpedient: false,
		TransactionPrice:   transactionPrice,
		TotalInterest:      totalInterest,
		Schedule:           schedule,
	}, nil
}

// BuildFinancingInterestJournal generates a balanced journal entry for the periodic financing component:
// - For Customer Financing (Advance): Dr Interest Expense, Cr Contract Liability
// - For Entity Financing (Arrears): Dr Contract Asset / Receivable, Cr Interest Income
func BuildFinancingInterestJournal(
	fc FinancingComponent,
	period FinancingSchedulePeriod,
	journalID uuid.UUID,
	date time.Time,
	interestAccountID, balanceAccountID uuid.UUID,
	customerID uuid.UUID,
) (posting.JournalEntry, error) {
	if journalID == uuid.Nil || interestAccountID == uuid.Nil || balanceAccountID == uuid.Nil || date.IsZero() {
		return posting.JournalEntry{}, ErrInvalidContract
	}
	if !period.InterestAmount.Amount().IsPositive() {
		return posting.JournalEntry{}, ErrInvalidAmount
	}

	var lines []posting.JournalEntryLine
	if fc.Type == CustomerFinancing {
		// Dr Interest Expense, Cr Contract Liability
		lines = []posting.JournalEntryLine{
			{AccountID: interestAccountID, Debit: period.InterestAmount, PartnerID: &customerID},
			{AccountID: balanceAccountID, Credit: period.InterestAmount, PartnerID: &customerID},
		}
	} else {
		// Dr Contract Asset / Receivable, Cr Interest Income
		lines = []posting.JournalEntryLine{
			{AccountID: balanceAccountID, Debit: period.InterestAmount, PartnerID: &customerID},
			{AccountID: interestAccountID, Credit: period.InterestAmount, PartnerID: &customerID},
		}
	}

	return posting.NewJournalEntry(journalID, date, "ifrs15-financing-interest", lines)
}
