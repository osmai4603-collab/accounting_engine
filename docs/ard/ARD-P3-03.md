# ARD-P3-03: Memory Adapter Unit-of-Work Semantics

## Status

Implemented for the Memory adapter.

## Decision

The Memory driver exposes the public storage transaction and unit-of-work
interfaces through `Driver.Ports()`. A transaction takes the driver's
exclusive process-local lock, stages mutable posting state in a private
snapshot, and publishes it only after a successful callback and a final
context check. Callback errors, panics, and cancellation discard the snapshot.
Read-only transactions reject write operations.

The snapshot stages entries, company/journal sequences, balances, adjusting
period markers, audit records, and idempotency records. The read-only master
data needed by transactional ports is shared behind the same driver lock.
Regular SPI ports use the transaction context to route calls through that
staged snapshot.

Idempotency records are keyed by `(CompanyID, Key)`. Repeated claims with the
same operation and payload hash return the existing record without claiming
it again; reusing the company/key pair with a different operation or payload
hash returns `storage.ErrUniqueConstraint`. Completion and failure updates
require a matching hash. Record response bytes and entry IDs are copied at
storage boundaries.

Sequences are allocated monotonically per `(CompanyID, JournalID)` while the
driver lock is held. A rolled-back unit of work does not consume sequence
values. Memory declares in-process repeatable-read/serializable behavior for
its serialized transactions, but does not claim database durability, foreign
key enforcement, savepoints, or deadlock detection.

## Limitations

- State is volatile and local to one `Driver` instance.
- Locking does not coordinate across processes or driver instances.
- Memory does not emulate PostgreSQL RLS, database constraints, crash recovery,
  WAL durability, or PostgreSQL-specific isolation and failure behavior.
- The Memory capability declaration is not evidence of production readiness.
- The shared SPI contract suite remains defined in the SPI package; this task
  did not alter its harness. Memory-specific tests exercise commit/rollback,
  context-bound transaction routing, scoped idempotency, and read-only writes.

## Verification

The following checks pass:

- `go build ./internal/adapters/memory`
- Memory production sources plus `unit_of_work_test.go`, using `go test`
- The same isolated Memory unit-of-work test with `go test -race`
- Memory production sources, using `go vet`

The full `go test ./internal/adapters/memory` invocation is currently blocked
by unrelated compile errors in `internal/adapters/postgres/public_events.go`
(`events.Ingestion` has no `CreatedAt` or `UpdatedAt` fields). PostgreSQL and
shared SPI files were outside this task's edit scope and were left unchanged.

The Memory-specific tests exercise:

- rollback of staged sequence, audit, and idempotency mutations after callback
  failure;
- company-scoped idempotency and payload-hash conflict detection;
- rollback of sequence allocation through context-bound `TransactionRunner`
  ports; and
- rejection of writes in read-only transactions.
