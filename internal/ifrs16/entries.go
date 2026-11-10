package ifrs16

import (
	"fmt"
	"time"

	"accounting_engine/internal/posting"
	"accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// LeaseAccounts identifies the general-ledger accounts used by lease entries.
type LeaseAccounts struct {
	ROUAsset                uuid.UUID
	LeaseLiability          uuid.UUID
	Cash                    uuid.UUID
	PrepaidLease            uuid.UUID
	InterestExpense         uuid.UUID
	DepreciationExpense     uuid.UUID
	AccumulatedDepreciation uuid.UUID
	ModificationGain        uuid.UUID
	TerminationGain         uuid.UUID
	TerminationLoss         uuid.UUID
}

// BuildCommencementEntry records the initial ROU, liability, and commencement cash items.
func BuildCommencementEntry(
	contract LeaseContract, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if contract.InitialROUAsset.Currency() == "" || accounts.ROUAsset == uuid.Nil ||
		accounts.LeaseLiability == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAccounts
	}
	lines := []posting.JournalEntryLine{
		debitLine(accounts.ROUAsset, contract.InitialROUAsset),
		creditLine(accounts.LeaseLiability, contract.InitialLeaseLiability),
	}
	if contract.InitialDirectCosts.Amount().IsPositive() {
		if accounts.Cash == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidAccounts
		}
		lines = append(lines, creditLine(accounts.Cash, contract.InitialDirectCosts))
	}
	if contract.Prepayments.Amount().IsPositive() {
		if accounts.PrepaidLease == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidAccounts
		}
		lines = append(lines, creditLine(accounts.PrepaidLease, contract.Prepayments))
	}
	if contract.LeaseIncentives.Amount().IsPositive() {
		if accounts.Cash == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidAccounts
		}
		lines = append(lines, debitLine(accounts.Cash, contract.LeaseIncentives))
	}
	return newEntry(contract.CommencementDate, companyID, journalID, createdBy, lines)
}

// BuildPaymentEntry records interest, the liability movement, and cash payment for one period.
func BuildPaymentEntry(
	period SchedulePeriod, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if period.Payment.Currency() == "" || !period.Payment.Amount().IsPositive() ||
		accounts.LeaseLiability == uuid.Nil || accounts.Cash == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAccounts
	}
	lines := make([]posting.JournalEntryLine, 0, 3)
	if period.InterestExpense.Amount().IsPositive() {
		if accounts.InterestExpense == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidAccounts
		}
		lines = append(lines, debitLine(accounts.InterestExpense, period.InterestExpense))
	}
	if period.PrincipalReduction.Amount().IsPositive() {
		lines = append(lines, debitLine(accounts.LeaseLiability, period.PrincipalReduction))
	} else if period.PrincipalReduction.Amount().IsNegative() {
		increase, err := types.NewMoney(period.PrincipalReduction.Amount().Abs(), period.PrincipalReduction.Currency())
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, creditLine(accounts.LeaseLiability, increase))
	}
	lines = append(lines, creditLine(accounts.Cash, period.Payment))
	return newEntry(period.DueDate, companyID, journalID, createdBy, lines)
}

// BuildDepreciationEntry records a period's ROU depreciation.
func BuildDepreciationEntry(
	period SchedulePeriod, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if !period.ROUDepreciation.Amount().IsPositive() {
		return posting.JournalEntry{}, ErrDepreciationUnavail
	}
	if accounts.DepreciationExpense == uuid.Nil || accounts.AccumulatedDepreciation == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAccounts
	}
	return newEntry(period.DueDate, companyID, journalID, createdBy, []posting.JournalEntryLine{
		debitLine(accounts.DepreciationExpense, period.ROUDepreciation),
		creditLine(accounts.AccumulatedDepreciation, period.ROUDepreciation),
	})
}

// ModificationResult describes remeasurement of the remaining lease at a modification date.
type ModificationResult struct {
	EffectiveDate       time.Time
	OldLeaseLiability   types.Money
	RemeasuredLiability types.Money
	LiabilityAdjustment types.Money
	OldROUAsset         types.Money
	NewROUAsset         types.Money
	ROUAdjustment       types.Money
	ModificationGain    types.Money
	DiscountRate        decimal.Decimal
	RemainingPayments   []LeasePayment
}

// RecalculateModification remeasures the liability and adjusts the ROU for a non-separate modification.
// Any liability reduction beyond the remaining ROU carrying amount is recognized as a gain.
func (c LeaseContract) RecalculateModification(
	effectiveDate time.Time,
	remainingPayments []LeasePayment,
	newAnnualDiscountRate decimal.Decimal,
	liabilityCarryingAmount, rouCarryingAmount types.Money,
) (ModificationResult, error) {
	if effectiveDate.IsZero() || effectiveDate.Before(c.CommencementDate) || newAnnualDiscountRate.IsNegative() ||
		!validCarrying(liabilityCarryingAmount, c.Currency) ||
		!validCarrying(rouCarryingAmount, c.Currency) || len(remainingPayments) == 0 {
		return ModificationResult{}, ErrInvalidCarrying
	}
	payments, err := validateRemainingPayments(effectiveDate, remainingPayments, c.Currency)
	if err != nil {
		return ModificationResult{}, err
	}
	remeasured, err := presentValue(payments, newAnnualDiscountRate, c.Currency)
	if err != nil {
		return ModificationResult{}, err
	}
	delta, err := remeasured.Subtract(liabilityCarryingAmount)
	if err != nil {
		return ModificationResult{}, err
	}
	rouAdjustment := delta
	gain := mustZero(c.Currency)
	newROU := rouCarryingAmount
	if delta.Amount().IsNegative() && delta.Amount().Abs().GreaterThan(rouCarryingAmount.Amount()) {
		rouAdjustment, err = types.NewMoney(rouCarryingAmount.Amount().Neg(), c.Currency)
		if err != nil {
			return ModificationResult{}, err
		}
		gain, err = types.NewMoney(delta.Amount().Abs().Sub(rouCarryingAmount.Amount()), c.Currency)
		if err != nil {
			return ModificationResult{}, err
		}
		newROU = mustZero(c.Currency)
	} else {
		newROU, err = rouCarryingAmount.Add(rouAdjustment)
		if err != nil {
			return ModificationResult{}, err
		}
	}
	return ModificationResult{
		EffectiveDate:       effectiveDate.UTC(),
		OldLeaseLiability:   liabilityCarryingAmount,
		RemeasuredLiability: remeasured,
		LiabilityAdjustment: delta,
		OldROUAsset:         rouCarryingAmount,
		NewROUAsset:         newROU,
		ROUAdjustment:       rouAdjustment,
		ModificationGain:    gain,
		DiscountRate:        newAnnualDiscountRate,
		RemainingPayments:   payments,
	}, nil
}

// BuildModificationEntry posts a modification adjustment, including any gain on excess liability reduction.
func BuildModificationEntry(
	result ModificationResult, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if result.RemeasuredLiability.Currency() == "" ||
		accounts.LeaseLiability == uuid.Nil || accounts.ROUAsset == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAccounts
	}
	delta := result.LiabilityAdjustment.Amount()
	if delta.IsZero() {
		return posting.JournalEntry{}, ErrInvalidCarrying
	}
	lines := make([]posting.JournalEntryLine, 0, 3)
	if delta.IsPositive() {
		lines = append(lines,
			debitLine(accounts.ROUAsset, result.ROUAdjustment),
			creditLine(accounts.LeaseLiability, result.LiabilityAdjustment),
		)
	} else {
		reduction, err := types.NewMoney(delta.Abs(), result.LiabilityAdjustment.Currency())
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, debitLine(accounts.LeaseLiability, reduction))
		if result.ROUAdjustment.Amount().IsNegative() {
			rouReduction, err := types.NewMoney(result.ROUAdjustment.Amount().Abs(), result.ROUAdjustment.Currency())
			if err != nil {
				return posting.JournalEntry{}, err
			}
			lines = append(lines, creditLine(accounts.ROUAsset, rouReduction))
		}
		if result.ModificationGain.Amount().IsPositive() {
			if accounts.ModificationGain == uuid.Nil {
				return posting.JournalEntry{}, ErrInvalidAccounts
			}
			lines = append(lines, creditLine(accounts.ModificationGain, result.ModificationGain))
		}
	}
	return newEntry(result.EffectiveDate, companyID, journalID, createdBy, lines)
}

// TerminationResult contains the derecognition amounts and resulting gain or loss.
type TerminationResult struct {
	EffectiveDate       time.Time
	LeaseLiability      types.Money
	ROUAsset            types.Money
	SettlementPayment   types.Money
	TerminationGainLoss types.Money // Positive is a gain; negative is a loss.
}

// EarlyTerminate derecognizes the remaining liability and ROU, recognizing the net settlement gain or loss.
func (c LeaseContract) EarlyTerminate(
	effectiveDate time.Time,
	liabilityCarryingAmount, rouCarryingAmount, settlementPayment types.Money,
) (TerminationResult, error) {
	if effectiveDate.IsZero() || effectiveDate.Before(c.CommencementDate) ||
		!validCarrying(liabilityCarryingAmount, c.Currency) ||
		!validCarrying(rouCarryingAmount, c.Currency) || !validCarrying(settlementPayment, c.Currency) {
		return TerminationResult{}, ErrInvalidCarrying
	}
	gainLoss := liabilityCarryingAmount.Amount().Sub(rouCarryingAmount.Amount()).Sub(settlementPayment.Amount())
	result, err := types.NewMoney(gainLoss, c.Currency)
	if err != nil {
		return TerminationResult{}, err
	}
	return TerminationResult{
		EffectiveDate:       effectiveDate.UTC(),
		LeaseLiability:      liabilityCarryingAmount,
		ROUAsset:            rouCarryingAmount,
		SettlementPayment:   settlementPayment,
		TerminationGainLoss: result,
	}, nil
}

// BuildTerminationEntry posts the derecognition and settlement for an early termination.
func BuildTerminationEntry(
	result TerminationResult, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if accounts.LeaseLiability == uuid.Nil || accounts.ROUAsset == uuid.Nil ||
		result.LeaseLiability.Currency() == "" {
		return posting.JournalEntry{}, ErrInvalidAccounts
	}
	lines := make([]posting.JournalEntryLine, 0, 4)
	lines = append(lines, debitLine(accounts.LeaseLiability, result.LeaseLiability))
	if result.TerminationGainLoss.Amount().IsNegative() {
		if accounts.TerminationLoss == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidAccounts
		}
		loss, err := types.NewMoney(result.TerminationGainLoss.Amount().Abs(), result.TerminationGainLoss.Currency())
		if err != nil {
			return posting.JournalEntry{}, err
		}
		lines = append(lines, debitLine(accounts.TerminationLoss, loss))
	}
	if result.ROUAsset.Amount().IsPositive() {
		lines = append(lines, creditLine(accounts.ROUAsset, result.ROUAsset))
	}
	if result.SettlementPayment.Amount().IsPositive() {
		if accounts.Cash == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidAccounts
		}
		lines = append(lines, creditLine(accounts.Cash, result.SettlementPayment))
	}
	if result.TerminationGainLoss.Amount().IsPositive() {
		if accounts.TerminationGain == uuid.Nil {
			return posting.JournalEntry{}, ErrInvalidAccounts
		}
		lines = append(lines, creditLine(accounts.TerminationGain, result.TerminationGainLoss))
	}
	return newEntry(result.EffectiveDate, companyID, journalID, createdBy, lines)
}

func validateRemainingPayments(date time.Time, payments []LeasePayment, currency string) ([]LeasePayment, error) {
	result := append([]LeasePayment(nil), payments...)
	last := date.UTC()
	for i := range result {
		payment := &result[i]
		if payment.DueDate.IsZero() || !payment.DueDate.After(last) ||
			payment.Amount.Currency() != currency || !payment.Amount.Amount().IsPositive() {
			return nil, ErrInvalidPayment
		}
		payment.DueDate = payment.DueDate.UTC()
		last = payment.DueDate
	}
	return result, nil
}

func validCarrying(amount types.Money, currency string) bool {
	return amount.Currency() == currency && !amount.IsNegative()
}

func debitLine(account uuid.UUID, amount types.Money) posting.JournalEntryLine {
	return posting.JournalEntryLine{AccountID: account, Debit: amount}
}

func creditLine(account uuid.UUID, amount types.Money) posting.JournalEntryLine {
	return posting.JournalEntryLine{AccountID: account, Credit: amount}
}

func newEntry(
	date time.Time, companyID, journalID uuid.UUID, createdBy string, lines []posting.JournalEntryLine,
) (posting.JournalEntry, error) {
	if date.IsZero() {
		return posting.JournalEntry{}, fmt.Errorf("%w: entry date", ErrInvalidContract)
	}
	utcDate := date.UTC()
	entry := posting.JournalEntry{
		CompanyID: companyID, JournalID: journalID, EntryDate: utcDate, Date: utcDate,
		CreatedBy: createdBy, CreatedAt: utcDate, UpdatedAt: utcDate,
		Status: posting.EntryDraft, Classification: posting.EntryClassRegular, Lines: lines,
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}
