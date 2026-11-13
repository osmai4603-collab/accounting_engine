package ifrs15

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var ErrContractNotFound = errors.New("revenue contract not found")

// UnitConfig binds contract lifecycle events to the general-ledger accounts.
type UnitConfig struct {
	JournalID                uuid.UUID
	CashAccountID            uuid.UUID
	ReceivableAccountID      uuid.UUID
	ContractAssetAccountID   uuid.UUID
	ContractLiabilityAccount uuid.UUID
	RevenueAccountID         uuid.UUID
}

// Unit coordinates contract state and creates balanced journal drafts. Posting
// remains the responsibility of the central posting workflow.
type Unit struct {
	config         UnitConfig
	mu             sync.RWMutex
	contracts      map[uuid.UUID]Contract
	recognizedByPO map[uuid.UUID]map[uuid.UUID]financialtypes.Money
}

func NewUnit(config UnitConfig) (*Unit, error) {
	if config.JournalID == uuid.Nil || config.CashAccountID == uuid.Nil ||
		config.ContractAssetAccountID == uuid.Nil || config.ContractLiabilityAccount == uuid.Nil ||
		config.RevenueAccountID == uuid.Nil {
		return nil, ErrInvalidContract
	}
	return &Unit{
		config: config, contracts: make(map[uuid.UUID]Contract),
		recognizedByPO: make(map[uuid.UUID]map[uuid.UUID]financialtypes.Money),
	}, nil
}

func (u *Unit) AddContract(contract Contract) error {
	if u == nil {
		return ErrInvalidContract
	}
	if err := contract.Validate(); err != nil {
		return err
	}
	if !contract.RecognizedAmount.Amount().IsZero() {
		return ErrInvalidContract
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, exists := u.contracts[contract.ID]; exists {
		return ErrInvalidContract
	}
	u.contracts[contract.ID] = contract
	u.recognizedByPO[contract.ID] = make(map[uuid.UUID]financialtypes.Money, len(contract.Obligations))
	zero, _ := financialtypes.Zero(contract.Currency)
	for _, obligation := range contract.Obligations {
		u.recognizedByPO[contract.ID][obligation.ID] = zero
	}
	return nil
}

func (u *Unit) Contract(id uuid.UUID) (Contract, bool) {
	if u == nil {
		return Contract{}, false
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	contract, ok := u.contracts[id]
	return contract, ok
}

// ReceiveAdvance records customer cash as a contract liability and returns
// the corresponding balanced journal draft.
func (u *Unit) ReceiveAdvance(contractID uuid.UUID, amount financialtypes.Money, date time.Time) (Contract, posting.JournalEntry, error) {
	if u == nil || date.IsZero() {
		return Contract{}, posting.JournalEntry{}, ErrInvalidContract
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	contract, ok := u.contracts[contractID]
	if !ok {
		return Contract{}, posting.JournalEntry{}, ErrContractNotFound
	}
	updated, err := contract.ApplyBilling(amount)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	entry, err := buildAdvanceJournal(u.config, contract, amount, date)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	u.contracts[contractID] = updated
	return updated, entry, nil
}

// BillCustomer records an invoice issued before satisfaction of the related
// performance obligation as a receivable and contract liability.
func (u *Unit) BillCustomer(contractID uuid.UUID, amount financialtypes.Money, date time.Time) (Contract, posting.JournalEntry, error) {
	if u == nil || date.IsZero() || u.config.ReceivableAccountID == uuid.Nil {
		return Contract{}, posting.JournalEntry{}, ErrInvalidContract
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	contract, ok := u.contracts[contractID]
	if !ok {
		return Contract{}, posting.JournalEntry{}, ErrContractNotFound
	}
	updated, err := contract.ApplyBilling(amount)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	customerID := contract.CustomerID
	unbilledEarned := contract.RecognizedAmount.Amount().Sub(contract.BilledAmount.Amount())
	if unbilledEarned.IsNegative() {
		unbilledEarned = decimal.Zero
	}
	assetCredit := decimal.Min(amount.Amount(), unbilledEarned)
	liabilityCredit := amount.Amount().Sub(assetCredit)
	lines := []posting.JournalEntryLine{{AccountID: u.config.ReceivableAccountID, Debit: amount, PartnerID: &customerID}}
	if assetCredit.IsPositive() {
		assetAmount, err := financialtypes.NewMoney(assetCredit, contract.Currency)
		if err != nil {
			return Contract{}, posting.JournalEntry{}, err
		}
		lines = append(lines, posting.JournalEntryLine{AccountID: u.config.ContractAssetAccountID, Credit: assetAmount, PartnerID: &customerID})
	}
	if liabilityCredit.IsPositive() {
		liabilityAmount, err := financialtypes.NewMoney(liabilityCredit, contract.Currency)
		if err != nil {
			return Contract{}, posting.JournalEntry{}, err
		}
		lines = append(lines, posting.JournalEntryLine{AccountID: u.config.ContractLiabilityAccount, Credit: liabilityAmount, PartnerID: &customerID})
	}
	entry, err := posting.NewJournalEntry(u.config.JournalID, date, "ifrs15-billing", lines)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	u.contracts[contractID] = updated
	return updated, entry, nil
}

// CollectReceivable applies customer cash to an invoice receivable. The
// receivable subledger remains responsible for allocation to specific invoices.
func (u *Unit) CollectReceivable(contractID uuid.UUID, amount financialtypes.Money, date time.Time) (posting.JournalEntry, error) {
	if u == nil || date.IsZero() || u.config.ReceivableAccountID == uuid.Nil ||
		amount.Currency() == "" || !amount.Amount().IsPositive() {
		return posting.JournalEntry{}, ErrInvalidContract
	}
	u.mu.RLock()
	contract, ok := u.contracts[contractID]
	u.mu.RUnlock()
	if !ok {
		return posting.JournalEntry{}, ErrContractNotFound
	}
	if amount.Currency() != contract.Currency {
		return posting.JournalEntry{}, ErrInvalidAmount
	}
	customerID := contract.CustomerID
	return posting.NewJournalEntry(u.config.JournalID, date, "ifrs15-receivable-collection", []posting.JournalEntryLine{
		{AccountID: u.config.CashAccountID, Debit: amount, PartnerID: &customerID},
		{AccountID: u.config.ReceivableAccountID, Credit: amount, PartnerID: &customerID},
	})
}

// RecognizeObligation records a milestone or goods-delivery event against a
// specific performance obligation and prevents recognition beyond its
// allocated transaction price.
func (u *Unit) RecognizeObligation(
	contractID, obligationID uuid.UUID,
	amount financialtypes.Money,
	date time.Time,
) (Contract, posting.JournalEntry, error) {
	if u == nil {
		return Contract{}, posting.JournalEntry{}, ErrInvalidContract
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	contract, ok := u.contracts[contractID]
	if !ok {
		return Contract{}, posting.JournalEntry{}, ErrContractNotFound
	}
	allocated, ok := contract.AllocatedPrices[obligationID]
	if !ok {
		return Contract{}, posting.JournalEntry{}, ErrObligationNotFound
	}
	recognized := u.recognizedByPO[contractID][obligationID]
	next, err := recognized.Add(amount)
	if err != nil || next.GreaterThan(allocated) {
		return Contract{}, posting.JournalEntry{}, ErrRecognitionExceeded
	}
	entry, err := BuildRecognitionJournal(contract, amount, u.config.JournalID, date,
		u.config.RevenueAccountID, u.config.ContractLiabilityAccount, u.config.ContractAssetAccountID)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	customerID := contract.CustomerID
	for i := range entry.Lines {
		entry.Lines[i].PartnerID = &customerID
	}
	updated, err := contract.ApplyRecognition(amount)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	u.contracts[contractID] = updated
	u.recognizedByPO[contractID][obligationID] = next
	return updated, entry, nil
}

func (u *Unit) applyRecognitionToObligations(contract Contract, amount financialtypes.Money) {
	remaining := amount
	for _, obligation := range contract.Obligations {
		if !remaining.Amount().IsPositive() {
			return
		}
		recognized := u.recognizedByPO[contract.ID][obligation.ID]
		allocated := contract.AllocatedPrices[obligation.ID]
		available, _ := allocated.Subtract(recognized)
		applied := remaining
		if available.LessThan(applied) {
			applied = available
		}
		updated, _ := recognized.Add(applied)
		u.recognizedByPO[contract.ID][obligation.ID] = updated
		remaining, _ = remaining.Subtract(applied)
	}
}

// Recognize records revenue for a satisfied performance obligation. Previously
// billed consideration is released from deferred revenue; any unbilled earned
// amount is debited to the contract asset.
func (u *Unit) Recognize(contractID uuid.UUID, amount financialtypes.Money, date time.Time) (Contract, posting.JournalEntry, error) {
	if u == nil {
		return Contract{}, posting.JournalEntry{}, ErrInvalidContract
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	contract, ok := u.contracts[contractID]
	if !ok {
		return Contract{}, posting.JournalEntry{}, ErrContractNotFound
	}
	entry, err := BuildRecognitionJournal(contract, amount, u.config.JournalID, date,
		u.config.RevenueAccountID, u.config.ContractLiabilityAccount, u.config.ContractAssetAccountID)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	customerID := contract.CustomerID
	for i := range entry.Lines {
		entry.Lines[i].PartnerID = &customerID
	}
	updated, err := contract.ApplyRecognition(amount)
	if err != nil {
		return Contract{}, posting.JournalEntry{}, err
	}
	u.contracts[contractID] = updated
	u.applyRecognitionToObligations(contract, amount)
	return updated, entry, nil
}

func buildAdvanceJournal(config UnitConfig, contract Contract, amount financialtypes.Money, date time.Time) (posting.JournalEntry, error) {
	if amount.Currency() != contract.Currency || !amount.Amount().IsPositive() {
		return posting.JournalEntry{}, ErrInvalidAmount
	}
	customerID := contract.CustomerID
	debit := posting.JournalEntryLine{AccountID: config.CashAccountID, Debit: amount, PartnerID: &customerID}
	credit := posting.JournalEntryLine{AccountID: config.ContractLiabilityAccount, Credit: amount, PartnerID: &customerID}
	return posting.NewJournalEntry(config.JournalID, date, "ifrs15-advance", []posting.JournalEntryLine{debit, credit})
}
