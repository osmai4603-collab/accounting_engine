package subledgers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
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
	companyID    uuid.UUID
	sourceSystem string
	key          string
}

type EventBridge struct {
	repo      EventBridgeRepository
	creator   JournalCreator
	companyID uuid.UUID
	mappings  map[eventMappingKey]SubledgerEventMapping
	rules     map[eventMappingKey][]EventAccountingRule
	mu        sync.Mutex
	entries   map[eventIdempotencyKey]cachedEconomicEvent
}

type EventBridgeRepository interface {
	EventAccountingRuleRepository
	CompanyEventRepository
	AtomicEconomicEventRepository
}

func NewEventBridge(repo EventBridgeRepository, creator JournalCreator) *EventBridge {
	return &EventBridge{
		repo: repo, creator: creator, mappings: make(map[eventMappingKey]SubledgerEventMapping),
		rules: make(map[eventMappingKey][]EventAccountingRule), entries: make(map[eventIdempotencyKey]cachedEconomicEvent),
	}
}

func NewEventBridgeForCompany(repo EventBridgeRepository, creator JournalCreator, companyID uuid.UUID) *EventBridge {
	bridge := NewEventBridge(repo, creator)
	bridge.companyID = companyID
	return bridge
}

// Deprecated: use RegisterMappingContext.
func (b *EventBridge) RegisterMapping(mapping SubledgerEventMapping) error {
	return b.RegisterMappingContext(context.Background(), mapping)
}

func (b *EventBridge) RegisterMappingContext(ctx context.Context, mapping SubledgerEventMapping) error {
	if b.companyID != uuid.Nil && mapping.CompanyID != b.companyID {
		return ErrCompanyMismatch
	}
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
	if err := b.RegisterRuleContext(ctx, rule); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mappings[eventMappingKey{companyID: mapping.CompanyID, eventType: mapping.EventType}] = mapping
	return nil
}

// Deprecated: use RegisterRuleContext.
func (b *EventBridge) RegisterRule(rule EventAccountingRule) error {
	return b.RegisterRuleContext(context.Background(), rule)
}

func (b *EventBridge) RegisterRuleContext(ctx context.Context, rule EventAccountingRule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	if b.companyID != uuid.Nil && rule.CompanyID != b.companyID {
		return ErrCompanyMismatch
	}
	if err := rule.Validate(); err != nil {
		return err
	}
	rule.EventType = strings.TrimSpace(rule.EventType)
	rule.RuleName = strings.TrimSpace(rule.RuleName)
	key := eventMappingKey{companyID: rule.CompanyID, eventType: rule.EventType}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.repo.SaveEventAccountingRule(ctx, rule); err != nil {
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

// Deprecated: use ProcessContext.
func (b *EventBridge) Process(event EconomicEvent) (posting.JournalEntry, error) {
	return b.ProcessContext(context.Background(), event)
}

func (b *EventBridge) ProcessContext(ctx context.Context, event EconomicEvent) (posting.JournalEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := event.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if b.companyID != uuid.Nil && event.CompanyID != b.companyID {
		return posting.JournalEntry{}, ErrCompanyMismatch
	}
	event.SourceSystem = strings.TrimSpace(event.SourceSystem)
	if event.EventID == uuid.Nil {
		event.EventID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Date.IsZero() {
		event.Date = event.CreatedAt
	}
	if event.Status == "" {
		event.Status = EventReceived
	}
	idempotencyKey := eventIdempotencyKey{companyID: event.CompanyID, sourceSystem: event.SourceSystem, key: event.IdempotencyKey}
	storedEvent, found, err := b.getEventByIdentity(ctx, event.CompanyID, event.SourceSystem, event.IdempotencyKey)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if found {
		if storedEvent.EventID != uuid.Nil {
			event.EventID = storedEvent.EventID
		}
		if storedEvent.ResultEntryID == uuid.Nil && storedEvent.Status == "" {
			return posting.JournalEntry{}, ErrEventResultMissing
		}
		if storedEvent.ResultEntryID != uuid.Nil && storedEvent.PayloadHash == "" {
			return posting.JournalEntry{}, ErrIdempotencyPayloadConflict
		}
	}
	if eventRepository, ok := b.repo.(EventRepository); ok {
		if err := eventRepository.SaveEvent(ctx, event); err != nil && !errors.Is(err, ErrAlreadyExists) {
			return posting.JournalEntry{}, err
		}
	}
	if !found {
		storedEvent, found, err = b.getEventByIdentity(ctx, event.CompanyID, event.SourceSystem, event.IdempotencyKey)
		if err != nil {
			return posting.JournalEntry{}, err
		}
	}
	if found && storedEvent.EventID != uuid.Nil {
		event.EventID = storedEvent.EventID
	}
	event.Status = EventReceived
	event.ErrorMessage = ""
	event.ProcessedAt = nil
	rule, err := b.matchingRuleContext(ctx, event)
	if err != nil {
		b.markEventFailure(ctx, event, err, EventDeadLetter)
		return posting.JournalEntry{}, err
	}
	lines, err := translateEventLines(rule, event)
	if err != nil {
		b.markEventFailure(ctx, event, err, EventDeadLetter)
		return posting.JournalEntry{}, err
	}
	eventID := event.EventID
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
		SourceEventID:     &eventID,
		Lines:             lines,
	}
	if err := entry.Validate(); err != nil {
		b.markEventFailure(ctx, event, err, EventRejected)
		return posting.JournalEntry{}, err
	}
	payloadHash, err := EconomicEventPayloadHash(event, entry)
	if err != nil {
		b.markEventFailure(ctx, event, err, EventRejected)
		return posting.JournalEntry{}, err
	}
	if cached, ok := b.entries[idempotencyKey]; ok {
		if cached.payloadHash != payloadHash {
			return posting.JournalEntry{}, ErrIdempotencyPayloadConflict
		}
		return cached.entry, nil
	}
	if found && storedEvent.ResultEntryID != uuid.Nil {
		if storedEvent.PayloadHash != payloadHash {
			return posting.JournalEntry{}, ErrIdempotencyPayloadConflict
		}
		return posting.JournalEntry{ID: storedEvent.ResultEntryID, EntryDate: storedEvent.Date, Status: posting.EntryDraft}, nil
	}
	event.PayloadHash = payloadHash
	event.ResultEntryID = entry.ID
	eventID = event.EventID
	event.GeneratedEntryID = &entry.ID
	event.Status = EventProcessed
	now := time.Now().UTC()
	event.ProcessedAt = &now
	if err := b.repo.SaveEventAndDraft(ctx, event, entry); err != nil {
		if !errors.Is(err, ErrAlreadyExists) {
			b.markEventFailure(ctx, event, err, EventDeadLetter)
			return posting.JournalEntry{}, err
		}
		stored, found, lookupErr := b.getEventByIdentity(ctx, event.CompanyID, event.SourceSystem, event.IdempotencyKey)
		if lookupErr != nil {
			return posting.JournalEntry{}, lookupErr
		}
		if found && stored.PayloadHash != payloadHash {
			return posting.JournalEntry{}, ErrIdempotencyPayloadConflict
		}
		if !found || stored.ResultEntryID == uuid.Nil {
			return posting.JournalEntry{}, ErrEventResultMissing
		}
		return posting.JournalEntry{ID: stored.ResultEntryID, EntryDate: stored.Date, Status: posting.EntryDraft}, nil
	}
	b.entries[idempotencyKey] = cachedEconomicEvent{entry: entry, payloadHash: payloadHash}
	return entry, nil
}

func (b *EventBridge) getEventByIdentity(ctx context.Context, companyID uuid.UUID, sourceSystem, key string) (EconomicEvent, bool, error) {
	if repository, ok := b.repo.(EventLifecycleRepository); ok {
		return repository.GetEventByIdentity(ctx, companyID, sourceSystem, key)
	}
	return b.repo.GetEventByIdempotencyKeyForCompany(ctx, companyID, key)
}

func (b *EventBridge) markEventFailure(ctx context.Context, event EconomicEvent, cause error, status EventStatus) {
	repository, ok := b.repo.(EventLifecycleRepository)
	if !ok {
		return
	}
	event.Status = status
	event.ErrorMessage = cause.Error()
	event.ProcessedAt = nil
	if err := repository.UpdateEventStatus(ctx, event); err != nil {
		return
	}
}

func (b *EventBridge) ReplayEvent(eventID uuid.UUID) (posting.JournalEntry, error) {
	return b.ReplayEventContext(context.Background(), eventID)
}

func (b *EventBridge) ReplayEventContext(ctx context.Context, eventID uuid.UUID) (posting.JournalEntry, error) {
	if b.companyID == uuid.Nil {
		return posting.JournalEntry{}, ErrCompanyScopeRequired
	}
	return b.ReplayEventForCompanyContext(ctx, b.companyID, eventID)
}

func (b *EventBridge) ReplayEventForCompanyContext(ctx context.Context, companyID, eventID uuid.UUID) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	if companyID == uuid.Nil {
		return posting.JournalEntry{}, ErrCompanyScopeRequired
	}
	if b.companyID != uuid.Nil && b.companyID != companyID {
		return posting.JournalEntry{}, ErrCompanyMismatch
	}
	repository, ok := b.repo.(EventLifecycleRepository)
	if !ok {
		return posting.JournalEntry{}, ErrCompanyScopedEventsRequired
	}
	if eventID == uuid.Nil {
		return posting.JournalEntry{}, ErrInvalidEvent
	}
	event, err := repository.GetEventByID(ctx, companyID, eventID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	if event.Status != EventDeadLetter && event.Status != EventRejected && event.Status != EventReceived && event.Status != "" {
		if event.Status == EventProcessed {
			return b.ProcessContext(ctx, event)
		}
		return posting.JournalEntry{}, ErrEventNotReplayable
	}
	event.Status = EventReceived
	event.ErrorMessage = ""
	event.ProcessedAt = nil
	if err := repository.UpdateEventStatus(ctx, event); err != nil {
		return posting.JournalEntry{}, err
	}
	return b.ProcessContext(ctx, event)
}

func (b *EventBridge) matchingRuleContext(ctx context.Context, event EconomicEvent) (EventAccountingRule, error) {
	key := eventMappingKey{companyID: event.CompanyID, eventType: event.EventType}
	rules := b.rules[key]
	persisted, err := b.repo.ListEventAccountingRulesForCompany(ctx, event.CompanyID, event.EventType)
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

type cachedEconomicEvent struct {
	entry       posting.JournalEntry
	payloadHash string
}
