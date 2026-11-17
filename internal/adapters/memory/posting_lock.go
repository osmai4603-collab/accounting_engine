package memory

import (
	"context"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
)

// WithinPostingLock serializes posting for one company+journal inside this
// process. Memory has no cross-process guarantees, so the equivalent of the
// PostgreSQL advisory lock is a fine-grained keyed mutex: independent journals
// can still progress concurrently.
func (d *Driver) WithinPostingLock(
	ctx context.Context,
	companyID uuid.UUID,
	journalID uuid.UUID,
	fn func(context.Context) error,
) error {
	if d.postingLocks == nil {
		d.postingLocks = posting.NewKeyedPostingLocker()
	}
	return d.postingLocks.WithinPostingLock(ctx, companyID, journalID, fn)
}
