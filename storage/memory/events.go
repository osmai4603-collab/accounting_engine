package memory

import (
	"context"
	"sort"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"

	"github.com/google/uuid"
)

func (d *Driver) SaveEvent(ctx context.Context, event subledgers.EconomicEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	event.SourceSystem = strings.TrimSpace(event.SourceSystem)
	if event.EventID == uuid.Nil {
		event.EventID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	key := memoryEventKey(event.CompanyID, event.SourceSystem, event.IdempotencyKey)
	if existing, ok := d.events[key]; ok {
		if existing.ResultEntryID != event.ResultEntryID || existing.EventType != event.EventType ||
			existing.Reference != event.Reference || !existing.Amount.Equal(event.Amount) ||
			existing.PayloadHash != event.PayloadHash || string(existing.PayloadJSON) != string(event.PayloadJSON) {
			return subledgers.ErrAlreadyExists
		}
		return nil
	}
	d.events[key] = cloneMemoryEvent(event)
	return nil
}

func (d *Driver) SaveEventAccountingRule(ctx context.Context, rule subledgers.EventAccountingRule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rule.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	key := rule.CompanyID.String() + "\x00" + rule.EventType
	rules := d.eventRules[key]
	for i := range rules {
		if rules[i].Priority == rule.Priority {
			rules[i] = rule.Clone()
			d.eventRules[key] = rules
			return nil
		}
	}
	d.eventRules[key] = append(rules, rule.Clone())
	sort.Slice(d.eventRules[key], func(i, j int) bool {
		return d.eventRules[key][i].Priority > d.eventRules[key][j].Priority
	})
	return nil
}

func (d *Driver) ListEventAccountingRulesForCompany(ctx context.Context, companyID uuid.UUID, eventType string) ([]subledgers.EventAccountingRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	rules := d.eventRules[companyID.String()+"\x00"+eventType]
	result := make([]subledgers.EventAccountingRule, len(rules))
	for i := range rules {
		result[i] = rules[i].Clone()
	}
	return result, nil
}

func (d *Driver) SaveEventAndDraft(ctx context.Context, event subledgers.EconomicEvent, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return err
	}
	event.SourceSystem = strings.TrimSpace(event.SourceSystem)
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if event.EventID == uuid.Nil {
		event.EventID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	key := memoryEventKey(event.CompanyID, event.SourceSystem, event.IdempotencyKey)
	if existing, exists := d.events[key]; exists && existing.ResultEntryID != uuid.Nil {
		return subledgers.ErrAlreadyExists
	}
	if _, exists := d.entries[entry.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	eventID := event.EventID
	if entry.SourceEventID == nil {
		entry.SourceEventID = &eventID
	}
	if *entry.SourceEventID != event.EventID {
		return posting.ErrInvalidEntry
	}
	for i := range entry.Lines {
		if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != entry.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[entry.Lines[i].AccountID]
		if !ok || account.CompanyID != entry.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		entry.Lines[i].CompanyID = entry.CompanyID
	}
	entryID := entry.ID
	if event.GeneratedEntryID != nil && *event.GeneratedEntryID != entry.ID {
		return posting.ErrInvalidEntry
	}
	event.ResultEntryID = entry.ID
	event.GeneratedEntryID = &entryID
	event.Status = subledgers.EventProcessed
	if event.ProcessedAt == nil {
		now := time.Now().UTC()
		event.ProcessedAt = &now
	}
	d.entries[entry.ID] = cloneEntry(entry)
	d.events[key] = cloneMemoryEvent(event)
	return nil
}

func (d *Driver) GetEventByIdempotencyKey(ctx context.Context, key string) (subledgers.EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.EconomicEvent{}, false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	var event subledgers.EconomicEvent
	var ok bool
	for _, current := range d.events {
		if current.IdempotencyKey == key {
			if ok {
				return subledgers.EconomicEvent{}, false, subledgers.ErrInvalidEvent
			}
			event, ok = current, true
		}
	}
	return event, ok, nil
}

func (d *Driver) GetEventByIdempotencyKeyForCompany(ctx context.Context, companyID uuid.UUID, key string) (subledgers.EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.EconomicEvent{}, false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if event, ok := d.events[memoryEventKey(companyID, "", key)]; ok {
		return cloneMemoryEvent(event), true, nil
	}
	var result subledgers.EconomicEvent
	found := false
	for _, event := range d.events {
		if event.CompanyID == companyID && event.IdempotencyKey == key {
			if found {
				return subledgers.EconomicEvent{}, false, subledgers.ErrInvalidEvent
			}
			result, found = event, true
		}
	}
	return cloneMemoryEvent(result), found, nil
}

func (d *Driver) GetEventByIdentity(ctx context.Context, companyID uuid.UUID, sourceSystem, key string) (subledgers.EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.EconomicEvent{}, false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	event, ok := d.events[memoryEventKey(companyID, sourceSystem, key)]
	return cloneMemoryEvent(event), ok, nil
}

func (d *Driver) GetEventByID(ctx context.Context, companyID, eventID uuid.UUID) (subledgers.EconomicEvent, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.EconomicEvent{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, event := range d.events {
		if event.CompanyID == companyID && event.EventID == eventID {
			return cloneMemoryEvent(event), nil
		}
	}
	return subledgers.EconomicEvent{}, subledgers.ErrNotFound
}

func (d *Driver) UpdateEventStatus(ctx context.Context, event subledgers.EconomicEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := memoryEventKey(event.CompanyID, event.SourceSystem, event.IdempotencyKey)
	current, ok := d.events[key]
	if !ok || current.EventID != event.EventID {
		return subledgers.ErrNotFound
	}
	d.events[key] = cloneMemoryEvent(event)
	return nil
}

func memoryEventKey(companyID uuid.UUID, sourceSystem, key string) string {
	return companyID.String() + "\x00" + sourceSystem + "\x00" + key
}

func cloneMemoryEvent(event subledgers.EconomicEvent) subledgers.EconomicEvent {
	event.PayloadJSON = append([]byte(nil), event.PayloadJSON...)
	if event.GeneratedEntryID != nil {
		id := *event.GeneratedEntryID
		event.GeneratedEntryID = &id
	}
	if event.ProcessedAt != nil {
		processedAt := *event.ProcessedAt
		event.ProcessedAt = &processedAt
	}
	return event
}
