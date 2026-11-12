package postgres

import (
	"context"

	"accounting_engine/internal/observability"

	"github.com/jackc/pgx/v5"
)

// The trace-context primitives are owned by internal/observability so that
// application logs and PostgreSQL query annotations always share the same
// canonical trace-id. The exported wrappers below preserve the storage
// package's public API.
var (
	ErrInvalidTraceparentFormat = observability.ErrInvalidTraceparentFormat
	ErrZeroTraceID              = observability.ErrZeroTraceID
	ErrZeroSpanID               = observability.ErrZeroSpanID
)

// ValidateTraceparent checks whether a string conforms strictly to the W3C
// Trace Context specification.
func ValidateTraceparent(tp string) error {
	return observability.ValidateTraceparent(tp)
}

// ContextWithTraceparent returns a new context containing the provided W3C
// traceparent.
func ContextWithTraceparent(ctx context.Context, traceparent string) context.Context {
	return observability.ContextWithTraceparent(ctx, traceparent)
}

// TraceparentFromContext extracts the W3C traceparent from the context if
// present.
func TraceparentFromContext(ctx context.Context) string {
	return observability.TraceparentFromContext(ctx)
}

// TraceIDFromContext extracts the W3C trace-id from the context if present.
func TraceIDFromContext(ctx context.Context) string {
	return observability.TraceIDFromContext(ctx)
}

// GenerateTraceparent generates a compliant, random W3C traceparent header
// string.
func GenerateTraceparent(sampled bool) (string, error) {
	return observability.GenerateTraceparent(sampled)
}

// AnnotateQueryWithTraceparent formats a SQL query following the sqlcommenter
// standard, appending /*traceparent='...'*/ before any trailing semicolon.
// Invalid traceparents are ignored so a forged header cannot inject SQL.
func AnnotateQueryWithTraceparent(query, traceparent string) string {
	return observability.AnnotateQueryWithTraceparent(query, traceparent)
}

// AnnotateQuery annotates a query with the traceparent found in ctx, if
// available.
func (d *Driver) AnnotateQuery(ctx context.Context, query string) string {
	return observability.AnnotateQueryWithTraceparent(query, observability.TraceparentFromContext(ctx))
}

// CurrentSessionTraceparent retrieves the current transaction-level
// traceparent setting from PostgreSQL.
func (d *Driver) CurrentSessionTraceparent(ctx context.Context, tx pgx.Tx) (string, error) {
	var val *string
	err := tx.QueryRow(ctx, "SELECT current_setting('app.traceparent', true)").Scan(&val)
	if err != nil {
		return "", err
	}
	if val == nil {
		return "", nil
	}
	return *val, nil
}
