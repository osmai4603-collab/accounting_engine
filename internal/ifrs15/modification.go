package ifrs15

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidModification = errors.New("invalid contract modification")
)

type ModificationType string

const (
	// ModificationSeparateContract is accounted for as a separate contract under IFRS 15.20
	// when additional distinct goods/services are added at standalone selling prices.
	ModificationSeparateContract ModificationType = "separate_contract"

	// ModificationProspective is accounted for prospectively under IFRS 15.21(a)
	// (termination of existing contract and creation of a new contract) when remaining goods/services are distinct.
	ModificationProspective ModificationType = "prospective"

	// ModificationCumulativeCatchUp is accounted for with a cumulative catch-up adjustment under IFRS 15.21(b)
	// when remaining goods/services are not distinct.
	ModificationCumulativeCatchUp ModificationType = "cumulative_catch_up"
)

type ContractModification struct {
	ID                    uuid.UUID               `json:"id"`
	ContractID            uuid.UUID               `json:"contract_id"`
	Type                  ModificationType        `json:"type"`
	Date                  time.Time               `json:"date"`
	Description           string                  `json:"description"`
	AdditionalPrice       financialtypes.Money    `json:"additional_price"`
	AdditionalObligations []PerformanceObligation `json:"additional_obligations,omitempty"`

	// For CumulativeCatchUp:
	UpdatedProgress *decimal.Decimal `json:"updated_progress,omitempty"` // Between 0 and 1
}

// ApplySeparateContractModification handles IFRS 15.20:
// Creates a new separate contract for the additional distinct obligations.
// The existing contract is unchanged and continues on its original terms.
func ApplySeparateContractModification(
	existing Contract,
	mod ContractModification,
	newContractID uuid.UUID,
) (Contract, Contract, error) {
	if err := existing.Validate(); err != nil {
		return Contract{}, Contract{}, err
	}
	if mod.ID == uuid.Nil || newContractID == uuid.Nil || mod.Date.IsZero() {
		return Contract{}, Contract{}, ErrInvalidModification
	}
	if mod.Type != ModificationSeparateContract {
		return Contract{}, Contract{}, fmt.Errorf("%w: expected separate_contract, got %q", ErrInvalidModification, mod.Type)
	}
	if len(mod.AdditionalObligations) == 0 {
		return Contract{}, Contract{}, fmt.Errorf("%w: separate contract requires additional obligations", ErrInvalidObligation)
	}
	if mod.AdditionalPrice.Currency() != existing.Currency || !mod.AdditionalPrice.Amount().IsPositive() {
		return Contract{}, Contract{}, fmt.Errorf("%w: invalid additional price", ErrInvalidAmount)
	}

	newContract, err := NewContract(newContractID, existing.CustomerID, mod.AdditionalPrice, mod.AdditionalObligations)
	if err != nil {
		return Contract{}, Contract{}, fmt.Errorf("create separate contract: %w", err)
	}

	return cloneContract(existing), newContract, nil
}

// ApplyProspectiveModification handles IFRS 15.21(a):
// Remaining distinct goods/services are treated as a termination of the old contract and creation of a new contract.
// Previously recognized revenue remains unchanged. The remaining unearned consideration plus additional price
// is allocated across remaining (unsatisfied/partially satisfied) and new obligations.
func ApplyProspectiveModification(
	existing Contract,
	mod ContractModification,
) (Contract, error) {
	if err := existing.Validate(); err != nil {
		return Contract{}, err
	}
	if mod.ID == uuid.Nil || mod.Date.IsZero() {
		return Contract{}, ErrInvalidModification
	}
	if mod.Type != ModificationProspective {
		return Contract{}, fmt.Errorf("%w: expected prospective modification, got %q", ErrInvalidModification, mod.Type)
	}
	currency := existing.Currency
	if mod.AdditionalPrice.Currency() != currency || mod.AdditionalPrice.IsNegative() {
		return Contract{}, fmt.Errorf("%w: additional price currency mismatch or negative", ErrInvalidAmount)
	}

	// 1. Calculate remaining unrecognized consideration from existing contract
	unrecognizedOld, err := existing.TransactionPrice.Subtract(existing.RecognizedAmount)
	if err != nil {
		return Contract{}, err
	}

	// 2. Total remaining consideration to allocate = UnrecognizedOld + AdditionalPrice
	remainingTotal, err := unrecognizedOld.Add(mod.AdditionalPrice)
	if err != nil {
		return Contract{}, err
	}
	if !remainingTotal.Amount().IsPositive() {
		return Contract{}, fmt.Errorf("%w: remaining consideration must be positive", ErrInvalidAmount)
	}

	// 3. Identify unsatisfied portion of existing obligations
	// Build the set of obligations for remaining allocation:
	// If an existing obligation is already fully recognized, it keeps its recognized amount and gets 0 remaining.
	// For remaining unsatisfied obligations and new obligations, we allocate remainingTotal by relative standalone prices.
	remainingObligations := make([]PerformanceObligation, 0)
	for _, obl := range existing.Obligations {
		allocated := existing.AllocatedPrices[obl.ID]
		recognized := existing.RecognizedPrices[obl.ID]
		if recognized.Amount().IsZero() && existing.RecognizedAmount.Amount().IsZero() {
			// No revenue recognized yet
			remainingObligations = append(remainingObligations, obl)
		} else if allocated.GreaterThan(recognized) {
			// Partially satisfied: standalone price for remaining portion proportional to unsatisfied amount
			unearned, _ := allocated.Subtract(recognized)
			remObl := obl
			remObl.StandalonePrice = unearned
			remainingObligations = append(remainingObligations, remObl)
		}
	}
	// Add newly promised obligations
	for _, obl := range mod.AdditionalObligations {
		if obl.StandalonePrice.Currency() != currency || !obl.StandalonePrice.Amount().IsPositive() {
			return Contract{}, fmt.Errorf("%w: invalid standalone price for added obligation %s", ErrInvalidObligation, obl.ID)
		}
		remainingObligations = append(remainingObligations, obl)
	}

	if len(remainingObligations) == 0 {
		return Contract{}, fmt.Errorf("%w: no remaining unsatisfied obligations to allocate", ErrInvalidObligation)
	}

	// 4. Allocate remainingTotal across remaining obligations
	allocatedRemaining, err := AllocateTransactionPrice(remainingTotal, remainingObligations)
	if err != nil {
		return Contract{}, fmt.Errorf("allocate remaining consideration: %w", err)
	}

	// 5. Build updated obligations list and updated allocated prices for the modified contract
	allObligations := append([]PerformanceObligation(nil), existing.Obligations...)
	allObligations = append(allObligations, mod.AdditionalObligations...)

	newAllocatedPrices := make(map[uuid.UUID]financialtypes.Money, len(allObligations))
	newRecognizedPrices := make(map[uuid.UUID]financialtypes.Money, len(allObligations))
	zero, _ := financialtypes.Zero(currency)

	for _, obl := range existing.Obligations {
		recognized := existing.RecognizedPrices[obl.ID]
		newRecognizedPrices[obl.ID] = recognized
		remAllocated, hasRem := allocatedRemaining[obl.ID]
		if hasRem {
			totalForObl, addErr := recognized.Add(remAllocated)
			if addErr != nil {
				return Contract{}, addErr
			}
			newAllocatedPrices[obl.ID] = totalForObl
		} else {
			newAllocatedPrices[obl.ID] = recognized
		}
	}
	for _, obl := range mod.AdditionalObligations {
		newRecognizedPrices[obl.ID] = zero
		remAllocated, hasRem := allocatedRemaining[obl.ID]
		if !hasRem {
			return Contract{}, fmt.Errorf("%w: missing allocation for added obligation %s", ErrInvalidContract, obl.ID)
		}
		newAllocatedPrices[obl.ID] = remAllocated
	}

	newTransactionPrice, err := existing.RecognizedAmount.Add(remainingTotal)
	if err != nil {
		return Contract{}, err
	}

	updated := Contract{
		ID:               existing.ID,
		CustomerID:       existing.CustomerID,
		Currency:         currency,
		TransactionPrice: newTransactionPrice,
		Obligations:      allObligations,
		AllocatedPrices:  newAllocatedPrices,
		BilledAmount:     existing.BilledAmount,
		RecognizedAmount: existing.RecognizedAmount,
		Status:           ContractStatusModified,
		RecognizedPrices: newRecognizedPrices,
	}

	if err := updated.Validate(); err != nil {
		return Contract{}, fmt.Errorf("validate modified contract: %w", err)
	}

	return updated, nil
}

// ApplyCumulativeCatchUpModification handles IFRS 15.21(b):
// Remaining goods/services are not distinct (e.g. single construction/over-time project).
// The transaction price is updated, cumulative revenue to date is recalculated based on measure of progress,
// and a catch-up journal entry is generated.
func ApplyCumulativeCatchUpModification(
	existing Contract,
	mod ContractModification,
	journalID uuid.UUID,
	revenueAccountID, contractAssetAccountID, contractLiabilityAccountID uuid.UUID,
) (Contract, posting.JournalEntry, error) {
	if err := existing.Validate(); err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	if mod.ID == uuid.Nil || mod.Date.IsZero() || mod.UpdatedProgress == nil {
		return Contract{}, posting.JournalEntry{}, ErrInvalidModification
	}
	if mod.Type != ModificationCumulativeCatchUp {
		return Contract{}, posting.JournalEntry{}, fmt.Errorf("%w: expected cumulative_catch_up, got %q", ErrInvalidModification, mod.Type)
	}
	progress := *mod.UpdatedProgress
	if progress.IsNegative() || progress.GreaterThan(decimal.NewFromInt(1)) {
		return Contract{}, posting.JournalEntry{}, fmt.Errorf("%w: progress measure must be between 0 and 1", ErrInvalidModification)
	}
	currency := existing.Currency
	if mod.AdditionalPrice.Currency() != currency {
		return Contract{}, posting.JournalEntry{}, fmt.Errorf("%w: additional price currency mismatch", ErrInvalidAmount)
	}

	newTransactionPrice, err := existing.TransactionPrice.Add(mod.AdditionalPrice)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	if !newTransactionPrice.Amount().IsPositive() {
		return Contract{}, posting.JournalEntry{}, fmt.Errorf("%w: updated transaction price must be positive", ErrInvalidAmount)
	}

	// Cumulative revenue that should have been recognized to date = NewTransactionPrice * Progress
	minorUnits := financialtypes.CurrencyMinorUnit(currency)
	cumulativeRevenueDec := newTransactionPrice.Amount().Mul(progress).RoundBank(minorUnits)
	cumulativeRevenue, err := financialtypes.NewMoney(cumulativeRevenueDec, currency)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}

	catchUpAdjustment, err := cumulativeRevenue.Subtract(existing.RecognizedAmount)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}

	var entry posting.JournalEntry
	if catchUpAdjustment.Amount().IsPositive() {
		// Cumulative catch-up increase in revenue:
		// Dr Contract Asset, Cr Revenue
		customerID := existing.CustomerID
		entry, err = posting.NewJournalEntry(journalID, mod.Date, "ifrs15-catch-up-adjustment", []posting.JournalEntryLine{
			{AccountID: contractAssetAccountID, Debit: catchUpAdjustment, PartnerID: &customerID},
			{AccountID: revenueAccountID, Credit: catchUpAdjustment, PartnerID: &customerID},
		})
		if err != nil {
			return Contract{}, posting.JournalEntry{}, err
		}
	} else if catchUpAdjustment.Amount().IsNegative() {
		// Cumulative catch-up reduction in revenue:
		// Dr Revenue, Cr Contract Liability
		adjustmentAbs, _ := catchUpAdjustment.Multiply(decimal.NewFromInt(-1))
		customerID := existing.CustomerID
		entry, err = posting.NewJournalEntry(journalID, mod.Date, "ifrs15-catch-up-adjustment", []posting.JournalEntryLine{
			{AccountID: revenueAccountID, Debit: adjustmentAbs, PartnerID: &customerID},
			{AccountID: contractLiabilityAccountID, Credit: adjustmentAbs, PartnerID: &customerID},
		})
		if err != nil {
			return Contract{}, posting.JournalEntry{}, err
		}
	}

	// Update allocations proportionally
	newAllocations, err := AllocateTransactionPrice(newTransactionPrice, existing.Obligations)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}

	updated := cloneContract(existing)
	updated.TransactionPrice = newTransactionPrice
	updated.AllocatedPrices = newAllocations
	updated.RecognizedAmount = cumulativeRevenue
	updated.Status = ContractStatusModified

	// Update per-obligation recognition
	if len(updated.Obligations) == 1 {
		updated.RecognizedPrices[updated.Obligations[0].ID] = cumulativeRevenue
	}

	if err := updated.Validate(); err != nil {
		return Contract{}, posting.JournalEntry{}, fmt.Errorf("validate catch-up contract: %w", err)
	}

	return updated, entry, nil
}

