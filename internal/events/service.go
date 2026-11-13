package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	apiEvents "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// EventService is a legacy/internal helper. It must not guess account mappings.
// Prefer the SDK event ingestion path for durable, idempotent processing.
type EventService struct {
	store         storage.CoreLedgerStore
	router        *Router
	mu            sync.Mutex
	processedKeys map[string]ledger.JournalEntry
	inflight      map[string]struct{}
}

func NewEventService(store storage.CoreLedgerStore, router *Router) *EventService {
	return &EventService{
		store:         store,
		router:        router,
		processedKeys: make(map[string]ledger.JournalEntry),
		inflight:      make(map[string]struct{}),
	}
}

func (s *EventService) ProcessEvent(ctx context.Context, event apiEvents.AccountingEvent) (ledger.JournalEntry, error) {
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	if strings.TrimSpace(event.EventType) == "" {
		return ledger.JournalEntry{}, errors.New("event type is required")
	}
	key := strings.TrimSpace(event.IdempotencyKey)
	if key != "" {
		s.mu.Lock()
		if existing, ok := s.processedKeys[key]; ok {
			s.mu.Unlock()
			return existing, nil
		}
		if _, busy := s.inflight[key]; busy {
			s.mu.Unlock()
			return ledger.JournalEntry{}, errors.New("duplicate idempotency key is being processed")
		}
		s.inflight[key] = struct{}{}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, key)
			s.mu.Unlock()
		}()
	}

	rules := s.router.GetRules(event.EventType)
	if len(rules) == 0 {
		return ledger.JournalEntry{}, fmt.Errorf("no accounting rule registered for event type %q", event.EventType)
	}
	rule := rules[0]

	entry, err := EvaluateAndConstructEntry(rule, event)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	if err := s.store.PostEntry(ctx, entry); err != nil {
		return ledger.JournalEntry{}, err
	}
	if key != "" {
		s.mu.Lock()
		s.processedKeys[key] = entry
		s.mu.Unlock()
	}
	return entry, nil
}
