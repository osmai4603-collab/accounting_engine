// Package postgres implements accounting storage on PostgreSQL.
//
// Driver adapts pgx connection-pool operations to the repository interfaces
// used by master data, posting, subledgers, valuation, reporting, and
// governance. New opens and verifies a pool, ensures the base schema and
// migrations are applied, seeds the currency registry, and returns the
// initialized driver. NewWithPool adapts an existing pool; the caller retains
// responsibility for that pool's lifecycle. Close closes the driver's pool.
//
// Company-scoped operations run through WithCompanyTx, which establishes the
// database transaction and company context used by row-level security and
// transaction-local behavior. MigrateUp applies pending migrations;
// MigrateDown rolls back a requested number of migration steps. The package
// also provides query traceparent helpers and persistence for governance and
// IFRS 15 revenue contracts. Contexts are passed to database operations so
// cancellation and deadlines can be honored. Database and migration errors
// are returned to callers.
package postgres
