// Package observability provides structured logging, Prometheus metrics, and
// W3C trace-context propagation for the accounting engine.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var traceparentRegex = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)

var (
	ErrInvalidTraceparentFormat = errors.New("invalid W3C traceparent format: must be 00-<32 hex trace-id>-<16 hex span-id>-<2 hex flags>")
	ErrZeroTraceID              = errors.New("invalid W3C traceparent: trace-id cannot be all zeros")
	ErrZeroSpanID               = errors.New("invalid W3C traceparent: span-id cannot be all zeros")
)

type traceparentContextKey struct{}

// ValidateTraceparent checks whether a string conforms strictly to the W3C
// Trace Context specification.
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

// ContextWithTraceparent returns a new context containing the provided W3C
// traceparent. Invalid values are ignored so a malformed header can never
// poison downstream SQL comments or database settings.
func ContextWithTraceparent(ctx context.Context, traceparent string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	tp := strings.TrimSpace(traceparent)
	if ValidateTraceparent(tp) != nil {
		return ctx
	}
	return context.WithValue(ctx, traceparentContextKey{}, tp)
}

// TraceparentFromContext extracts the W3C traceparent from the context if
// present. A stored value is validated before being returned.
func TraceparentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	val, ok := ctx.Value(traceparentContextKey{}).(string)
	if !ok || ValidateTraceparent(val) != nil {
		return ""
	}
	return val
}

// TraceIDFromTraceparent extracts the 32-hex trace-id from a valid traceparent.
// It returns an empty string when the traceparent is malformed.
func TraceIDFromTraceparent(tp string) string {
	matches := traceparentRegex.FindStringSubmatch(strings.TrimSpace(tp))
	if len(matches) != 4 {
		return ""
	}
	if ValidateTraceparent(tp) != nil {
		return ""
	}
	return matches[1]
}

// TraceIDFromContext returns the W3C trace-id carried by the context, if any.
func TraceIDFromContext(ctx context.Context) string {
	return TraceIDFromTraceparent(TraceparentFromContext(ctx))
}

// GenerateTraceparent generates a compliant, random W3C traceparent header
// string.
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

// EnsureTraceparent returns a context that carries a valid traceparent. An
// existing valid traceparent is preserved; otherwise a new sampled trace is
// generated. The returned string is always the active traceparent.
func EnsureTraceparent(ctx context.Context) (context.Context, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing := TraceparentFromContext(ctx); existing != "" {
		return ctx, existing, nil
	}
	tp, err := GenerateTraceparent(true)
	if err != nil {
		return ctx, "", err
	}
	return ContextWithTraceparent(ctx, tp), tp, nil
}

// AnnotateQueryWithTraceparent formats a SQL query following the sqlcommenter
// standard, appending /*traceparent='...'*/ before any trailing semicolon.
// Invalid traceparents are rejected and the query is returned unchanged, which
// prevents SQL comment injection through a forged header.
func AnnotateQueryWithTraceparent(query, traceparent string) string {
	tp := strings.TrimSpace(traceparent)
	if tp == "" || ValidateTraceparent(tp) != nil {
		return query
	}
	comment := fmt.Sprintf(" /*traceparent='%s'*/", tp)
	trimmed := strings.TrimRight(query, " \t\r\n")
	if strings.HasSuffix(trimmed, ";") {
		return strings.TrimSuffix(trimmed, ";") + comment + ";"
	}
	return query + comment
}
