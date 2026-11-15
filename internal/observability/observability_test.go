package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/observability"
)

func TestTraceparentLifecycle(t *testing.T) {
	generated, err := observability.GenerateTraceparent(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := observability.ValidateTraceparent(generated); err != nil {
		t.Fatalf("generated traceparent is invalid: %v", err)
	}
	if got := observability.TraceIDFromTraceparent(generated); len(got) != 32 {
		t.Fatalf("trace id length = %d, want 32", len(got))
	}

	ctx := observability.ContextWithTraceparent(context.Background(), generated)
	if got := observability.TraceparentFromContext(ctx); got != generated {
		t.Fatalf("traceparent from context = %q, want %q", got, generated)
	}
	if got := observability.TraceIDFromContext(ctx); got != observability.TraceIDFromTraceparent(generated) {
		t.Fatalf("trace id from context = %q", got)
	}

	ensured, tp, err := observability.EnsureTraceparent(context.Background())
	if err != nil || tp == "" {
		t.Fatalf("ensure traceparent = %q, %v", tp, err)
	}
	if got := observability.TraceparentFromContext(ensured); got != tp {
		t.Fatalf("ensured context traceparent = %q, want %q", got, tp)
	}
	preserved, preservedTP, err := observability.EnsureTraceparent(ctx)
	if err != nil || preservedTP != generated {
		t.Fatalf("existing traceparent not preserved: %q, %v", preservedTP, err)
	}
	if got := observability.TraceparentFromContext(preserved); got != generated {
		t.Fatalf("preserved context traceparent = %q, want %q", got, generated)
	}
}

func TestTraceparentRejectsInjectionAndMalformedValues(t *testing.T) {
	ctx := observability.ContextWithTraceparent(context.Background(),
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01*/ DROP TABLE account_moves; --")
	if got := observability.TraceparentFromContext(ctx); got != "" {
		t.Fatalf("malformed traceparent was stored: %q", got)
	}

	query := "SELECT 1;"
	if got := observability.AnnotateQueryWithTraceparent(query, "not-a-traceparent"); got != query {
		t.Fatalf("invalid traceparent annotated the query: %q", got)
	}
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	got := observability.AnnotateQueryWithTraceparent(query, valid)
	if !strings.Contains(got, "/*traceparent='"+valid+"'*/") || !strings.HasSuffix(got, ";") {
		t.Fatalf("valid annotation = %q", got)
	}
}

func TestStructuredLoggerEnrichesFinancialContext(t *testing.T) {
	var buffer bytes.Buffer
	logger := observability.NewLogger(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})

	companyID, entryID := uuid.New(), uuid.New()
	tp, err := observability.GenerateTraceparent(true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := observability.WithFinancialScope(context.Background(), companyID, entryID, tp)
	logger.InfoContext(ctx, "journal entry posted", "amount", "100.00")

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buffer.Bytes()), &record); err != nil {
		t.Fatalf("decode structured log: %v (raw %q)", err, buffer.String())
	}
	if record["company_id"] != companyID.String() {
		t.Errorf("company_id = %v, want %s", record["company_id"], companyID)
	}
	if record["entry_id"] != entryID.String() {
		t.Errorf("entry_id = %v, want %s", record["entry_id"], entryID)
	}
	if record["trace_id"] != observability.TraceIDFromTraceparent(tp) {
		t.Errorf("trace_id = %v, want %s", record["trace_id"], observability.TraceIDFromTraceparent(tp))
	}
	if record["msg"] != "journal entry posted" {
		t.Errorf("msg = %v", record["msg"])
	}
}

func TestMetricsPrometheusExposition(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.ObservePostingDuration(0.002)
	metrics.ObservePostingDuration(0.3)
	metrics.ObservePostingDuration(30)
	metrics.IncPostedEntries()
	metrics.IncPostedEntries()
	metrics.IncPostingFailures()
	metrics.IncMakerCheckerPending()
	metrics.IncMakerCheckerPending()
	metrics.DecMakerCheckerPending()
	metrics.SetReconciliationDiscrepancyCount(3)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("content type = %q", contentType)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"# TYPE accounting_posting_duration_seconds histogram",
		`accounting_posting_duration_seconds_bucket{le="0.001"} 0`,
		`accounting_posting_duration_seconds_bucket{le="0.005"} 1`,
		`accounting_posting_duration_seconds_bucket{le="0.5"} 2`,
		`accounting_posting_duration_seconds_bucket{le="+Inf"} 3`,
		"accounting_posting_duration_seconds_count 3",
		"accounting_posted_entries_total 2",
		"accounting_posting_failures_total 1",
		"accounting_maker_checker_pending_total 1",
		"accounting_reconciliation_discrepancy_count 3",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q:\n%s", want, body)
		}
	}
	if metrics.PostingDurationCount() != 3 || metrics.PostedEntries() != 2 ||
		metrics.MakerCheckerPending() != 1 || metrics.ReconciliationDiscrepancyCount() != 3 {
		t.Fatalf("metric accessors do not match observations")
	}

	postRequest := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	postRecorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(postRecorder, postRequest)
	if postRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", postRecorder.Code)
	}
}

func TestMetricsAreConcurrencySafe(t *testing.T) {
	metrics := observability.NewMetrics()
	const workers = 32
	const observations = 100
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range observations {
				metrics.ObservePostingDuration(0.01)
				metrics.IncPostedEntries()
				metrics.IncMakerCheckerPending()
				metrics.DecMakerCheckerPending()
			}
		}()
	}
	wait.Wait()
	if metrics.PostingDurationCount() != workers*observations {
		t.Fatalf("observations = %d, want %d", metrics.PostingDurationCount(), workers*observations)
	}
	if metrics.PostedEntries() != workers*observations {
		t.Fatalf("posted entries = %d, want %d", metrics.PostedEntries(), workers*observations)
	}
	if metrics.MakerCheckerPending() != 0 {
		t.Fatalf("pending gauge = %d, want 0", metrics.MakerCheckerPending())
	}
}

func TestMiddlewarePropagatesTraceContext(t *testing.T) {
	unit := observability.NewUnit(observability.Config{})
	var observed string
	handler := unit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = observability.TraceparentFromContext(r.Context())
	}))

	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("traceparent", valid)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if observed != valid {
		t.Fatalf("observed traceparent = %q, want %q", observed, valid)
	}
	if recorder.Header().Get("traceparent") != valid {
		t.Fatalf("response traceparent = %q", recorder.Header().Get("traceparent"))
	}

	observed = ""
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if err := observability.ValidateTraceparent(observed); err != nil {
		t.Fatalf("generated traceparent is invalid: %v", err)
	}
}

func TestSensitiveDataRedactionInStructuredLogs(t *testing.T) {
	var buffer bytes.Buffer
	logger := observability.NewLogger(&buffer, nil)

	logger.Info("user authentication attempt",
		"password", "SuperSecret123!",
		"token", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"secret", "whsec_abcdef123456",
		"credit_card", "4111111111111111",
		"normal_field", "visible_value",
	)

	logged := buffer.String()
	if strings.Contains(logged, "SuperSecret123!") {
		t.Errorf("log leaked sensitive password: %s", logged)
	}
	if strings.Contains(logged, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Errorf("log leaked sensitive token: %s", logged)
	}
	if strings.Contains(logged, "whsec_abcdef123456") {
		t.Errorf("log leaked webhook secret: %s", logged)
	}
	if strings.Contains(logged, "4111111111111111") {
		t.Errorf("log leaked credit card: %s", logged)
	}
	if !strings.Contains(logged, "[REDACTED]") {
		t.Errorf("log missing [REDACTED] placeholder: %s", logged)
	}
	if !strings.Contains(logged, "visible_value") {
		t.Errorf("log did not preserve non-sensitive fields: %s", logged)
	}
}

func TestExtendedOperationalMetrics(t *testing.T) {
	metrics := observability.NewMetrics()

	// Test reversals
	metrics.IncReversalAttempts()
	metrics.IncReversedEntries()
	metrics.IncReversalFailures()
	if metrics.ReversalAttempts() != 1 || metrics.ReversedEntries() != 1 || metrics.ReversalFailures() != 1 {
		t.Errorf("reversal metrics mismatch")
	}

	// Test ingestion events
	metrics.IncEventsReceived()
	metrics.IncEventsProcessed()
	metrics.IncEventsFailed()
	if metrics.EventsReceived() != 1 || metrics.EventsProcessed() != 1 || metrics.EventsFailed() != 1 {
		t.Errorf("event metrics mismatch")
	}

	// Test errors
	metrics.IncValidationError()
	metrics.IncConflictError()
	metrics.IncDBError()
	if metrics.ValidationErrors() != 1 || metrics.ConflictErrors() != 1 || metrics.DBErrors() != 1 {
		t.Errorf("error counters mismatch")
	}

	// Test deadlocks & retries
	metrics.IncDeadlock()
	metrics.IncTransactionRetry()
	if metrics.DeadlocksDetected() != 1 || metrics.TransactionRetries() != 1 {
		t.Errorf("deadlock/retry metrics mismatch")
	}

	// Test outbox
	metrics.SetOutboxPendingCount(42)
	metrics.SetOutboxLagSeconds(3.5)
	if metrics.OutboxPendingCount() != 42 || metrics.OutboxLagSeconds() != 3.5 {
		t.Errorf("outbox metrics mismatch")
	}

	// Verify Prometheus output contains new metrics
	var buf bytes.Buffer
	if err := metrics.WritePrometheus(&buf); err != nil {
		t.Fatalf("failed to write prometheus output: %v", err)
	}
	output := buf.String()
	for _, expectedMetric := range []string{
		"accounting_reversal_attempts_total 1",
		"accounting_reversed_entries_total 1",
		"accounting_reversal_failures_total 1",
		"accounting_events_received_total 1",
		"accounting_events_processed_total 1",
		"accounting_events_failed_total 1",
		"accounting_validation_errors_total 1",
		"accounting_conflict_errors_total 1",
		"accounting_db_errors_total 1",
		"accounting_deadlocks_detected_total 1",
		"accounting_transaction_retries_total 1",
		"accounting_outbox_pending_count 42",
		"accounting_outbox_lag_seconds 3.5",
	} {
		if !strings.Contains(output, expectedMetric) {
			t.Errorf("prometheus output missing %q", expectedMetric)
		}
	}
}
