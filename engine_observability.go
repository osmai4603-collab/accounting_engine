package accounting_engine

import (
	"net/http"

	"accounting_engine/internal/observability"
)

// Metrics exposes the engine's Prometheus metrics registry.
func (e *AccountingEngine) Metrics() *observability.Metrics {
	if e == nil || e.Telemetry == nil {
		return observability.NewMetrics()
	}
	return e.Telemetry.Metrics
}

// MetricsHandler returns the Prometheus /metrics HTTP handler for this engine.
func (e *AccountingEngine) MetricsHandler() http.Handler {
	if e == nil || e.Telemetry == nil {
		return observability.NewMetrics().Handler()
	}
	return e.Telemetry.Handler()
}

// TelemetryMiddleware propagates W3C trace context from incoming HTTP requests
// into the engine's financial operation contexts.
func (e *AccountingEngine) TelemetryMiddleware(next http.Handler) http.Handler {
	if e == nil || e.Telemetry == nil {
		return next
	}
	return e.Telemetry.Middleware(next)
}
