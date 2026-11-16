package ifrs16

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrPostingCapabilityUnavailable = errors.New("IFRS 16 ledger-posting capability unavailable")
	ErrJournalMappingUnavailable    = errors.New("IFRS 16 journal mapping unavailable")
)

// LeasePostingCapability must be backed by the host application's configured
// ledger-posting capability and resolve the selected real journal mapping.
type LeasePostingCapability interface {
	LedgerPostingAvailable() bool
	ResolveLeaseAccounts(context.Context, uuid.UUID, string) (LeaseAccounts, error)
}

// EntryBuilder only creates drafts after the host confirms posting capability
// and resolves configured accounts for the requested mapping.
type EntryBuilder struct {
	capability LeasePostingCapability
}

func NewEntryBuilder(capability LeasePostingCapability) (*EntryBuilder, error) {
	if capability == nil || !capability.LedgerPostingAvailable() {
		return nil, ErrPostingCapabilityUnavailable
	}
	return &EntryBuilder{capability: capability}, nil
}

func (b *EntryBuilder) resolve(
	ctx context.Context, companyID uuid.UUID, mappingID string,
) (LeaseAccounts, error) {
	if b == nil || b.capability == nil || !b.capability.LedgerPostingAvailable() {
		return LeaseAccounts{}, ErrPostingCapabilityUnavailable
	}
	if strings.TrimSpace(mappingID) == "" {
		return LeaseAccounts{}, ErrJournalMappingUnavailable
	}
	accounts, err := b.capability.ResolveLeaseAccounts(ctx, companyID, mappingID)
	if err != nil {
		return LeaseAccounts{}, err
	}
	return accounts, nil
}

// LeaseAccounts identifies the general-ledger accounts used by lease entries.
type LeaseAccounts struct {
	ROUAsset                uuid.UUID `json:"rou_asset"`
	LeaseLiability          uuid.UUID `json:"lease_liability"`
	Cash                    uuid.UUID `json:"cash"`
	PrepaidLease            uuid.UUID `json:"prepaid_lease"`
	InterestExpense         uuid.UUID `json:"interest_expense"`
	DepreciationExpense     uuid.UUID `json:"depreciation_expense"`
	AccumulatedDepreciation uuid.UUID `json:"accumulated_depreciation"`
	ModificationGain        uuid.UUID `json:"modification_gain"`
	TerminationGain         uuid.UUID `json:"termination_gain"`
	TerminationLoss         uuid.UUID `json:"termination_loss"`
}

func (b *EntryBuilder) BuildCommencementEntry(
	ctx context.Context, contract LeaseContract, companyID, journalID uuid.UUID,
	mappingID, createdBy string,
) (posting.JournalEntry, error) {
	accounts, err := b.resolve(ctx, companyID, mappingID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return buildCommencementEntry(contract, accounts, companyID, journalID, createdBy)
}

func (b *EntryBuilder) BuildPaymentEntry(
	ctx context.Context, period SchedulePeriod, companyID, journalID uuid.UUID,
	mappingID, createdBy string,
) (posting.JournalEntry, error) {
	accounts, err := b.resolve(ctx, companyID, mappingID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return buildPaymentEntry(period, accounts, companyID, journalID, createdBy)
}

func (b *EntryBuilder) BuildDepreciationEntry(
	ctx context.Context, period SchedulePeriod, companyID, journalID uuid.UUID,
	mappingID, createdBy string,
) (posting.JournalEntry, error) {
	accounts, err := b.resolve(ctx, companyID, mappingID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return buildDepreciationEntry(period, accounts, companyID, journalID, createdBy)
}

func (b *EntryBuilder) BuildModificationEntry(
	ctx context.Context, result ModificationResult, companyID, journalID uuid.UUID,
	mappingID, createdBy string,
) (posting.JournalEntry, error) {
	accounts, err := b.resolve(ctx, companyID, mappingID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return buildModificationEntry(result, accounts, companyID, journalID, createdBy)
}

func (b *EntryBuilder) BuildTerminationEntry(
	ctx context.Context, result TerminationResult, companyID, journalID uuid.UUID,
	mappingID, createdBy string,
) (posting.JournalEntry, error) {
	accounts, err := b.resolve(ctx, companyID, mappingID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return buildTerminationEntry(result, accounts, companyID, journalID, createdBy)
}

// BuildCommencementEntry creates a draft for the initial ROU, liability, and commencement cash items.
func BuildCommencementEntry(
	contract LeaseContract, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	return buildCommencementEntry(contract, accounts, companyID, journalID, createdBy)
}

// BuildPaymentEntry records a scheduled payment, including interest and liability reduction.
func BuildPaymentEntry(
	period SchedulePeriod, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	return buildPaymentEntry(period, accounts, companyID, journalID, createdBy)
}

// BuildDepreciationEntry creates a draft for a period's ROU depreciation.
func BuildDepreciationEntry(
	period SchedulePeriod, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	return buildDepreciationEntry(period, accounts, companyID, journalID, createdBy)
}

// BuildModificationEntry creates a draft for a modification adjustment.
func BuildModificationEntry(
	result ModificationResult, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	return buildModificationEntry(result, accounts, companyID, journalID, createdBy)
}

// BuildTerminationEntry creates a draft for derecognition and early-termination settlement.
func BuildTerminationEntry(
	result TerminationResult, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	return buildTerminationEntry(result, accounts, companyID, journalID, createdBy)
}

// buildCommencementEntry creates a draft for the initial ROU, liability, and commencement cash items.
func buildCommencementEntry(
	contract LeaseContract, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if strings.TrimSpace(contract.Reference) == "" {
		return posting.JournalEntry{}, ErrLeaseReferenceRequired
	}
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
	entry, err := newEntry(contract.CommencementDate, companyID, journalID, createdBy, lines)
	return identifyEntry(entry, contract.Reference, "commencement", err)
}

// buildPaymentEntry creates a draft for interest, the liability movement, and cash payment.
func buildPaymentEntry(
	period SchedulePeriod, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if strings.TrimSpace(period.LeaseReference) == "" {
		return posting.JournalEntry{}, ErrLeaseReferenceRequired
	}
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
	entry, err := newEntry(period.DueDate, companyID, journalID, createdBy, lines)
	return identifyEntry(entry, period.LeaseReference, fmt.Sprintf("payment:%d:%s", period.Period, period.DueDate.Format("2006-01-02")), err)
}

// buildDepreciationEntry creates a draft for a period's ROU depreciation.
func buildDepreciationEntry(
	period SchedulePeriod, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if strings.TrimSpace(period.LeaseReference) == "" {
		return posting.JournalEntry{}, ErrLeaseReferenceRequired
	}
	if !period.ROUDepreciation.Amount().IsPositive() {
		return posting.JournalEntry{}, ErrDepreciationUnavail
	}
	if accounts.DepreciationExpense == uuid.Nil || accounts.AccumulatedDepreciation == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidAccounts
	}
	entry, err := newEntry(period.DueDate, companyID, journalID, createdBy, []posting.JournalEntryLine{
		debitLine(accounts.DepreciationExpense, period.ROUDepreciation),
		creditLine(accounts.AccumulatedDepreciation, period.ROUDepreciation),
	})
	return identifyEntry(entry, period.LeaseReference, fmt.Sprintf("depreciation:%d:%s", period.Period, period.DueDate.Format("2006-01-02")), err)
}

// ModificationResult describes remeasurement of the remaining lease at a modification date.
type ModificationResult struct {
	LeaseReference      string
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
	remeasured, err := presentValue(payments, effectiveDate, newAnnualDiscountRate, c.Currency)
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
		LeaseReference:      c.Reference,
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

// buildModificationEntry creates a draft for a modification adjustment.
func buildModificationEntry(
	result ModificationResult, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if strings.TrimSpace(result.LeaseReference) == "" {
		return posting.JournalEntry{}, ErrLeaseReferenceRequired
	}
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
	entry, err := newEntry(result.EffectiveDate, companyID, journalID, createdBy, lines)
	return identifyEntry(entry, result.LeaseReference, "modification:"+result.EffectiveDate.Format("2006-01-02"), err)
}

// TerminationResult contains the derecognition amounts and resulting gain or loss.
type TerminationResult struct {
	LeaseReference      string
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
		LeaseReference:      c.Reference,
		EffectiveDate:       effectiveDate.UTC(),
		LeaseLiability:      liabilityCarryingAmount,
		ROUAsset:            rouCarryingAmount,
		SettlementPayment:   settlementPayment,
		TerminationGainLoss: result,
	}, nil
}

// buildTerminationEntry creates a draft for derecognition and early-termination settlement.
func buildTerminationEntry(
	result TerminationResult, accounts LeaseAccounts, companyID, journalID uuid.UUID, createdBy string,
) (posting.JournalEntry, error) {
	if strings.TrimSpace(result.LeaseReference) == "" {
		return posting.JournalEntry{}, ErrLeaseReferenceRequired
	}
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
	entry, err := newEntry(result.EffectiveDate, companyID, journalID, createdBy, lines)
	return identifyEntry(entry, result.LeaseReference, "termination:"+result.EffectiveDate.Format("2006-01-02"), err)
}

func identifyEntry(
	entry posting.JournalEntry, leaseReference, operation string, err error,
) (posting.JournalEntry, error) {
	if err != nil {
		return posting.JournalEntry{}, err
	}
	keyMaterial := fmt.Sprintf("ifrs16|%s|%s|%s|%s", strings.TrimSpace(leaseReference),
		entry.CompanyID, entry.JournalID, operation)
	keyHash := sha256.Sum256([]byte(keyMaterial))
	key := "ifrs16-" + hex.EncodeToString(keyHash[:])
	entry.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(key))
	entry.SourceModule = "ifrs16"
	entry.Reference = strings.TrimSpace(leaseReference)
	entry.DocumentReference = operation
	entry.IdempotencyKey = key
	payload := fmt.Sprintf("%s|%s|%s|%s|%s", entry.CompanyID, entry.JournalID,
		entry.EntryDate.UTC().Format(time.RFC3339Nano), entry.Reference, operation)
	for _, line := range entry.Lines {
		payload += fmt.Sprintf("|%s:%s:%s:%s:%s", line.AccountID,
			line.Debit.Amount().String(), line.Debit.Currency(),
			line.Credit.Amount().String(), line.Credit.Currency())
	}
	payloadHash := sha256.Sum256([]byte(payload))
	entry.IdempotencyPayloadHash = hex.EncodeToString(payloadHash[:])
	return entry, nil
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
