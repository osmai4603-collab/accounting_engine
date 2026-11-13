package events

import (
	"context"
	"errors"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"

	"github.com/google/uuid"
)

var (
	ErrRecordNotFound = errors.New("idempotency record not found")
	ErrStateConflict  = errors.New("idempotency record cannot be completed from its current state")
)

type Ingestion struct {
	CompanyID      uuid.UUID
	IdempotencyKey string
	EventType      string
	PayloadHash    string
	EntryID        uuid.UUID
	Status         string
	FailureReason  string
	Entry          ledger.JournalEntry
}

type AccountMapping struct {
	CompanyID           uuid.UUID
	EventType           string
	LedgerID            string
	DebitAccountCode    string
	CreditAccountCode   string
	DescriptionTemplate string
	JournalCode         string
}

type IdempotencyStore interface {
	GetPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string) (Ingestion, bool, error)
	ClaimPublicEventIngestion(ctx context.Context, record Ingestion) (existing Ingestion, claimed bool, err error)
	CompletePublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string, entry ledger.JournalEntry) error
	FailPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey, reason string) error
}

type MappingStore interface {
	GetPublicEventAccountMapping(ctx context.Context, companyID uuid.UUID, eventType, ledgerID string) (AccountMapping, bool, error)
	SavePublicEventAccountMapping(ctx context.Context, mapping AccountMapping) error
}
