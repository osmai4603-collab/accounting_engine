package subledgers

import (
	"errors"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrNotFound        = errors.New("subledger record not found")
	ErrAlreadyExists   = errors.New("subledger record already exists")
	ErrInvalidAmount   = errors.New("invalid subledger amount")
	ErrUnmatchedLine   = errors.New("subledger line is not open")
	ErrInvalidAsset    = errors.New("invalid fixed asset")
	ErrInvalidEvent    = errors.New("invalid economic event")
	ErrMappingNotFound = errors.New("economic event mapping not found")
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

type MemoryRepository struct {
	mu     sync.Mutex
	events map[string]EconomicEvent
	lines  map[uuid.UUID]OpenLine
	assets map[uuid.UUID]FixedAsset
	bank   map[string][]BankItem
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		events: make(map[string]EconomicEvent),
		lines:  make(map[uuid.UUID]OpenLine),
		assets: make(map[uuid.UUID]FixedAsset),
		bank:   make(map[string][]BankItem),
	}
}

func (r *MemoryRepository) SaveEvent(event EconomicEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[event.IdempotencyKey] = event
	return nil
}

func (r *MemoryRepository) GetEventByIdempotencyKey(key string) (EconomicEvent, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[key]
	return event, ok, nil
}

func (r *MemoryRepository) SaveOpenLine(line OpenLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[line.ID] = cloneOpenLine(line)
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

func (r *MemoryRepository) SaveFixedAsset(asset FixedAsset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assets[asset.ID] = asset
	return nil
}

func (r *MemoryRepository) GetFixedAsset(id uuid.UUID) (FixedAsset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	asset, ok := r.assets[id]
	if !ok {
		return FixedAsset{}, ErrNotFound
	}
	return asset, nil
}

func (r *MemoryRepository) SaveBankItem(item BankItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bank[item.AccountID.String()] = append(r.bank[item.AccountID.String()], item)
	return nil
}

func (r *MemoryRepository) ListBankItems(accountID string) ([]BankItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := append([]BankItem(nil), r.bank[accountID]...)
	return items, nil
}

func cloneOpenLine(line OpenLine) OpenLine {
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	return line
}
