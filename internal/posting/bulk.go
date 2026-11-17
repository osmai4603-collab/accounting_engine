package posting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
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

// PostBulk posts a batch of same-company draft entries. When the underlying
// repository implements BulkRepository the whole batch is handed to the
// storage layer as one atomic unit: either every entry is posted and sequenced
// or the entire group is rolled back with no dangling or partially posted
// entries. Repositories without bulk support reject the operation rather than
// risk leaving a partially posted batch.
func (e *Engine) PostBulk(entries []JournalEntry, posterID string) error {
	return e.PostBulkContext(context.Background(), entries, posterID)
}

func (e *Engine) PostBulkContext(ctx context.Context, entries []JournalEntry, posterID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	companyID := entries[0].CompanyID
	for i := range entries {
		if err := entries[i].Validate(); err != nil {
			return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
		}
		if entries[i].ID == uuid.Nil {
			entries[i].ID = uuid.New()
		}
		if entries[i].CompanyID != companyID {
			return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: masterdata.ErrCompanyScopeRequired}
		}
		if entries[i].Status != EntryDraft {
			if entries[i].Status == EntryPosted {
				return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: ErrMoveAlreadyPosted}
			}
			return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: ErrEntryNotDraft}
		}
	}
	if repository, ok := e.repo.(BulkRepository); ok {
		return e.postBulkAtomic(ctx, repository, entries, posterID)
	}
	return ErrAtomicBulkPostingRequired
}

func (e *Engine) postBulkAtomic(ctx context.Context, repository BulkRepository, entries []JournalEntry, posterID string) (err error) {
	start := time.Now()
	if e.metrics != nil {
		e.metrics.IncPostingAttempts()
		defer func() {
			e.metrics.ObserveTransactionDuration(time.Since(start).Seconds())
			if err != nil {
				_, category := ClassifyError(err)
				e.metrics.IncPostingFailure(string(category))
			}
		}()
	}

	journalIDs := make([]uuid.UUID, 0, len(entries))
	for i := range entries {
		journalIDs = append(journalIDs, entries[i].JournalID)
	}
	return WithinPostingLocks(ctx, e.postingLocker(), entries[0].CompanyID, journalIDs, func(lockCtx context.Context) error {
		originals := make([]JournalEntry, len(entries))
		copy(originals, entries)
		restore := func() {
			copy(entries, originals)
		}

		now := time.Now().UTC()
		for i := range entries {
			if err := e.prepareForPosting(lockCtx, &entries[i], posterID, now); err != nil {
				restore()
				return &BulkPostError{Index: i, EntryID: entries[i].ID, Err: err}
			}
		}
		for i := range entries {
			entries[i].PostingDate = calendarDate(now)
			entries[i].PostedBy = posterID
			entries[i].PostedAt = &now
			entries[i].Status = EntryPosted
			entries[i].UpdatedAt = now
		}
		if err := repository.PostBulk(lockCtx, entries); err != nil {
			restore()
			return err
		}
		return nil
	})
}
