package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Standard transaction errors returned by storage adapters to signal
// transient concurrency issues or constraint violations.
var (
	// ErrSerializationFailure indicates a concurrent modification or snapshot isolation conflict.
	// This error is transient and safe to retry with a fresh transaction.
	ErrSerializationFailure = errors.New("storage: transaction serialization failure")

	// ErrDeadlockDetected indicates that a deadlock occurred between concurrent transactions.
	// This error is transient and safe to retry with a fresh transaction.
	ErrDeadlockDetected = errors.New("storage: transaction deadlock detected")

	// ErrUniqueConstraint indicates a unique key or identifier collision.
	// This error is terminal for the same payload.
	ErrUniqueConstraint = errors.New("storage: unique constraint violation")

	// ErrTransactionAborted indicates that the transaction was aborted due to context cancellation
	// or unrecoverable adapter failure.
	ErrTransactionAborted = errors.New("storage: transaction aborted")

	// ErrNestedUnitOfWork indicates that Execute was called while a unit of
	// work is already active in the context. Callers that intentionally nest
	// (for example an adapter-provided posting lock) must reuse the active
	// transaction instead of opening a second one.
	ErrNestedUnitOfWork = errors.New("storage: nested unit of work is not supported")
)

// TxOptions specifies transaction configuration such as isolation level and read-only mode.
type TxOptions struct {
	Isolation IsolationLevel
	ReadOnly  bool
}

// UnitOfWork exposes the transactional store ports bound to a single transaction scope.
// All writes and reads executed through these ports participate in the same atomic transaction.
// Implementations must ensure that uncommitted mutations made inside this unit of work
// are isolated from other concurrent transactions according to the configured isolation level.
type UnitOfWork interface {
	Drafts() DraftStore
	Entries() EntryReader
	Posting() PostingWriter
	Sequences() SequenceAllocator
	Audit() AuditWriter
	Idempotency() IdempotencyStore
	Balance() BalanceReader
}

// UnitOfWorkRunner coordinates the execution of an atomic unit of work.
// When the callback function returns nil, all operations are committed atomically.
// If the callback returns an error or panics, the entire unit of work is rolled back cleanly.
type UnitOfWorkRunner interface {
	Execute(ctx context.Context, fn func(ctx context.Context, uow UnitOfWork) error) error
	ExecuteWithOptions(ctx context.Context, opts TxOptions, fn func(ctx context.Context, uow UnitOfWork) error) error
}

// AdvancedTransactionRunner extends TransactionRunner with explicit options.
type AdvancedTransactionRunner interface {
	TransactionRunner
	WithinTransactionWithOptions(ctx context.Context, opts TxOptions, fn func(ctx context.Context) error) error
}

// AdapterCapabilities declares the explicit transactional, concurrency, and durability
// capabilities guaranteed by an adapter. Guarantees must be declared explicitly and never
// assumed by callers.
type AdapterCapabilities struct {
	// AdapterName is the human-readable identifier of the storage implementation.
	AdapterName string
	// SupportsRepeatableRead indicates support for repeatable read isolation.
	SupportsRepeatableRead bool
	// SupportsSerializable indicates support for strict serializable isolation.
	SupportsSerializable bool
	// EnforcesForeignKeys indicates database-level foreign key constraint enforcement.
	EnforcesForeignKeys bool
	// GuaranteesDurability indicates durable writes to persistent storage (e.g. disk/WAL).
	// In-memory adapters return false.
	GuaranteesDurability bool
	// SupportsSavepoints indicates support for nested transaction savepoints.
	SupportsSavepoints bool
	// DetectsDeadlocks indicates engine/adapter deadlock detection and abort semantics.
	DetectsDeadlocks bool
}

// IsRetryable reports whether err represents a transient transaction failure
// (such as a serialization conflict or deadlock) that can safely be retried
// by re-running the entire unit of work from the beginning.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSerializationFailure) || errors.Is(err, ErrDeadlockDetected) {
		return true
	}
	type retryableError interface {
		Retryable() bool
	}
	var r retryableError
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return false
}

// RetryPolicy specifies parameters for retrying transient transaction errors.
type RetryPolicy struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// DefaultRetryPolicy returns recommended retry defaults for high-concurrency accounting workloads.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
	}
}

// ExecuteWithRetry executes a unit of work with automated backoff retries when transient errors occur.
func ExecuteWithRetry(ctx context.Context, runner UnitOfWorkRunner, opts TxOptions, policy RetryPolicy, fn func(ctx context.Context, uow UnitOfWork) error) error {
	if runner == nil {
		return errors.New("storage: unit of work runner is nil")
	}
	var lastErr error
	backoff := policy.InitialBackoff
	if backoff <= 0 {
		backoff = 10 * time.Millisecond
	}

	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: %v (previous attempt failed with: %v)", ErrTransactionAborted, ctx.Err(), lastErr)
			case <-time.After(backoff):
			}
			backoff *= 2
			if policy.MaxBackoff > 0 && backoff > policy.MaxBackoff {
				backoff = policy.MaxBackoff
			}
		}

		err := runner.ExecuteWithOptions(ctx, opts, fn)
		if err == nil {
			return nil
		}
		if !IsRetryable(err) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("storage: retry limit exceeded (%d retries): %w", policy.MaxRetries, lastErr)
}
