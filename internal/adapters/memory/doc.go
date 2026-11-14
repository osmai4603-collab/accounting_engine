// Package memory provides an in-memory implementation of the accounting
// storage interfaces.
//
// Its Driver supplies repository behavior used by the accounting engine,
// including master data, posting, subledgers, valuation, reporting, and
// governance operations supported by the implementation. It is useful for
// tests, examples, and local workflows that do not require a PostgreSQL
// service; it is not a durable store and its state is lost when the driver is
// discarded.
//
// Create the driver, pass it to the application engine, and retain it for the
// duration of the workflow. Public unit-of-work callbacks are serialized by a
// process-local lock and commit staged posting, sequence, balance, audit, and
// idempotency changes together; callback errors, cancellation, and panics
// discard those staged changes. Sequence values are monotonic within each
// company and journal and are not consumed by rolled-back unit of work.
//
// Memory cannot establish durability, PostgreSQL isolation or constraints,
// row-level security, or coordination with another process. Use it only when
// selected explicitly for tests and development. This package does not open
// external resources or provide database migration behavior.
package memory
