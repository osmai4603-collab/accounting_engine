package posting

import (
	financialtypes "accounting_engine/internal/types"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	SaveDraft(JournalEntry) error
	GetEntryByID(uuid.UUID) (JournalEntry, error)
	PostAtomic(JournalEntry) error
	GetNextSequence(uuid.UUID) (int64, error)
}

type AtomicPostingRepository interface {
	PostAndSequence(JournalEntry) (JournalEntry, error)
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
	PostReversalAtomic(original, reversal JournalEntry) (JournalEntry, error)
}

type BulkRepository interface {
	Repository
	PostBulk([]JournalEntry) error
}
