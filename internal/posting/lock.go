package posting

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"
)

// PostingLocker serializes the critical section of a posting operation for one
// company/journal pair. It is an optional engine capability supplied by a
// storage adapter; when absent the engine falls back to an in-process keyed
// locker.
//
// Implementations must scope the lock at least to company+journal. PostgreSQL
// implementations should use a transaction-scoped advisory lock so the lock is
// released automatically on commit or rollback and other application instances
// observe the same lock. In-process implementations must not serialize
// unrelated journals.
type PostingLocker interface {
	WithinPostingLock(
		ctx context.Context,
		companyID uuid.UUID,
		journalID uuid.UUID,
		fn func(ctx context.Context) error,
	) error
}

type postingLockKey struct {
	companyID uuid.UUID
	journalID uuid.UUID
}

type postingLockEntry struct {
	mu   sync.Mutex
	refs int
}

// KeyedPostingLocker is the default in-process implementation. It replaces the
// former engine-wide mutex with one mutex per company+journal so independent
// journals can post concurrently. Unused entries are removed to avoid an
// unbounded lock table.
type KeyedPostingLocker struct {
	mu    sync.Mutex
	locks map[postingLockKey]*postingLockEntry
}

func NewKeyedPostingLocker() *KeyedPostingLocker {
	return &KeyedPostingLocker{locks: make(map[postingLockKey]*postingLockEntry)}
}

func (l *KeyedPostingLocker) WithinPostingLock(
	ctx context.Context,
	companyID uuid.UUID,
	journalID uuid.UUID,
	fn func(ctx context.Context) error,
) error {
	if fn == nil {
		return ErrInvalidEntry
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := postingLockKey{companyID: companyID, journalID: journalID}
	l.mu.Lock()
	entry, ok := l.locks[key]
	if !ok {
		entry = &postingLockEntry{}
		l.locks[key] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	defer func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ctx)
}

// WithinPostingLocks acquires several journal locks in deterministic order and
// runs fn while all of them are held. Bulk posting uses this to avoid
// deadlocks between operations that span multiple journals.
func WithinPostingLocks(
	ctx context.Context,
	locker PostingLocker,
	companyID uuid.UUID,
	journalIDs []uuid.UUID,
	fn func(ctx context.Context) error,
) error {
	if locker == nil {
		locker = NewKeyedPostingLocker()
	}
	unique := make(map[uuid.UUID]struct{}, len(journalIDs))
	ordered := make([]uuid.UUID, 0, len(journalIDs))
	for _, id := range journalIDs {
		if _, seen := unique[id]; seen {
			continue
		}
		unique[id] = struct{}{}
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].String() < ordered[j].String()
	})
	var acquire func(index int, current context.Context) error
	acquire = func(index int, current context.Context) error {
		if index >= len(ordered) {
			return fn(current)
		}
		return locker.WithinPostingLock(current, companyID, ordered[index], func(lockCtx context.Context) error {
			return acquire(index+1, lockCtx)
		})
	}
	return acquire(0, ctx)
}
