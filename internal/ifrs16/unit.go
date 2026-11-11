package ifrs16

import (
	"time"

	"accounting_engine/internal/posting"
	"accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Unit coordinates lease schedule, modification, and period-entry operations.
type Unit struct{}

func NewUnit() *Unit { return &Unit{} }

func (u *Unit) AmortizationSchedule(contract LeaseContract) ([]SchedulePeriod, error) {
	if u == nil {
		return nil, ErrInvalidContract
	}
	return contract.AmortizationSchedule()
}

func (u *Unit) ModifyLeaseContext(
	contract LeaseContract,
	effectiveDate time.Time,
	remainingPayments []LeasePayment,
	newAnnualDiscountRate decimal.Decimal,
	liabilityCarryingAmount, rouCarryingAmount types.Money,
) (ModificationResult, error) {
	if u == nil {
		return ModificationResult{}, ErrInvalidContract
	}
	return contract.RecalculateModification(effectiveDate, remainingPayments, newAnnualDiscountRate,
		liabilityCarryingAmount, rouCarryingAmount)
}

// ClosePeriod returns balanced payment/interest and depreciation journal drafts
// for all schedule periods due by the requested date.
func (u *Unit) ClosePeriod(
	contract LeaseContract,
	through time.Time,
	accounts LeaseAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
) ([]posting.JournalEntry, error) {
	if u == nil || through.IsZero() {
		return nil, ErrInvalidContract
	}
	schedule, err := contract.AmortizationSchedule()
	if err != nil {
		return nil, err
	}
	entries := make([]posting.JournalEntry, 0, len(schedule)*2)
	for _, period := range schedule {
		if period.DueDate.After(through) {
			continue
		}
		payment, err := BuildPaymentEntry(period, accounts, companyID, journalID, createdBy)
		if err != nil {
			return nil, err
		}
		depreciation, err := BuildDepreciationEntry(period, accounts, companyID, journalID, createdBy)
		if err != nil {
			return nil, err
		}
		entries = append(entries, payment, depreciation)
	}
	return entries, nil
}
