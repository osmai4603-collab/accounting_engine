package subledgers

import (
	"strings"
	"sync"
	"time"

	"accounting_engine/internal/posting"
	"github.com/google/uuid"
)

type SubledgerEventMapping struct {
	EventType       string
	DebitAccountID  uuid.UUID
	CreditAccountID uuid.UUID
	JournalID       uuid.UUID
}

type JournalCreator interface {
	CreateDraft(posting.JournalEntry) error
}

type EventBridge struct {
	repo     Repository
	creator  JournalCreator
	mappings map[string]SubledgerEventMapping
	mu       sync.Mutex
	entries  map[string]posting.JournalEntry
}

func NewEventBridge(repo Repository, creator JournalCreator) *EventBridge {
	return &EventBridge{repo: repo, creator: creator, mappings: make(map[string]SubledgerEventMapping), entries: make(map[string]posting.JournalEntry)}
}

func (b *EventBridge) RegisterMapping(mapping SubledgerEventMapping) error {
	if strings.TrimSpace(mapping.EventType) == "" ||
		mapping.DebitAccountID == uuid.Nil ||
		mapping.CreditAccountID == uuid.Nil ||
		mapping.JournalID == uuid.Nil {
		return ErrMappingNotFound
	}
	b.mappings[mapping.EventType] = mapping
	return nil
}

func (b *EventBridge) Process(event EconomicEvent) (posting.JournalEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := event.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if entry, ok := b.entries[event.IdempotencyKey]; ok {
		return entry, nil
	}
	if existing, ok, err := b.repo.GetEventByIdempotencyKey(event.IdempotencyKey); err != nil {
		return posting.JournalEntry{}, err
	} else if ok {
		if entry, ok := b.entries[existing.IdempotencyKey]; ok {
			return entry, nil
		}
		if existing.ResultEntryID == uuid.Nil {
			return posting.JournalEntry{}, nil
		}
		return posting.JournalEntry{ID: existing.ResultEntryID, EntryDate: existing.Date, Status: posting.EntryDraft}, nil
	}

	mapping, ok := b.mappings[event.EventType]
	if !ok {
		return posting.JournalEntry{}, ErrMappingNotFound
	}
	amount := event.Amount
	entry := posting.JournalEntry{
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
			{ID: uuid.New(), AccountID: mapping.DebitAccountID, PartnerID: event.PartnerID, Debit: amount},
			{ID: uuid.New(), AccountID: mapping.CreditAccountID, PartnerID: event.PartnerID, Credit: amount},
		},
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if b.creator != nil {
		if err := b.creator.CreateDraft(entry); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	event.ResultEntryID = entry.ID
	if err := b.repo.SaveEvent(event); err != nil {
		return posting.JournalEntry{}, err
	}
	b.entries[event.IdempotencyKey] = entry
	return entry, nil
}
