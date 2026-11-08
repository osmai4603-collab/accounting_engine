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

type BulkRepository interface {
	Repository
	PostBulk([]JournalEntry) error
}
