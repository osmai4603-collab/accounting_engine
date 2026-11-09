package posting

import (
	"fmt"

	"github.com/google/uuid"
)

type BulkPostError struct {
	Index   int
	EntryID uuid.UUID
	Err     error
}

func (e *BulkPostError) Error() string {
	return fmt.Sprintf("bulk posting failed at entry %d (%s): %v", e.Index, e.EntryID, e.Err)
}

func (e *BulkPostError) Unwrap() error {
	return e.Err
}

func (e *Engine) PostBulk(entries []JournalEntry, posterID string) error {
	for i := range entries {
		if err := entries[i].Validate(); err != nil {
			return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
		}
		if entries[i].ID == uuid.Nil {
			entries[i].ID = uuid.New()
		}
		if err := e.PostEntry(&entries[i], posterID); err != nil {
			return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
		}
	}
	return nil
}
