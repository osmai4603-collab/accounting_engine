package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"accounting_engine/internal/observability"
	"github.com/google/uuid"
)

func TestPeriodIDContextLifecycle(t *testing.T) {
	periodID := uuid.New()
	ctx := observability.WithPeriodID(context.Background(), periodID)
	got, ok := observability.PeriodIDFromContext(ctx)
	if !ok || got != periodID {
		t.Fatalf("PeriodIDFromContext = %s, %v; want %s, true", got, ok, periodID)
	}
	if _, ok := observability.PeriodIDFromContext(context.Background()); ok {
		t.Fatal("empty context reported a period id")
	}
	if _, ok := observability.PeriodIDFromContext(nil); ok {
		t.Fatal("nil context reported a period id")
	}
	cleared, ok := observability.PeriodIDFromContext(
		observability.WithPeriodID(ctx, uuid.Nil))
	if ok {
		t.Fatalf("nil uuid was not cleared: %s", cleared)
	}
}

func TestLoggerEnrichesPeriodIDAndPreservesExplicitAttr(t *testing.T) {
	var buffer bytes.Buffer
	logger := observability.NewLogger(&buffer, nil)
	periodID := uuid.New()
	ctx := observability.WithPeriodID(context.Background(), periodID)
	logger.InfoContext(ctx, "posting", "period_id", "explicit")

	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("decode log record: %v\n%s", err, buffer.String())
	}
	if record["period_id"] != "explicit" {
		t.Fatalf("explicit period_id overwritten: %v", record["period_id"])
	}

	buffer.Reset()
	logger.InfoContext(observability.WithPeriodID(context.Background(), periodID), "posting")
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["period_id"] != periodID.String() {
		t.Fatalf("enriched period_id = %v, want %s", record["period_id"], periodID)
	}
}

func TestUnitEnrichBindsFinancialIdentifiers(t *testing.T) {
	var buffer bytes.Buffer
	unit := observability.NewUnit(observability.Config{
		Logger: observability.NewLogger(&buffer, nil),
	})
	companyID, entryID, periodID := uuid.New(), uuid.New(), uuid.New()
	ctx := observability.WithFinancialScope(context.Background(), companyID, entryID, "")
	ctx = observability.WithPeriodID(ctx, periodID)

	logger := unit.Enrich(ctx)
	if logger == nil {
		t.Fatal("Enrich returned nil for a configured logger")
	}
	logger.Info("posting")
	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"company_id": companyID.String(),
		"entry_id":   entryID.String(),
		"period_id":  periodID.String(),
	} {
		if record[key] != want {
			t.Errorf("%s = %v, want %s", key, record[key], want)
		}
	}
	if observability.NewUnit(observability.Config{}).Enrich(ctx) != nil {
		t.Fatal("Enrich should return nil without a logger")
	}
	if (*observability.Unit)(nil).Enrich(ctx) != nil {
		t.Fatal("nil unit Enrich should return nil")
	}
}

func TestPostingTelemetryMetrics(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.IncPostingAttempts()
	metrics.IncPostingAttempts()
	metrics.IncPostedEntries()
	metrics.IncPostingFailure("validation")
	metrics.IncPostingFailure("Validation")
	metrics.IncPostingFailures()
	metrics.ObserveTransactionDuration(0.02)

	if got := metrics.PostingAttempts(); got != 2 {
		t.Fatalf("PostingAttempts = %d, want 2", got)
	}
	if got := metrics.PostedEntries(); got != 1 {
		t.Fatalf("PostedEntries = %d, want 1", got)
	}
	categories := metrics.PostingFailuresByCategory()
	if categories["validation"] != 2 || categories["unknown"] != 1 {
		t.Fatalf("failure categories = %#v", categories)
	}
	if got := metrics.TransactionDurationCount(); got != 1 {
		t.Fatalf("TransactionDurationCount = %d, want 1", got)
	}

	var buffer bytes.Buffer
	if err := metrics.WritePrometheus(&buffer); err != nil {
		t.Fatal(err)
	}
	output := buffer.String()
	for _, want := range []string{
		`accounting_posting_attempts_total 2`,
		`accounting_posting_failures_by_category_total{category="validation"} 2`,
		`accounting_transaction_duration_seconds_count 1`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("metrics exposition missing %q:\n%s", want, output)
		}
	}
}

func TestNilMetricsAccessorsAreSafe(t *testing.T) {
	var metrics *observability.Metrics
	metrics.IncPostingAttempts()
	metrics.IncPostingFailure("validation")
	metrics.IncPostedEntries()
	metrics.ObserveTransactionDuration(1)
	if metrics.PostingAttempts() != 0 || metrics.PostedEntries() != 0 ||
		metrics.TransactionDurationCount() != 0 || metrics.PostingFailuresByCategory() != nil {
		t.Fatal("nil metrics accessor returned a value")
	}
}
