package subledgers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"accounting_engine/internal/masterdata"
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
	ErrInvalidBankAccount               = errors.New("invalid bank account")
	ErrMappingNotFound                  = errors.New("economic event mapping not found")
	ErrCompanyMismatch                  = errors.New("subledger records belong to different companies")
	ErrAtomicMatchRequired              = errors.New("atomic open-line updates are required for reconciliation")
	ErrAtomicFixedAssetRequired         = errors.New("atomic fixed-asset and journal-draft storage is required")
	ErrFixedAssetConfirmationRequired   = errors.New("atomic fixed-asset posting confirmation is required")
	ErrConcurrentAssetUpdate            = errors.New("fixed asset changed during processing")
	ErrConcurrentMatch                  = errors.New("open line changed during reconciliation")
	ErrConcurrentBankItem               = errors.New("bank item changed during reconciliation")
	ErrInvalidOpenLine                  = errors.New("invalid open line")
	ErrCompanyScopedOpenLinesRequired   = errors.New("company-scoped open-line storage is required")
	ErrCompanyScopedBankItemsRequired   = errors.New("company-scoped bank-item storage is required")
	ErrAtomicBankReconciliationRequired = errors.New("atomic bank-item updates are required for reconciliation")
	ErrAmbiguousBankReconciliation      = errors.New("multiple ledger groups match the bank statement")
	ErrBankReconciliationTooComplex     = errors.New("bank reconciliation candidate set is too large")
	ErrAtomicEventRequired              = errors.New("atomic event and journal-draft storage is required")
	ErrEventResultMissing               = errors.New("stored economic event has no result entry")
	ErrEventNotReplayable               = errors.New("economic event is not eligible for replay")
	ErrCompanyScopedEventsRequired      = errors.New("company-scoped event storage is required")
	ErrEventRulesRequired               = errors.New("persistent event accounting rules are required")
	ErrWriteOffPending                  = errors.New("subledger open line already has a pending write-off")
	ErrNoPendingWriteOff                = errors.New("subledger open line has no pending write-off")
	ErrCompanyScopeRequired             = errors.New("company scope is required")
)

type Repository interface {
	EventRepository
	EventAccountingRuleRepository
	AtomicEconomicEventRepository
	CompanyEventRepository
	OpenLineRepository
	CompanyOpenLineRepository
	AtomicOpenLineRepository
	ConditionalOpenLineRepository
	ConditionalOpenLinesAndDraftRepository
	AtomicOpenLineWriteOffConfirmationRepository
	PostedJournalEntryReader
	AtomicPostedOpenLineRepository
	FixedAssetRepository
	AtomicFixedAssetDraftRepository
	AtomicFixedAssetOperationDraftRepository
	AtomicFixedAssetConfirmationRepository
	BankItemRepository
	CompanyBankItemRepository
	ConditionalBankItemRepository
	ConditionalBankItemsAndDraftRepository
	ControlAccountReader
}

type EventRepository interface {
	SaveEvent(context.Context, EconomicEvent) error
	GetEventByIdempotencyKey(context.Context, string) (EconomicEvent, bool, error)
}

type OpenLineRepository interface {
	SaveOpenLine(context.Context, OpenLine) error
	GetOpenLine(context.Context, uuid.UUID, uuid.UUID) (OpenLine, error)
	ListOpenLines(context.Context, string) ([]OpenLine, error)
}

type FixedAssetRepository interface {
	SaveFixedAsset(context.Context, FixedAsset) error
	GetFixedAsset(context.Context, uuid.UUID, uuid.UUID) (FixedAsset, error)
	ListFixedAssets(context.Context) ([]FixedAsset, error)
}

type BankItemRepository interface {
	SaveBankItem(context.Context, BankItem) error
	ListBankItems(context.Context, string) ([]BankItem, error)
}

func (r *MemoryRepository) SaveEventAndDraft(ctx context.Context, event EconomicEvent, entry posting.JournalEntry) error {
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
	r.mu.Lock()
	defer r.mu.Unlock()
	key := companyEventKey(event.CompanyID, event.SourceSystem, event.IdempotencyKey)
	if existing, exists := r.events[key]; exists && existing.ResultEntryID != uuid.Nil {
		return ErrAlreadyExists
	}
	if _, exists := r.drafts[entry.ID]; exists {
		return ErrAlreadyExists
	}
	if event.EventID == uuid.Nil {
		event.EventID = uuid.New()
	}
	if entry.SourceEventID == nil {
		eventID := event.EventID
		entry.SourceEventID = &eventID
	}
	if *entry.SourceEventID != event.EventID {
		return posting.ErrInvalidEntry
	}
	if event.GeneratedEntryID == nil {
		entryID := entry.ID
		event.GeneratedEntryID = &entryID
	}
	if *event.GeneratedEntryID != entry.ID {
		return posting.ErrInvalidEntry
	}
	event.ResultEntryID = entry.ID
	event.Status = EventProcessed
	if event.ProcessedAt == nil {
		now := time.Now().UTC()
		event.ProcessedAt = &now
	}
	r.events[key] = cloneEconomicEvent(event)
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	r.drafts[entry.ID] = entry
	return nil
}

type CompanyEventRepository interface {
	GetEventByIdempotencyKeyForCompany(context.Context, uuid.UUID, string) (EconomicEvent, bool, error)
}

type EventLifecycleRepository interface {
	GetEventByIdentity(context.Context, uuid.UUID, string, string) (EconomicEvent, bool, error)
	GetEventByID(context.Context, uuid.UUID, uuid.UUID) (EconomicEvent, error)
	UpdateEventStatus(context.Context, EconomicEvent) error
}

type EventAccountingRuleRepository interface {
	SaveEventAccountingRule(context.Context, EventAccountingRule) error
	ListEventAccountingRulesForCompany(context.Context, uuid.UUID, string) ([]EventAccountingRule, error)
}

type CompanyOpenLineRepository interface {
	ListOpenLinesForCompanyContext(context.Context, uuid.UUID, string) ([]OpenLine, error)
}

type CompanyBankItemRepository interface {
	ListBankItemsForCompany(context.Context, uuid.UUID, string) ([]BankItem, error)
}

type ConditionalBankItemRepository interface {
	CompareAndSaveBankItems(context.Context, []BankItem, []BankItem) error
}

type AtomicOpenLineRepository interface {
	SaveOpenLines(context.Context, []OpenLine) error
}

type ConditionalOpenLineRepository interface {
	CompareAndSaveOpenLines(context.Context, []OpenLine, []OpenLine) error
}

type ConditionalOpenLinesAndDraftRepository interface {
	CompareAndSaveOpenLinesAndDraft(context.Context, []OpenLine, []OpenLine, posting.JournalEntry) error
}

type AtomicOpenLineWriteOffConfirmationRepository interface {
	ConfirmOpenLineWriteOff(context.Context, OpenLine, OpenLine, posting.JournalEntry) error
}

type ConditionalBankItemsAndDraftRepository interface {
	CompareAndSaveBankItemsAndDraft(context.Context, []BankItem, []BankItem, posting.JournalEntry) error
}

type AtomicEconomicEventRepository interface {
	SaveEventAndDraft(context.Context, EconomicEvent, posting.JournalEntry) error
}

type AtomicFixedAssetDraftRepository interface {
	SaveFixedAssetDraft(context.Context, FixedAsset, posting.JournalEntry) error
}

type AtomicFixedAssetOperationDraftRepository interface {
	SaveFixedAssetOperationDraft(context.Context, FixedAsset, FixedAsset, posting.JournalEntry) error
}

type AtomicFixedAssetConfirmationRepository interface {
	ConfirmFixedAssetMutation(context.Context, FixedAsset, FixedAsset, posting.JournalEntry) error
}

type MemoryRepository struct {
	mu     sync.Mutex
	events map[string]EconomicEvent
	drafts map[uuid.UUID]posting.JournalEntry
	lines  map[uuid.UUID]OpenLine
	assets map[uuid.UUID]FixedAsset
	bank   map[string][]BankItem
	rules  map[string]EventAccountingRule
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		events: make(map[string]EconomicEvent),
		drafts: make(map[uuid.UUID]posting.JournalEntry),
		lines:  make(map[uuid.UUID]OpenLine),
		assets: make(map[uuid.UUID]FixedAsset),
		bank:   make(map[string][]BankItem),
		rules:  make(map[string]EventAccountingRule),
	}
}

func eventRuleKey(companyID uuid.UUID, eventType string, priority int) string {
	return companyID.String() + "\x00" + eventType + "\x00" + fmt.Sprint(priority)
}

func (r *MemoryRepository) SaveEventAccountingRule(ctx context.Context, rule EventAccountingRule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rule.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	r.rules[eventRuleKey(rule.CompanyID, rule.EventType, rule.Priority)] = cloneEventAccountingRule(rule)
	return nil
}

func (r *MemoryRepository) ListEventAccountingRulesForCompany(ctx context.Context, companyID uuid.UUID, eventType string) ([]EventAccountingRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rules := make([]EventAccountingRule, 0)
	for _, rule := range r.rules {
		if rule.CompanyID == companyID && rule.EventType == eventType {
			rules = append(rules, cloneEventAccountingRule(rule))
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Priority == rules[j].Priority {
			return rules[i].RuleName < rules[j].RuleName
		}
		return rules[i].Priority > rules[j].Priority
	})
	return rules, nil
}

func (r *MemoryRepository) SaveEvent(ctx context.Context, event EconomicEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	event.SourceSystem = strings.TrimSpace(event.SourceSystem)
	if event.EventID == uuid.Nil {
		event.EventID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	key := companyEventKey(event.CompanyID, event.SourceSystem, event.IdempotencyKey)
	if existing, ok := r.events[key]; ok {
		if existing.ResultEntryID != event.ResultEntryID || existing.EventType != event.EventType ||
			existing.Reference != event.Reference || !existing.Amount.Equal(event.Amount) ||
			existing.PayloadHash != event.PayloadHash || !bytes.Equal(existing.PayloadJSON, event.PayloadJSON) {
			return ErrAlreadyExists
		}
		return nil
	}
	r.events[key] = cloneEconomicEvent(event)
	return nil
}

func (r *MemoryRepository) GetEventByIdempotencyKey(ctx context.Context, key string) (EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return EconomicEvent{}, false, err
	}
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

func (r *MemoryRepository) GetEventByIdempotencyKeyForCompany(ctx context.Context, companyID uuid.UUID, key string) (EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return EconomicEvent{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if event, ok := r.events[companyEventKey(companyID, "", key)]; ok {
		return cloneEconomicEvent(event), true, nil
	}
	var result EconomicEvent
	found := false
	for _, event := range r.events {
		if event.CompanyID == companyID && event.IdempotencyKey == key {
			if found {
				return EconomicEvent{}, false, ErrInvalidEvent
			}
			result, found = event, true
		}
	}
	return cloneEconomicEvent(result), found, nil
}

func (r *MemoryRepository) GetEventByIdentity(ctx context.Context, companyID uuid.UUID, sourceSystem, key string) (EconomicEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return EconomicEvent{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.events[companyEventKey(companyID, sourceSystem, key)]
	return cloneEconomicEvent(event), ok, nil
}

func (r *MemoryRepository) GetEventByID(ctx context.Context, companyID, eventID uuid.UUID) (EconomicEvent, error) {
	if err := ctx.Err(); err != nil {
		return EconomicEvent{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.CompanyID == companyID && event.EventID == eventID {
			return cloneEconomicEvent(event), nil
		}
	}
	return EconomicEvent{}, ErrNotFound
}

func (r *MemoryRepository) UpdateEventStatus(ctx context.Context, event EconomicEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := companyEventKey(event.CompanyID, event.SourceSystem, event.IdempotencyKey)
	current, ok := r.events[key]
	if !ok || current.EventID != event.EventID {
		return ErrNotFound
	}
	r.events[key] = cloneEconomicEvent(event)
	return nil
}

func companyEventKey(companyID uuid.UUID, sourceSystem, key string) string {
	return companyID.String() + "\x00" + sourceSystem + "\x00" + key
}

func cloneEconomicEvent(event EconomicEvent) EconomicEvent {
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

func (r *MemoryRepository) SaveOpenLine(ctx context.Context, line OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
		return ErrCompanyMismatch
	}
	r.lines[line.ID] = cloneOpenLine(line)
	return nil
}

func (r *MemoryRepository) SaveOpenLineForPostedEntry(ctx context.Context, line OpenLine) error {
	return r.SaveOpenLine(ctx, line)
}

func (r *MemoryRepository) GetAccountByIDForCompany(
	ctx context.Context,
	companyID, accountID uuid.UUID,
) (masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.Account{}, err
	}
	return masterdata.Account{}, masterdata.ErrAccountNotFound
}

func (r *MemoryRepository) SaveOpenLines(ctx context.Context, lines []OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (r *MemoryRepository) CompareAndSaveOpenLines(ctx context.Context, expected, updated []OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (r *MemoryRepository) CompareAndSaveOpenLinesAndDraft(ctx context.Context, expected, updated []OpenLine, draft posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
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
			updated[i].CompanyID != oldLine.CompanyID || draft.CompanyID != oldLine.CompanyID {
			return ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			return ErrConcurrentMatch
		}
	}
	if _, exists := r.drafts[draft.ID]; exists {
		return ErrAlreadyExists
	}
	for _, line := range updated {
		r.lines[line.ID] = cloneOpenLine(line)
	}
	draftCopy := draft
	draftCopy.Lines = append([]posting.JournalEntryLine(nil), draft.Lines...)
	r.drafts[draft.ID] = draftCopy
	return nil
}

func (r *MemoryRepository) GetOpenLine(ctx context.Context, companyID, id uuid.UUID) (OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return OpenLine{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	line, ok := r.lines[id]
	if !ok || (companyID != uuid.Nil && line.CompanyID != companyID) {
		return OpenLine{}, ErrNotFound
	}
	return cloneOpenLine(line), nil
}

func (r *MemoryRepository) GetEntryByIDForCompany(ctx context.Context, companyID, id uuid.UUID) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.drafts[id]
	if !ok || (companyID != uuid.Nil && entry.CompanyID != companyID) {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	return entry, nil
}

func (r *MemoryRepository) ConfirmOpenLineWriteOff(ctx context.Context, expected, updated OpenLine, postedEntry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || postedEntry.CompanyID != expected.CompanyID ||
		postedEntry.Status != posting.EntryPosted {
		return ErrInvalidOpenLine
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.lines[expected.ID]
	if !ok {
		return ErrNotFound
	}
	if !reflect.DeepEqual(current, expected) {
		return ErrConcurrentMatch
	}
	actual, ok := r.drafts[postedEntry.ID]
	if !ok {
		return posting.ErrEntryNotFound
	}
	if !FixedAssetEntryMatches(actual, postedEntry) {
		return posting.ErrEntryNotPosted
	}
	r.lines[updated.ID] = cloneOpenLine(updated)
	return nil
}

func (r *MemoryRepository) ListOpenLines(ctx context.Context, partnerID string) ([]OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (r *MemoryRepository) ListOpenLinesForCompanyContext(ctx context.Context, companyID uuid.UUID, partnerID string) ([]OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (r *MemoryRepository) ListOpenLinesForCompany(companyID uuid.UUID, partnerID string) ([]OpenLine, error) {
	return r.ListOpenLinesForCompanyContext(context.Background(), companyID, partnerID)
}

func (r *MemoryRepository) SaveFixedAsset(ctx context.Context, asset FixedAsset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.assets[asset.ID]; ok && existing.CompanyID != asset.CompanyID {
		return ErrCompanyMismatch
	}
	r.assets[asset.ID] = cloneFixedAsset(asset)
	return nil
}

func (r *MemoryRepository) SaveFixedAssetDraft(ctx context.Context, expected FixedAsset, entry posting.JournalEntry) error {
	return r.saveFixedAssetDraft(ctx, expected, expected, entry)
}

func (r *MemoryRepository) SaveFixedAssetOperationDraft(
	ctx context.Context,
	expected, updated FixedAsset,
	entry posting.JournalEntry,
) error {
	if err := ValidateFixedAssetOperationDraft(expected, updated, entry); err != nil {
		return err
	}
	return r.saveFixedAssetDraft(ctx, expected, updated, entry)
}

func (r *MemoryRepository) saveFixedAssetDraft(
	ctx context.Context,
	expected, updated FixedAsset,
	entry posting.JournalEntry,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || entry.ID == uuid.Nil ||
		entry.CompanyID != expected.CompanyID || entry.Status != posting.EntryDraft {
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
	if expected.PendingOperation != "" || updated.PendingOperation != "" &&
		(updated.PendingOperationEntryID != entry.ID ||
			(updated.PendingOperation != "DISPOSAL" && updated.PendingOperation != "IMPAIRMENT" &&
				updated.PendingOperation != FixedAssetOperationCWIPCapitalization)) {
		return ErrAlreadyExists
	}
	if _, exists := r.drafts[entry.ID]; exists {
		return ErrAlreadyExists
	}
	if strings.HasPrefix(entry.DocumentReference, "FIXED_ASSET:") {
		for _, current := range r.drafts {
			if current.DocumentReference == entry.DocumentReference &&
				(current.Status == posting.EntryDraft || current.Status == posting.EntryPosted) {
				return ErrAlreadyExists
			}
		}
	}
	entry.Lines = append([]posting.JournalEntryLine(nil), entry.Lines...)
	r.drafts[entry.ID] = entry
	if updated.PendingOperation != "" {
		r.assets[updated.ID] = cloneFixedAsset(updated)
	}
	return nil
}

func (r *MemoryRepository) ConfirmFixedAssetMutation(ctx context.Context, expected, updated FixedAsset, expectedEntry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || expectedEntry.ID == uuid.Nil ||
		expectedEntry.CompanyID != expected.CompanyID || expectedEntry.Status != posting.EntryPosted {
		return ErrInvalidAsset
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
	if expected.PendingOperation != "" &&
		(expected.PendingOperationEntryID != expectedEntry.ID ||
			updated.PendingOperation != "" || updated.PendingOperationEntryID != uuid.Nil) {
		return ErrConcurrentAssetUpdate
	}
	entry, exists := r.drafts[expectedEntry.ID]
	if !exists {
		return posting.ErrEntryNotFound
	}
	if !FixedAssetEntryMatches(entry, expectedEntry) {
		return posting.ErrEntryNotPosted
	}
	r.assets[updated.ID] = cloneFixedAsset(updated)
	return nil
}

func FixedAssetEntryMatches(actual, expected posting.JournalEntry) bool {
	if actual.ID != expected.ID || actual.CompanyID != expected.CompanyID ||
		actual.Status != posting.EntryPosted || expected.Status != posting.EntryPosted ||
		actual.DocumentReference != expected.DocumentReference ||
		!actual.EntryDate.Equal(expected.EntryDate) || !actual.Date.Equal(expected.Date) ||
		len(actual.Lines) != len(expected.Lines) {
		return false
	}
	for i, actualLine := range actual.Lines {
		expectedLine := expected.Lines[i]
		if actualLine.CompanyID != expectedLine.CompanyID || actualLine.AccountID != expectedLine.AccountID ||
			!actualLine.Debit.Equal(expectedLine.Debit) || !actualLine.Credit.Equal(expectedLine.Credit) {
			return false
		}
	}
	return true
}

func (r *MemoryRepository) GetFixedAsset(ctx context.Context, companyID, id uuid.UUID) (FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return FixedAsset{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	asset, ok := r.assets[id]
	if !ok || (companyID != uuid.Nil && asset.CompanyID != companyID) {
		return FixedAsset{}, ErrNotFound
	}
	return cloneFixedAsset(asset), nil
}

func (r *MemoryRepository) ListFixedAssets(ctx context.Context) ([]FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	assets := make([]FixedAsset, 0, len(r.assets))
	for _, asset := range r.assets {
		assets = append(assets, cloneFixedAsset(asset))
	}
	return assets, nil
}

func (r *MemoryRepository) SaveBankItem(ctx context.Context, item BankItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (r *MemoryRepository) ListBankItems(ctx context.Context, accountID string) ([]BankItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items := append([]BankItem(nil), r.bank[accountID]...)
	return items, nil
}

func (r *MemoryRepository) ListBankItemsForCompany(ctx context.Context, companyID uuid.UUID, accountID string) ([]BankItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

func (r *MemoryRepository) CompareAndSaveBankItems(ctx context.Context, expected, updated []BankItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (r *MemoryRepository) CompareAndSaveBankItemsAndDraft(ctx context.Context, expected, updated []BankItem, draft posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(expected) != len(updated) {
		return ErrInvalidOpenLine
	}
	for i, oldItem := range expected {
		if updated[i].ID != oldItem.ID || updated[i].CompanyID != oldItem.CompanyID ||
			updated[i].AccountID != oldItem.AccountID || draft.CompanyID != oldItem.CompanyID {
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
	if _, exists := r.drafts[draft.ID]; exists {
		return ErrAlreadyExists
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
	draftCopy := draft
	draftCopy.Lines = append([]posting.JournalEntryLine(nil), draft.Lines...)
	r.drafts[draft.ID] = draftCopy
	return nil
}

func cloneOpenLine(line OpenLine) OpenLine {
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]OpenLineMatch(nil), line.Matches...)
	line.MatchHistory = append([]OpenLineMatch(nil), line.MatchHistory...)
	return line
}

func cloneEventAccountingRule(rule EventAccountingRule) EventAccountingRule {
	rule.Lines = append([]EventLineTemplate(nil), rule.Lines...)
	rule.Conditions = cloneStringMap(rule.Conditions)
	rule.Accounts = make(map[string]uuid.UUID, len(rule.Accounts))
	for key, id := range rule.Accounts {
		rule.Accounts[key] = id
	}
	return rule
}

func (r EventAccountingRule) Clone() EventAccountingRule {
	return cloneEventAccountingRule(r)
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneFixedAsset(asset FixedAsset) FixedAsset {
	asset.AppliedEntryIDs = append([]uuid.UUID(nil), asset.AppliedEntryIDs...)
	asset.Components = append([]AssetComponent(nil), asset.Components...)
	if asset.DisposedAt != nil {
		disposedAt := *asset.DisposedAt
		asset.DisposedAt = &disposedAt
	}
	return asset
}
