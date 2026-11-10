package posting

import (
	financialtypes "accounting_engine/internal/types"
	"context"
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

type ContextSequenceRepository interface {
	GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error)
}

type ContextAtomicPostingWithOpenLineRepository interface {
	PostAndSequenceWithOpenLineContext(ctx context.Context, entry JournalEntry, attachment OpenLineAttachment) (JournalEntry, error)
}

type ContextAtomicReversalRepository interface {
	PostReversalAtomicContext(ctx context.Context, original, reversal JournalEntry) (JournalEntry, error)
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

