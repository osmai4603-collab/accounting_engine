package ifrs16

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/types"
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
	ctx context.Context,
	contract LeaseContract,
	through time.Time,
	builder *EntryBuilder,
	mappingID string,
	companyID, journalID uuid.UUID,
	createdBy string,
) ([]posting.JournalEntry, error) {
	if u == nil || through.IsZero() || builder == nil {
		return nil, ErrInvalidContract
	}
	if contract.Reference == "" {
		return nil, ErrLeaseReferenceRequired
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
		payment, err := builder.BuildPaymentEntry(ctx, period, companyID, journalID, mappingID, createdBy)
		if err != nil {
			return nil, err
		}
		depreciation, err := builder.BuildDepreciationEntry(ctx, period, companyID, journalID, mappingID, createdBy)
		if err != nil {
			return nil, err
		}
		entries = append(entries, payment, depreciation)
	}
	return entries, nil
}
