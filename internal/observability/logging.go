package observability

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// Handler decorates a slog.Handler and automatically injects the financial
// correlation identifiers (company_id, trace_id, entry_id) carried by the
// context of each record.
type Handler struct {
	next slog.Handler
}

// NewJSONHandler returns a structured JSON slog handler that enriches every
// record with the identifiers found in its context.
func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &Handler{next: slog.NewJSONHandler(w, opts)}
}

// NewLogger builds a JSON logger with automatic financial context enrichment.
func NewLogger(w io.Writer, opts *slog.HandlerOptions) *slog.Logger {
	return slog.New(NewJSONHandler(w, opts))
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	companyID, hasCompany := CompanyIDFromContext(ctx)
	entryID, hasEntry := EntryIDFromContext(ctx)
	periodID, hasPeriod := PeriodIDFromContext(ctx)
	traceID := TraceIDFromContext(ctx)

	present := make(map[string]struct{}, record.NumAttrs())
	sanitizedAttrs := make([]slog.Attr, 0, record.NumAttrs()+4)

	record.Attrs(func(attr slog.Attr) bool {
		present[attr.Key] = struct{}{}
		sanitizedAttrs = append(sanitizedAttrs, sanitizeAttr(attr))
		return true
	})

	// Replace record with sanitized attrs
	sanitizedRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	for _, attr := range sanitizedAttrs {
		sanitizedRecord.AddAttrs(attr)
	}

	if hasCompany {
		if _, ok := present["company_id"]; !ok {
			sanitizedRecord.AddAttrs(slog.String("company_id", companyID.String()))
		}
	}
	if hasEntry {
		if _, ok := present["entry_id"]; !ok {
			sanitizedRecord.AddAttrs(slog.String("entry_id", entryID.String()))
		}
	}
	if hasPeriod {
		if _, ok := present["period_id"]; !ok {
			sanitizedRecord.AddAttrs(slog.String("period_id", periodID.String()))
		}
	}
	if traceID != "" {
		if _, ok := present["trace_id"]; !ok {
			sanitizedRecord.AddAttrs(slog.String("trace_id", traceID))
		}
	}
	return h.next.Handle(ctx, sanitizedRecord)
}

func sanitizeAttr(attr slog.Attr) slog.Attr {
	key := strings.ToLower(strings.TrimSpace(attr.Key))
	switch key {
	case "password", "token", "secret", "authorization", "auth", "api_key", "apikey",
		"private_key", "privatekey", "credit_card", "card_number", "iban", "cvv":
		return slog.String(attr.Key, "[REDACTED]")
	}
	return attr
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name)}
}
