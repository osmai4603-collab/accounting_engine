package observability

import (
	"context"
	"io"
	"log/slog"
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
	traceID := TraceIDFromContext(ctx)

	present := make(map[string]struct{}, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		present[attr.Key] = struct{}{}
		return true
	})

	if hasCompany {
		if _, ok := present["company_id"]; !ok {
			record.AddAttrs(slog.String("company_id", companyID.String()))
		}
	}
	if hasEntry {
		if _, ok := present["entry_id"]; !ok {
			record.AddAttrs(slog.String("entry_id", entryID.String()))
		}
	}
	if traceID != "" {
		if _, ok := present["trace_id"]; !ok {
			record.AddAttrs(slog.String("trace_id", traceID))
		}
	}
	return h.next.Handle(ctx, record)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name)}
}
