package posting

import (
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

type AtomicReversalRepository interface {
	PostReversalAtomic(original, reversal JournalEntry) (JournalEntry, error)
}

type BulkRepository interface {
	Repository
	PostBulk([]JournalEntry) error
}
