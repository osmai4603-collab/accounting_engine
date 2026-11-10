// Package main implements the agy command-line utility.
//
// The command currently exposes database migration operations through
// "agy db migrate [up|down] [--steps N]". It reads the PostgreSQL connection
// string from DATABASE_URL, validates the requested direction and rollback
// step count, installs interrupt and termination cancellation, verifies the
// database connection, and delegates schema application or rollback to the
// postgres storage package. Migration-up applies the schema and outstanding
// migrations; migration-down reverses the requested number of migration steps.
// Errors are written to standard error and cause a non-zero process exit.
package main
