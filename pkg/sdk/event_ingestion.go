package sdk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	publicevents "accounting_engine/pkg/spi/events"

	"github.com/google/uuid"
)

var (
	ErrIdempotencyKeyConflict     = errors.New("idempotency key already used with a different payload")
	ErrIdempotencyInProgress      = errors.New("idempotency key processing is in progress")
	ErrEventMappingNotFound       = errors.New("no account mapping registered for event type")
	ErrExchangeRateUnavailable    = errors.New("exchange rate source is unavailable")
	ErrEventDimensionsUnsupported = errors.New("event dimensions cannot be resolved to approved master data")
	ErrEventPayloadUnsupported    = errors.New("event payload requires a template that explicitly maps payload fields")
	ErrIdempotencyRecordNotFound  = publicevents.ErrRecordNotFound
	ErrIdempotencyStateConflict   = publicevents.ErrStateConflict
)

type PublicEventIngestion = publicevents.Ingestion
type PublicEventAccountMapping = publicevents.AccountMapping
type PublicEventIdempotencyStore = publicevents.IdempotencyStore
type PublicEventMappingStore = publicevents.MappingStore

func publicEventFingerprint(event events.AccountingEvent, rule events.PostingRule) (string, error) {
	payload := struct {
		EventType     string               `json:"event_type"`
		EventID       string               `json:"event_id"`
		CompanyID     types.CompanyID      `json:"company_id"`
		LedgerID      string               `json:"ledger_id"`
		EffectiveDate time.Time            `json:"effective_date"`
		Amount        types.Money          `json:"amount"`
		Dimensions    map[string]string    `json:"dimensions"`
		Payload       map[string]any       `json:"payload"`
		JournalDraft  *ledger.JournalEntry `json:"journal_draft"`
		Rule          events.PostingRule   `json:"rule"`
	}{
		EventType: event.EventType, EventID: event.EventID, CompanyID: event.CompanyID,
		LedgerID: strings.TrimSpace(event.LedgerID), EffectiveDate: event.EffectiveDate.UTC(),
		Amount: event.Amount, Dimensions: event.Dimensions, Payload: event.Payload,
		JournalDraft: event.JournalDraft, Rule: rule,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeLedgerEntryIDs(entry *ledger.JournalEntry) {
	if entry == nil {
		return
	}
	if uuid.UUID(entry.ID) == uuid.Nil {
		entry.ID = types.NewJournalEntryID()
	}
	for i := range entry.Lines {
		if uuid.UUID(entry.Lines[i].ID) == uuid.Nil {
			entry.Lines[i].ID = types.NewJournalEntryLineID()
		}
		if entry.CompanyID != types.CompanyID(uuid.Nil) {
			// Company is on entry; line-level company not represented in public DTO.
		}
	}
	if entry.EffectiveDate.IsZero() && !entry.PostingDate.IsZero() {
		entry.EffectiveDate = entry.PostingDate
	}
	if entry.PostingDate.IsZero() && !entry.EffectiveDate.IsZero() {
		entry.PostingDate = entry.EffectiveDate
	}
	if entry.Status == "" {
		entry.Status = ledger.EntryDraft
	}
}
