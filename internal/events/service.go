package events

import (
	"context"
	"errors"
	"sync"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/spi/storage"
)

type EventService struct {
	store          storage.CoreLedgerStore
	router         *Router
	mu             sync.Mutex
	processedKeys  map[string]ledger.JournalEntry
}

func NewEventService(store storage.CoreLedgerStore, router *Router) *EventService {
	return &EventService{
		store:         store,
		router:        router,
		processedKeys: make(map[string]ledger.JournalEntry),
	}
}

func (s *EventService) ProcessEvent(ctx context.Context, event events.AccountingEvent) (ledger.JournalEntry, error) {
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}

	s.mu.Lock()
	if event.IdempotencyKey != "" {
		if existing, ok := s.processedKeys[event.IdempotencyKey]; ok {
			s.mu.Unlock()
			return existing, nil
		}
	}
	s.mu.Unlock()

	rules := s.router.GetRules(event.EventType)
	var rule PostingRule
	if len(rules) > 0 {
		rule = rules[0]
	} else {
		rule = PostingRule{
			EventType:           event.EventType,
			DebitAccountCode:    "1000",
			CreditAccountCode:   "4000",
			DescriptionTemplate: "Default event processing for " + event.EventType,
		}
	}

	entry, err := EvaluateAndConstructEntry(rule, event)
	if err != nil {
		return ledger.JournalEntry{}, err
	}

	if err := s.store.PostEntry(ctx, entry); err != nil {
		return ledger.JournalEntry{}, err
	}

	s.mu.Lock()
	if event.IdempotencyKey != "" {
		s.processedKeys[event.IdempotencyKey] = entry
	}
	s.mu.Unlock()

	return entry, nil
}
