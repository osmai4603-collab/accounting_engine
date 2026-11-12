package sdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
)

var (
	ErrIdempotencyKeyConflict = errors.New("idempotency key already used with a different payload")
	ErrIdempotencyInProgress  = errors.New("idempotency key processing is in progress")
	ErrEventMappingNotFound   = errors.New("no account mapping registered for event type")
)

// PublicEventIngestion stores the durable result of a public event ingestion (B.2).
type PublicEventIngestion struct {
	CompanyID      uuid.UUID
	IdempotencyKey string
	EventType      string
	PayloadHash    string
	EntryID        uuid.UUID
	Status         string // processing|processed|failed
	Entry          ledger.JournalEntry
}

// PublicEventAccountMapping is a per-company (and optional ledger) mapping for B.4.
type PublicEventAccountMapping struct {
	CompanyID           uuid.UUID
	EventType           string
	LedgerID            string
	DebitAccountCode    string
	CreditAccountCode   string
	DescriptionTemplate string
	JournalCode         string
}

// PublicEventIdempotencyStore provides durable idempotency for public event ingestion.
type PublicEventIdempotencyStore interface {
	GetPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string) (PublicEventIngestion, bool, error)
	// ClaimPublicEventIngestion atomically reserves the key if unused and returns (record, claimed, err).
	// If not claimed, record contains the existing ingestion.
	ClaimPublicEventIngestion(ctx context.Context, record PublicEventIngestion) (existing PublicEventIngestion, claimed bool, err error)
	CompletePublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string, entry ledger.JournalEntry) error
	FailPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey, reason string) error
}

// PublicEventMappingStore provides configurable per-company/ledger event mappings (B.4).
type PublicEventMappingStore interface {
	GetPublicEventAccountMapping(ctx context.Context, companyID uuid.UUID, eventType, ledgerID string) (PublicEventAccountMapping, bool, error)
	SavePublicEventAccountMapping(ctx context.Context, mapping PublicEventAccountMapping) error
}

func publicEventFingerprint(eventType string, companyID types.CompanyID, ledgerID string, draft *ledger.JournalEntry, amountCurrency string, amount string, debitCode, creditCode string) string {
	payload := map[string]any{
		"event_type":  eventType,
		"company_id":  uuid.UUID(companyID).String(),
		"ledger_id":   strings.TrimSpace(ledgerID),
		"amount":      amount,
		"currency":    amountCurrency,
		"debit_code":  debitCode,
		"credit_code": creditCode,
	}
	if draft != nil {
		// Fingerprint key draft identity/content aspects.
		lines := make([]map[string]any, 0, len(draft.Lines))
		for _, l := range draft.Lines {
			lines = append(lines, map[string]any{
				"account_id":  uuid.UUID(l.AccountID).String(),
				"tx_amount":   l.TransactionAmount.Amount().String(),
				"tx_currency": l.TransactionCurrency,
				"fd":          l.FunctionalDebit.Amount().String(),
				"fc":          l.FunctionalCredit.Amount().String(),
			})
		}
		payload["draft"] = map[string]any{
			"id":          uuid.UUID(draft.ID).String(),
			"effective":   draft.EffectiveDate.UTC().Format(timeRFC3339Nano),
			"desc":        draft.Description,
			"ref":         draft.ReferenceID,
			"source":      draft.SourceModule,
			"line_count":  len(draft.Lines),
			"lines":       lines,
			"company_id":  uuid.UUID(draft.CompanyID).String(),
		}
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

const timeRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

func normalizeLedgerEntryIDs(entry *ledger.JournalEntry) {
	if entry == nil {
		return
	}
	if uuid.UUID(entry.ID) == uuid.Nil {
		entry.ID = types.NewJournalEntryID()
	}
	for i := range entry.Lines {
		if uuid.UUID(entry.Lines[i].ID) == uuid.Nil {
			entry.Lines[i].ID = types.AccountID(types.NewJournalEntryID())
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
