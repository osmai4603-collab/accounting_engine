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
// duration of the workflow. Company scoping and atomic operations follow the
// methods implemented by the driver. This package does not open external
// resources and does not provide database migration behavior.
package memory
