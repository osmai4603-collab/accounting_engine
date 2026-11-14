package events

import (
	"context"
	"errors"
	"strings"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"

	"github.com/google/uuid"
)

var (
	ErrRecordNotFound = errors.New("idempotency record not found")
	ErrStateConflict  = errors.New("idempotency record cannot be completed from its current state")
)

// Ingestion is the durable state of one public event ingestion. The tuple
// (CompanyID, SourceSystem, IdempotencyKey) identifies a record; callers must
// reuse the same payload hash when retrying an operation.
type Ingestion struct {
	CompanyID      uuid.UUID
	IdempotencyKey string
	// SourceSystem isolates equal idempotency keys that originate from
	// different upstream systems. It defaults to DefaultSourceSystem.
	SourceSystem string
	EventType    string
	PayloadHash  string
	// PayloadHashVersion records the normalization version used to compute
	// PayloadHash so hashes can be compared across releases.
	PayloadHashVersion string
	EntryID            uuid.UUID
	// Status is one of the canonical or legacy status constants below.
	Status string
	// FailureClass is FailureClassTransient, FailureClassTerminal, or empty.
	FailureClass  string
	FailureReason string
	Entry         ledger.JournalEntry
}

// Canonical ingestion lifecycle states.
//
//	started           - a caller owns the key and is producing the result
//	completed         - the result entry is persisted and can be replayed
//	retryable_failure - a transient failure occurred; a later retry may reclaim
//	terminal_failure  - a non-retryable failure occurred; retries are rejected
//
// Legacy values written by pre-P3-04 releases are still accepted on read and
// remain equivalent to their canonical state.
const (
	StatusStarted          = "started"
	StatusCompleted        = "completed"
	StatusRetryableFailure = "retryable_failure"
	StatusTerminalFailure  = "terminal_failure"

	// Legacy aliases (pre-P3-04 rows and implementations).
	StatusProcessing = "processing"
	StatusProcessed  = "processed"
	StatusFailed     = "failed"

	// FailureClassTransient marks an error that may be retried. FailureClassTerminal
	// marks a deterministic failure that must never be retried.
	FailureClassTransient = "transient"
	FailureClassTerminal  = "terminal"

	// DefaultSourceSystem is used when a caller does not declare a source.
	DefaultSourceSystem = "event"
)

// NormalizeStatus maps legacy status values to their canonical state.
func NormalizeStatus(status string) string {
	switch strings.TrimSpace(status) {
	case StatusProcessing:
		return StatusStarted
	case StatusProcessed:
		return StatusCompleted
	case StatusFailed:
		return StatusTerminalFailure
	default:
		return strings.TrimSpace(status)
	}
}

// IsCompleted reports whether status represents a replayable result.
func IsCompleted(status string) bool {
	return NormalizeStatus(status) == StatusCompleted
}

// IsStarted reports whether status represents an in-flight operation.
func IsStarted(status string) bool {
	return NormalizeStatus(status) == StatusStarted
}

// IsRetryableFailure reports whether status may be reclaimed by a retry.
func IsRetryableFailure(status string) bool {
	return NormalizeStatus(status) == StatusRetryableFailure
}

// IsTerminalFailure reports whether status permanently rejects retries.
func IsTerminalFailure(status string) bool {
	return NormalizeStatus(status) == StatusTerminalFailure
}

// NormalizeSourceSystem returns the trimmed source system or the default.
func NormalizeSourceSystem(sourceSystem string) string {
	sourceSystem = strings.TrimSpace(sourceSystem)
	if sourceSystem == "" {
		return DefaultSourceSystem
	}
	return sourceSystem
}

// ScopedIdempotencyKey returns the durable key string for an idempotency key
// scoped to an event source. The default source keeps the raw key for
// backwards compatibility with pre-P3-04 records; other sources are prefixed
// so equal caller keys never collide across source systems.
func ScopedIdempotencyKey(sourceSystem, idempotencyKey string) string {
	key := strings.TrimSpace(idempotencyKey)
	source := NormalizeSourceSystem(sourceSystem)
	if source == DefaultSourceSystem {
		return key
	}
	return source + ":" + key
}

// CompositeIdempotencyKey renders the full uniqueness identity
// (company, source system, idempotency key). It is suitable for logging and
// for adapters whose native keys are a single string; SQL adapters should use
// the individual columns and their composite uniqueness constraint.
func CompositeIdempotencyKey(companyID uuid.UUID, sourceSystem, idempotencyKey string) string {
	return companyID.String() + "\x00" + NormalizeSourceSystem(sourceSystem) + "\x00" + strings.TrimSpace(idempotencyKey)
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

// IdempotencyStore is the stable durable idempotency port. Implementations
// must isolate records by company and, for Ingestion records that declare a
// SourceSystem, by source system as well.
type IdempotencyStore interface {
	GetPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string) (Ingestion, bool, error)
	ClaimPublicEventIngestion(ctx context.Context, record Ingestion) (existing Ingestion, claimed bool, err error)
	CompletePublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey string, entry ledger.JournalEntry) error
	FailPublicEventIngestion(ctx context.Context, companyID uuid.UUID, idempotencyKey, reason string) error
}

// ScopedIdempotencyStore is an optional additive capability for source-aware
// lookups, payload-hash verified completion, and classified failures. The
// engine prefers it when available and otherwise falls back to the stable
// IdempotencyStore methods for the default source system.
type ScopedIdempotencyStore interface {
	IdempotencyStore
	GetPublicEventIngestionForSource(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey string) (Ingestion, bool, error)
	CompletePublicEventIngestionForSource(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, payloadHash string, entry ledger.JournalEntry) error
	FailPublicEventIngestionForSource(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, failureClass, reason string) error
}

// ClassifiedIdempotencyStore is an optional capability for persisting whether
// a failure is transient (reclaimable) or terminal. Implementers that do not
// provide it remain fully compatible.
type ClassifiedIdempotencyStore interface {
	FailPublicEventIngestionClassified(ctx context.Context, companyID uuid.UUID, sourceSystem, idempotencyKey, failureClass, reason string) error
}

// Stores groups the optional event ingestion persistence capabilities that an
// adapter can expose through storage.Ports. A nil field means the capability is
// unavailable; the engine then fails explicitly instead of degrading to
// process-local state.
type Stores struct {
	Idempotency IdempotencyStore
	Mappings    MappingStore
}

type MappingStore interface {
	GetPublicEventAccountMapping(ctx context.Context, companyID uuid.UUID, eventType, ledgerID string) (AccountMapping, bool, error)
	SavePublicEventAccountMapping(ctx context.Context, mapping AccountMapping) error
}
