package subledgers

import (
	"errors"
	"reflect"
	"sync"

	"accounting_engine/internal/posting"
	"github.com/google/uuid"
)

var (
	ErrNotFound                         = errors.New("subledger record not found")
	ErrAlreadyExists                    = errors.New("subledger record already exists")
	ErrInvalidAmount                    = errors.New("invalid subledger amount")
	ErrUnmatchedLine                    = errors.New("subledger line is not open")
	ErrInvalidAsset                     = errors.New("invalid fixed asset")
	ErrInvalidEvent                     = errors.New("invalid economic event")
	ErrMappingNotFound                  = errors.New("economic event mapping not found")
	ErrCompanyMismatch                  = errors.New("subledger records belong to different companies")
	ErrAtomicMatchRequired              = errors.New("atomic open-line updates are required for reconciliation")
	ErrAtomicFixedAssetRequired         = errors.New("atomic fixed-asset and journal-draft storage is required")
	ErrConcurrentAssetUpdate            = errors.New("fixed asset changed during processing")
	ErrConcurrentMatch                  = errors.New("open line changed during reconciliation")
	ErrConcurrentBankItem               = errors.New("bank item changed during reconciliation")
	ErrInvalidOpenLine                  = errors.New("invalid open line")
	ErrCompanyScopedOpenLinesRequired   = errors.New("company-scoped open-line storage is required")
	ErrCompanyScopedBankItemsRequired   = errors.New("company-scoped bank-item storage is required")
	ErrAtomicBankReconciliationRequired = errors.New("atomic bank-item updates are required for reconciliation")
	ErrAtomicEventRequired              = errors.New("atomic event and journal-draft storage is required")
	ErrEventResultMissing               = errors.New("stored economic event has no result entry")
	ErrCompanyScopedEventsRequired      = errors.New("company-scoped event storage is required")
)

type Repository interface {
	SaveEvent(EconomicEvent) error
	GetEventByIdempotencyKey(string) (EconomicEvent, bool, error)

	SaveOpenLine(OpenLine) error
	GetOpenLine(uuid.UUID) (OpenLine, error)
	ListOpenLines(string) ([]OpenLine, error)

	SaveFixedAsset(FixedAsset) error
	GetFixedAsset(uuid.UUID) (FixedAsset, error)

	SaveBankItem(BankItem) error
	ListBankItems(string) ([]BankItem, error)
}

func (r *MemoryRepository) SaveEventAndDraft(event EconomicEvent, entry posting.JournalEntry) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := companyEventKey(event.CompanyID, event.IdempotencyKey)
	if _, exists := r.events[key]; exists {
		return ErrAlreadyExists
	}
	if _, exists := r.drafts[entry.ID]; exists {
		return ErrAlreadyExists
	}
	r.events[key] = event
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	r.drafts[entry.ID] = entry
	return nil
}

type CompanyEventRepository interface {
	GetEventByIdempotencyKeyForCompany(uuid.UUID, string) (EconomicEvent, bool, error)
}

type CompanyOpenLineRepository interface {
	ListOpenLinesForCompany(uuid.UUID, string) ([]OpenLine, error)
}

type CompanyBankItemRepository interface {
	ListBankItemsForCompany(uuid.UUID, string) ([]BankItem, error)
}

type ConditionalBankItemRepository interface {
	CompareAndSaveBankItems(expected, updated []BankItem) error
}

type AtomicOpenLineRepository interface {
	SaveOpenLines([]OpenLine) error
}

type ConditionalOpenLineRepository interface {
	CompareAndSaveOpenLines(expected, updated []OpenLine) error
}

type AtomicEconomicEventRepository interface {
	SaveEventAndDraft(EconomicEvent, posting.JournalEntry) error
}

type AtomicFixedAssetRepository interface {
	SaveFixedAssetAndDraft(expected, updated FixedAsset, entry posting.JournalEntry) error
}

type MemoryRepository struct {
	mu     sync.Mutex
	events map[string]EconomicEvent
	drafts map[uuid.UUID]posting.JournalEntry
	lines  map[uuid.UUID]OpenLine
	assets map[uuid.UUID]FixedAsset
	bank   map[string][]BankItem
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		events: make(map[string]EconomicEvent),
		drafts: make(map[uuid.UUID]posting.JournalEntry),
		lines:  make(map[uuid.UUID]OpenLine),
		assets: make(map[uuid.UUID]FixedAsset),
		bank:   make(map[string][]BankItem),
	}
}

func (r *MemoryRepository) SaveEvent(event EconomicEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := companyEventKey(event.CompanyID, event.IdempotencyKey)
	if existing, ok := r.events[key]; ok {
		if existing.ResultEntryID != event.ResultEntryID || existing.EventType != event.EventType ||
			existing.Reference != event.Reference || !existing.Amount.Equal(event.Amount) {
			return ErrAlreadyExists
		}
		return nil
	}
	r.events[key] = event
	return nil
}

func (r *MemoryRepository) GetEventByIdempotencyKey(key string) (EconomicEvent, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var event EconomicEvent
	var ok bool
	for _, current := range r.events {
		if current.IdempotencyKey == key {
			if ok {
				return EconomicEvent{}, false, ErrInvalidEvent
			}
			event, ok = current, true
		}
	}
	return event, ok, nil
}

func (r *MemoryRepository) GetEventByIdempotencyKeyForCompany(companyID uuid.UUID, key string) (EconomicEvent, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[companyEventKey(companyID, key)]
	return event, ok, nil
}

func companyEventKey(companyID uuid.UUID, key string) string {
	return companyID.String() + "\x00" + key
}

func (r *MemoryRepository) SaveOpenLine(line OpenLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
		return ErrCompanyMismatch
	}
	r.lines[line.ID] = cloneOpenLine(line)
	return nil
}

func (r *MemoryRepository) SaveOpenLines(lines []OpenLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range lines {
		if line.ID == uuid.Nil {
			return ErrInvalidEvent
		}
		if existing, ok := r.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
			return ErrCompanyMismatch
		}
	}
	for _, line := range lines {
		r.lines[line.ID] = cloneOpenLine(line)
	}
	return nil
}

func (r *MemoryRepository) CompareAndSaveOpenLines(expected, updated []OpenLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(expected) != len(updated) {
		return ErrInvalidOpenLine
	}
	for i, oldLine := range expected {
		current, ok := r.lines[oldLine.ID]
		if !ok {
			return ErrNotFound
		}
		if current.CompanyID != oldLine.CompanyID || updated[i].ID != oldLine.ID ||
			updated[i].CompanyID != oldLine.CompanyID {
			return ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			return ErrConcurrentMatch
		}
	}
	for _, line := range updated {
		r.lines[line.ID] = cloneOpenLine(line)
	}
	return nil
}

func (r *MemoryRepository) GetOpenLine(id uuid.UUID) (OpenLine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line, ok := r.lines[id]
	if !ok {
		return OpenLine{}, ErrNotFound
	}
	return cloneOpenLine(line), nil
}

func (r *MemoryRepository) ListOpenLines(partnerID string) ([]OpenLine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := make([]OpenLine, 0)
	for _, line := range r.lines {
		if partnerID == "" || linePartner(line) == partnerID {
			lines = append(lines, cloneOpenLine(line))
		}
	}
	return lines, nil
}

func (r *MemoryRepository) ListOpenLinesForCompany(companyID uuid.UUID, partnerID string) ([]OpenLine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := make([]OpenLine, 0)
	for _, line := range r.lines {
		if line.CompanyID != companyID || (partnerID != "" && linePartner(line) != partnerID) {
			continue
		}
		lines = append(lines, cloneOpenLine(line))
	}
	return lines, nil
}

func (r *MemoryRepository) SaveFixedAsset(asset FixedAsset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.assets[asset.ID]; ok && existing.CompanyID != asset.CompanyID {
		return ErrCompanyMismatch
	}
	r.assets[asset.ID] = cloneFixedAsset(asset)
	return nil
}

func (r *MemoryRepository) SaveFixedAssetAndDraft(expected, updated FixedAsset, entry posting.JournalEntry) error {
	if updated.ID == uuid.Nil || updated.CompanyID == uuid.Nil || expected.ID != updated.ID ||
		expected.CompanyID != updated.CompanyID || entry.ID == uuid.Nil ||
		entry.CompanyID != updated.CompanyID || entry.Status != posting.EntryDraft {
		return ErrInvalidAsset
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	currentAsset, exists := r.assets[expected.ID]
	if !exists {
		return ErrNotFound
	}
	if !reflect.DeepEqual(currentAsset, expected) {
		return ErrConcurrentAssetUpdate
	}
	if current, exists := r.drafts[entry.ID]; exists && current.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	r.drafts[entry.ID] = entry
	r.assets[updated.ID] = cloneFixedAsset(updated)
	return nil
}

func (r *MemoryRepository) GetFixedAsset(id uuid.UUID) (FixedAsset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	asset, ok := r.assets[id]
	if !ok {
		return FixedAsset{}, ErrNotFound
	}
	return cloneFixedAsset(asset), nil
}

func (r *MemoryRepository) ListFixedAssets() ([]FixedAsset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	assets := make([]FixedAsset, 0, len(r.assets))
	for _, asset := range r.assets {
		assets = append(assets, cloneFixedAsset(asset))
	}
	return assets, nil
}

func (r *MemoryRepository) SaveBankItem(item BankItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, current := range r.bank[item.AccountID.String()] {
		if current.ID == item.ID {
			if current.CompanyID != item.CompanyID {
				return ErrCompanyMismatch
			}
			return ErrAlreadyExists
		}
	}
	r.bank[item.AccountID.String()] = append(r.bank[item.AccountID.String()], item)
	return nil
}

func (r *MemoryRepository) ListBankItems(accountID string) ([]BankItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := append([]BankItem(nil), r.bank[accountID]...)
	return items, nil
}

func (r *MemoryRepository) ListBankItemsForCompany(companyID uuid.UUID, accountID string) ([]BankItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]BankItem, 0)
	for _, item := range r.bank[accountID] {
		if item.CompanyID == companyID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (r *MemoryRepository) CompareAndSaveBankItems(expected, updated []BankItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(expected) != len(updated) {
		return ErrInvalidOpenLine
	}
	for i, oldItem := range expected {
		if updated[i].ID != oldItem.ID || updated[i].CompanyID != oldItem.CompanyID ||
			updated[i].AccountID != oldItem.AccountID {
			return ErrCompanyMismatch
		}
		items := r.bank[oldItem.AccountID.String()]
		found := false
		for _, current := range items {
			if current.ID != oldItem.ID {
				continue
			}
			found = true
			if !reflect.DeepEqual(current, oldItem) {
				return ErrConcurrentBankItem
			}
			break
		}
		if !found {
			return ErrNotFound
		}
	}
	for _, updatedItem := range updated {
		items := r.bank[updatedItem.AccountID.String()]
		for i := range items {
			if items[i].ID == updatedItem.ID {
				items[i] = updatedItem
				break
			}
		}
		r.bank[updatedItem.AccountID.String()] = items
	}
	return nil
}

func cloneOpenLine(line OpenLine) OpenLine {
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]OpenLineMatch(nil), line.Matches...)
	return line
}

func cloneFixedAsset(asset FixedAsset) FixedAsset {
	if asset.DisposedAt != nil {
		disposedAt := *asset.DisposedAt
		asset.DisposedAt = &disposedAt
	}
	return asset
}
