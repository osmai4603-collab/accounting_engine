package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

type traceparentContextKey struct{}

var traceparentRegex = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)

var (
	ErrInvalidTraceparentFormat = errors.New("invalid W3C traceparent format: must be 00-<32 hex trace-id>-<16 hex span-id>-<2 hex flags>")
	ErrZeroTraceID              = errors.New("invalid W3C traceparent: trace-id cannot be all zeros")
	ErrZeroSpanID               = errors.New("invalid W3C traceparent: span-id cannot be all zeros")
)

// ValidateTraceparent checks whether a string conforms strictly to the W3C Trace Context specification.
func ValidateTraceparent(tp string) error {
	matches := traceparentRegex.FindStringSubmatch(strings.TrimSpace(tp))
	if len(matches) != 4 {
		return ErrInvalidTraceparentFormat
	}
	traceID := matches[1]
	spanID := matches[2]
	if traceID == "00000000000000000000000000000000" {
		return ErrZeroTraceID
	}
	if spanID == "0000000000000000" {
		return ErrZeroSpanID
	}
	return nil
}

// ContextWithTraceparent returns a new context containing the provided W3C traceparent.
func ContextWithTraceparent(ctx context.Context, traceparent string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceparentContextKey{}, strings.TrimSpace(traceparent))
}

// TraceparentFromContext extracts the W3C traceparent from the context if present.
func TraceparentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val, ok := ctx.Value(traceparentContextKey{}).(string)
	if !ok {
		return ""
	}
	return val
}

// GenerateTraceparent generates a compliant, random W3C traceparent header string.
func GenerateTraceparent(sampled bool) (string, error) {
	var traceID [16]byte
	if _, err := rand.Read(traceID[:]); err != nil {
		return "", fmt.Errorf("generate trace id: %w", err)
	}
	if traceID == [16]byte{} {
		traceID[15] = 1
	}

	var spanID [8]byte
	if _, err := rand.Read(spanID[:]); err != nil {
		return "", fmt.Errorf("generate span id: %w", err)
	}
	if spanID == [8]byte{} {
		spanID[7] = 1
	}

	flags := "00"
	if sampled {
		flags = "01"
	}

	return fmt.Sprintf("00-%s-%s-%s", hex.EncodeToString(traceID[:]), hex.EncodeToString(spanID[:]), flags), nil
}

// AnnotateQueryWithTraceparent formats a SQL query following the sqlcommenter standard,
// appending /*traceparent='...'*/ before any trailing semicolon.
func AnnotateQueryWithTraceparent(query, traceparent string) string {
	tp := strings.TrimSpace(traceparent)
	if tp == "" {
		return query
	}
	comment := fmt.Sprintf(" /*traceparent='%s'*/", tp)
	trimmed := strings.TrimRight(query, " \t\r\n")
	if strings.HasSuffix(trimmed, ";") {
		return strings.TrimSuffix(trimmed, ";") + comment + ";"
	}
	return query + comment
}

// AnnotateQuery annotates a query with the traceparent found in ctx, if available.
func (d *Driver) AnnotateQuery(ctx context.Context, query string) string {
	tp := TraceparentFromContext(ctx)
	if tp == "" {
		return query
	}
	return AnnotateQueryWithTraceparent(query, tp)
}

// CurrentSessionTraceparent retrieves the current transaction-level traceparent setting from PostgreSQL.
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

