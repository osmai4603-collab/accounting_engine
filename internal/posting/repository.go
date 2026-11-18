package posting

import (
	"context"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	SaveDraftContext(context.Context, JournalEntry) error
	GetEntryByIDForCompany(context.Context, uuid.UUID, uuid.UUID) (JournalEntry, error)
	PostAtomicContext(context.Context, JournalEntry) error
	GetNextSequenceForCompanyContext(context.Context, uuid.UUID, uuid.UUID) (int64, error)
}

type ContextDraftRepository interface {
	SaveDraftContext(ctx context.Context, entry JournalEntry) error
}

type ContextAtomicPostingRepository interface {
	PostAndSequenceContext(ctx context.Context, entry JournalEntry) (JournalEntry, error)
}

// ContextAtomicIdempotentPostingRepository posts a validated entry carrying a
// caller-supplied idempotency key and reports whether an identical posting
// under the same key had already completed (replay).
type ContextAtomicIdempotentPostingRepository interface {
	PostAndSequenceIdempotentContext(ctx context.Context, entry JournalEntry) (JournalEntry, bool, error)
}

type ContextSequenceRepository interface {
	GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error)
}

type ContextAtomicPostingWithOpenLineRepository interface {
	PostAndSequenceWithOpenLineContext(ctx context.Context, entry JournalEntry, attachment OpenLineAttachment) (JournalEntry, error)
}

type ContextAtomicReversalRepository interface {
	PostReversalAtomicContext(ctx context.Context, original, reversal JournalEntry) (JournalEntry, error)
}

// IdempotentPostingReader finds a previously posted entry by its
// company-scoped caller idempotency key, mirroring IdempotentReversalReader
// for regular postings.
type IdempotentPostingReader interface {
	FindPostedEntryByIdempotencyKeyContext(ctx context.Context, companyID uuid.UUID, key string) (JournalEntry, bool, error)
}

// AtomicReversalCreationRepository creates and posts a reversal without
// exposing an intermediate draft. Implementations must atomically link the
// original and reversal and enforce idempotency when the key is non-empty.
type AtomicReversalCreationRepository interface {
	CreateAndPostReversalAtomicContext(ctx context.Context, original, reversal JournalEntry) (JournalEntry, error)
}

// IdempotentReversalReader finds a previously completed reversal by its
// company-scoped idempotency key.
type IdempotentReversalReader interface {
	FindReversalByIdempotencyKeyContext(ctx context.Context, companyID uuid.UUID, key string) (JournalEntry, bool, error)
}

type AtomicPostingRepository interface {
	PostAndSequence(JournalEntry) (JournalEntry, error)
}

type CompanyScopedEntryReader interface {
	GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (JournalEntry, error)
}

type OpenLineAttachment struct {
	ID               uuid.UUID
	CompanyID        uuid.UUID
	JournalEntryID   uuid.UUID
	PartnerID        uuid.UUID
	ControlAccountID uuid.UUID
	Kind             string
	Amount           financialtypes.Money
	FunctionalAmount financialtypes.Money
	PostingDate      time.Time
	DocumentDate     time.Time
	DueDate          time.Time
	Reference        string
}

type AtomicPostingWithOpenLineRepository interface {
	PostAndSequenceWithOpenLine(JournalEntry, OpenLineAttachment) (JournalEntry, error)
}

type AtomicReversalRepository interface {
	PostReversalAtomic(JournalEntry, JournalEntry) (JournalEntry, error)
}

type BulkRepository interface {
	Repository
	PostBulk(context.Context, []JournalEntry) error
}

type AtomicAttachmentRepository interface {
	PostWithAttachments(ctx context.Context, entry *JournalEntry, attachments []LedgerAttachment) error
}

type ContextAtomicAttachmentRepository interface {
	PostWithAttachmentsContext(ctx context.Context, entry *JournalEntry, attachments []LedgerAttachment) error
}
