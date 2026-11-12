package observability

import (
	"context"
	"log/slog"
	"net/http"
)

// Config configures the observability unit. Both fields are optional; a nil
// logger disables structured posting logs and a nil metrics registry is
// replaced with a fresh one.
type Config struct {
	Logger  *slog.Logger
	Metrics *Metrics
}

// Unit bundles the logger and metrics registry used by an engine instance.
type Unit struct {
	Logger  *slog.Logger
	Metrics *Metrics
}

// NewUnit builds an observability unit, filling in a default metrics registry.
func NewUnit(config Config) *Unit {
	metrics := config.Metrics
	if metrics == nil {
		metrics = NewMetrics()
	}
	return &Unit{Logger: config.Logger, Metrics: metrics}
}

// Handler exposes the unit's Prometheus metrics endpoint.
func (u *Unit) Handler() http.Handler {
	if u == nil || u.Metrics == nil {
		return NewMetrics().Handler()
	}
	return u.Metrics.Handler()
}

// Middleware propagates W3C trace context from an incoming request. A valid
// traceparent header is preserved; otherwise a new sampled trace is generated.
// The active traceparent is echoed in the response header.
func (u *Unit) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tp := r.Header.Get("traceparent")
		if ValidateTraceparent(tp) != nil {
			generated, err := GenerateTraceparent(true)
			if err == nil {
				tp = generated
			} else {
				tp = ""
			}
		}
		if tp != "" {
			ctx = ContextWithTraceparent(ctx, tp)
			w.Header().Set("traceparent", tp)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Enrich returns a logger pre-bound to the financial correlation identifiers
// found in ctx. It returns nil when the unit has no logger.
func (u *Unit) Enrich(ctx context.Context) *slog.Logger {
	if u == nil || u.Logger == nil {
		return nil
	}
	attrs := make([]any, 0, 6)
	if companyID, ok := CompanyIDFromContext(ctx); ok {
		attrs = append(attrs, "company_id", companyID.String())
	}
	if entryID, ok := EntryIDFromContext(ctx); ok {
		attrs = append(attrs, "entry_id", entryID.String())
	}
	if traceID := TraceIDFromContext(ctx); traceID != "" {
		attrs = append(attrs, "trace_id", traceID)
	}
	return u.Logger.With(attrs...)
}
