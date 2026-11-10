package subledgers

import (
	"context"
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
	repo     EventBridgeRepository
	creator  JournalCreator
	mappings map[eventMappingKey]SubledgerEventMapping
	rules    map[eventMappingKey][]EventAccountingRule
	mu       sync.Mutex
	entries  map[eventIdempotencyKey]posting.JournalEntry
}

type EventBridgeRepository interface {
	EventAccountingRuleRepository
	CompanyEventRepository
	AtomicEconomicEventRepository
}

func NewEventBridge(repo EventBridgeRepository, creator JournalCreator) *EventBridge {
	return &EventBridge{
		repo: repo, creator: creator, mappings: make(map[eventMappingKey]SubledgerEventMapping),
		rules: make(map[eventMappingKey][]EventAccountingRule), entries: make(map[eventIdempotencyKey]posting.JournalEntry),
	}
}

func (b *EventBridge) RegisterMapping(mapping SubledgerEventMapping) error {
	if mapping.CompanyID == uuid.Nil ||
		strings.TrimSpace(mapping.EventType) == "" ||
		mapping.DebitAccountID == uuid.Nil ||
		mapping.CreditAccountID == uuid.Nil ||
		mapping.JournalID == uuid.Nil {
		return ErrMappingNotFound
	}
	rule := EventAccountingRule{
		CompanyID: mapping.CompanyID, EventType: mapping.EventType, RuleName: "default", JournalID: mapping.JournalID,
		Lines: []EventLineTemplate{
			{AccountID: mapping.DebitAccountID, Side: EventLineDebit, AmountFormula: "amount"},
			{AccountID: mapping.CreditAccountID, Side: EventLineCredit, AmountFormula: "amount"},
		},
	}
	if err := b.RegisterRule(rule); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mappings[eventMappingKey{companyID: mapping.CompanyID, eventType: mapping.EventType}] = mapping
	return nil
}

func (b *EventBridge) RegisterRule(rule EventAccountingRule) error {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	if err := rule.Validate(); err != nil {
		return err
	}
	rule.EventType = strings.TrimSpace(rule.EventType)
	rule.RuleName = strings.TrimSpace(rule.RuleName)
	key := eventMappingKey{companyID: rule.CompanyID, eventType: rule.EventType}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.repo.SaveEventAccountingRule(context.Background(), rule); err != nil {
		return err
	}
	rules := b.rules[key]
	for i := range rules {
		if rules[i].Priority == rule.Priority {
			rules[i] = rule.Clone()
			b.rules[key] = rules
			return nil
		}
	}
	b.rules[key] = append(rules, rule.Clone())
	return nil
}

func (b *EventBridge) Process(event EconomicEvent) (posting.JournalEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := event.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	idempotencyKey := eventIdempotencyKey{companyID: event.CompanyID, key: event.IdempotencyKey}
	if entry, ok := b.entries[idempotencyKey]; ok {
		return entry, nil
	}
	var existing subledgerEvent
	storedEvent, found, err := b.repo.GetEventByIdempotencyKeyForCompany(context.Background(), event.CompanyID, event.IdempotencyKey)
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

	rule, err := b.matchingRule(event)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	lines, err := translateEventLines(rule, event)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry := posting.JournalEntry{
		CompanyID:         event.CompanyID,
		ID:                uuid.New(),
		JournalID:         ruleJournalID(rule),
		EntryDate:         event.Date,
		Date:              event.Date,
		Status:            posting.EntryDraft,
		DocumentReference: event.Reference,
		Reference:         event.Reference,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		Lines:             lines,
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	event.ResultEntryID = entry.ID
	if err := b.repo.SaveEventAndDraft(context.Background(), event, entry); err != nil {
		if !errors.Is(err, ErrAlreadyExists) {
			return posting.JournalEntry{}, err
		}
		stored, found, lookupErr := b.repo.GetEventByIdempotencyKeyForCompany(context.Background(), event.CompanyID, event.IdempotencyKey)
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

func (b *EventBridge) matchingRule(event EconomicEvent) (EventAccountingRule, error) {
	key := eventMappingKey{companyID: event.CompanyID, eventType: event.EventType}
	rules := b.rules[key]
	persisted, err := b.repo.ListEventAccountingRulesForCompany(context.Background(), event.CompanyID, event.EventType)
	if err != nil {
		return EventAccountingRule{}, err
	}
	if len(persisted) > 0 {
		rules = persisted
	}
	for _, rule := range rules {
		if ruleMatchesEvent(rule, event) {
			return rule, nil
		}
	}
	return EventAccountingRule{}, ErrMappingNotFound
}

func ruleJournalID(rule EventAccountingRule) uuid.UUID {
	return rule.JournalID
}

type subledgerEvent struct {
	event EconomicEvent
	found bool
	err   error
}
