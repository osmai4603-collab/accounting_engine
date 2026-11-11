// Package main implements the agy command-line utility.
//
// The command exposes database migration operations through
// "agy db migrate [up|down] [--steps N]" and IFRS automation through
// "agy ifrs [run|daemon] [--interval DURATION] [--config PATH]". IFRS settings
// can also be supplied through AGY_IFRS_CONFIG_PATH. With no configured
// companies, IFRS automation logs a warning and exits without connecting to
// the database. Configured jobs use structured logs for each job outcome.
// Database commands read their PostgreSQL connection string from DATABASE_URL,
// validate migration options, install interrupt and termination cancellation,
// verify the connection, and delegate schema application or rollback to the
// postgres storage package. Errors cause a non-zero process exit.
package main
