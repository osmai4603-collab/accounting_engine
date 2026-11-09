package subledgers

import (
	"errors"
	"strings"
	"sync"
	"time"

	"accounting_engine/internal/posting"
	"github.com/google/uuid"
)

type SubledgerEventMapping struct {
	CompanyID       uuid.UUID
	EventType       string
	DebitAccountID  uuid.UUID
	CreditAccountID uuid.UUID
	JournalID       uuid.UUID
}

type JournalCreator interface {
	CreateDraft(posting.JournalEntry) error
}

type eventMappingKey struct {
	companyID uuid.UUID
	eventType string
}

type eventIdempotencyKey struct {
	companyID uuid.UUID
	key       string
}

type EventBridge struct {
	repo     Repository
	creator  JournalCreator
	mappings map[eventMappingKey]SubledgerEventMapping
	mu       sync.Mutex
	entries  map[eventIdempotencyKey]posting.JournalEntry
}

func NewEventBridge(repo Repository, creator JournalCreator) *EventBridge {
	return &EventBridge{repo: repo, creator: creator, mappings: make(map[eventMappingKey]SubledgerEventMapping), entries: make(map[eventIdempotencyKey]posting.JournalEntry)}
}

func (b *EventBridge) RegisterMapping(mapping SubledgerEventMapping) error {
	if mapping.CompanyID == uuid.Nil ||
		strings.TrimSpace(mapping.EventType) == "" ||
		mapping.DebitAccountID == uuid.Nil ||
		mapping.CreditAccountID == uuid.Nil ||
		mapping.JournalID == uuid.Nil {
		return ErrMappingNotFound
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mappings[eventMappingKey{companyID: mapping.CompanyID, eventType: mapping.EventType}] = mapping
	return nil
}

func (b *EventBridge) Process(event EconomicEvent) (posting.JournalEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := event.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	atomicRepository, atomic := b.repo.(AtomicEconomicEventRepository)
	if !atomic {
		return posting.JournalEntry{}, ErrAtomicEventRequired
	}
	idempotencyKey := eventIdempotencyKey{companyID: event.CompanyID, key: event.IdempotencyKey}
	if entry, ok := b.entries[idempotencyKey]; ok {
		return entry, nil
	}
	var existing subledgerEvent
	repository, ok := b.repo.(CompanyEventRepository)
	if !ok {
		return posting.JournalEntry{}, ErrCompanyScopedEventsRequired
	}
	storedEvent, found, err := repository.GetEventByIdempotencyKeyForCompany(event.CompanyID, event.IdempotencyKey)
	existing = subledgerEvent{event: storedEvent, found: found, err: err}
	if existing.err != nil {
		return posting.JournalEntry{}, existing.err
	}
	if existing.found {
		if entry, ok := b.entries[idempotencyKey]; ok {
			return entry, nil
		}
		if existing.event.ResultEntryID == uuid.Nil {
			return posting.JournalEntry{}, ErrEventResultMissing
		}
		return posting.JournalEntry{ID: existing.event.ResultEntryID, EntryDate: existing.event.Date, Status: posting.EntryDraft}, nil
	}

	mapping, ok := b.mappings[eventMappingKey{companyID: event.CompanyID, eventType: event.EventType}]
	if !ok {
		return posting.JournalEntry{}, ErrMappingNotFound
	}
	amount := event.Amount
	entry := posting.JournalEntry{
		CompanyID:         event.CompanyID,
		ID:                uuid.New(),
		JournalID:         mapping.JournalID,
		EntryDate:         event.Date,
		Date:              event.Date,
		Status:            posting.EntryDraft,
		DocumentReference: event.Reference,
		Reference:         event.Reference,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{CompanyID: event.CompanyID, ID: uuid.New(), AccountID: mapping.DebitAccountID, PartnerID: event.PartnerID, Debit: amount},
			{CompanyID: event.CompanyID, ID: uuid.New(), AccountID: mapping.CreditAccountID, PartnerID: event.PartnerID, Credit: amount},
		},
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	event.ResultEntryID = entry.ID
	if err := atomicRepository.SaveEventAndDraft(event, entry); err != nil {
		if !errors.Is(err, ErrAlreadyExists) {
			return posting.JournalEntry{}, err
		}
		stored, found, lookupErr := repository.GetEventByIdempotencyKeyForCompany(event.CompanyID, event.IdempotencyKey)
		if lookupErr != nil {
			return posting.JournalEntry{}, lookupErr
		}
		if !found || stored.ResultEntryID == uuid.Nil {
			return posting.JournalEntry{}, ErrEventResultMissing
		}
		return posting.JournalEntry{ID: stored.ResultEntryID, EntryDate: stored.Date, Status: posting.EntryDraft}, nil
	}
	b.entries[idempotencyKey] = entry
	return entry, nil
}

type subledgerEvent struct {
	event EconomicEvent
	found bool
	err   error
}
