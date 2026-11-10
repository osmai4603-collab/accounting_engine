package memory

import (
	"context"
	"sort"

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
	key := event.CompanyID.String() + "\x00" + event.IdempotencyKey
	if existing, ok := d.events[key]; ok {
		if existing.ResultEntryID != event.ResultEntryID || existing.EventType != event.EventType ||
			existing.Reference != event.Reference || !existing.Amount.Equal(event.Amount) {
			return subledgers.ErrAlreadyExists
		}
		return nil
	}
	d.events[key] = event
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
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := event.CompanyID.String() + "\x00" + event.IdempotencyKey
	if _, exists := d.events[key]; exists {
		return subledgers.ErrAlreadyExists
	}
	if _, exists := d.entries[entry.ID]; exists {
		return subledgers.ErrAlreadyExists
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
	d.entries[entry.ID] = cloneEntry(entry)
	d.events[key] = event
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
	event, ok := d.events[companyID.String()+"\x00"+key]
	return event, ok, nil
}
