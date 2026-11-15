package observability

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultPostingDurationBuckets are the latency buckets (seconds) used by the
// accounting_posting_duration_seconds histogram.
var DefaultPostingDurationBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Metrics is a dependency-free, concurrency-safe Prometheus metrics registry
// for the accounting engine.
type Metrics struct {
	mu sync.Mutex

	buckets      []float64
	bucketCounts []uint64
	overflow     uint64
	durationSum  float64
	durationN    uint64

	// transactionDuration tracks the full unit of work that contains one or
	// more journal postings (a single post, an atomic attachment post, or an
	// atomic bulk batch).
	transactionBucketCounts []uint64
	transactionOverflow     uint64
	transactionSum          float64
	transactionN            uint64

	postingAttempts           uint64
	postedEntries             uint64
	postingFailures           uint64
	postingFailuresByCategory map[string]uint64

	reversalAttempts uint64
	reversedEntries  uint64
	reversalFailures uint64

	eventsReceived  uint64
	eventsProcessed uint64
	eventsFailed    uint64

	validationErrors uint64
	conflictErrors   uint64
	dbErrors         uint64

	deadlocksDetected  uint64
	transactionRetries uint64

	outboxPendingCount int64
	outboxLagSeconds   float64

	makerCheckerPending int64

	reconciliationDiscrepancies int64
}

// NewMetrics creates an empty metrics registry with the default histogram
// buckets.
func NewMetrics() *Metrics {
	buckets := append([]float64(nil), DefaultPostingDurationBuckets...)
	return &Metrics{
		buckets:                   buckets,
		bucketCounts:              make([]uint64, len(buckets)),
		transactionBucketCounts:   make([]uint64, len(buckets)),
		postingFailuresByCategory: make(map[string]uint64),
	}
}

// ObservePostingDuration records one posting latency observation in seconds.
func (m *Metrics) ObservePostingDuration(seconds float64) {
	if m == nil {
		return
	}
	if seconds < 0 {
		seconds = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index := len(m.buckets)
	for i, upper := range m.buckets {
		if seconds <= upper {
			index = i
			break
		}
	}
	if index == len(m.buckets) {
		m.overflow++
	} else {
		m.bucketCounts[index]++
	}
	m.durationSum += seconds
	m.durationN++
}

// ObserveTransactionDuration records the latency of a complete posting
// transaction (all statements, locks, and attachments) in seconds.
func (m *Metrics) ObserveTransactionDuration(seconds float64) {
	if m == nil {
		return
	}
	if seconds < 0 {
		seconds = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index := len(m.buckets)
	for i, upper := range m.buckets {
		if seconds <= upper {
			index = i
			break
		}
	}
	if index == len(m.buckets) {
		m.transactionOverflow++
	} else {
		m.transactionBucketCounts[index]++
	}
	m.transactionSum += seconds
	m.transactionN++
}

// IncPostingAttempts records one posting attempt before its outcome is known.
func (m *Metrics) IncPostingAttempts() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.postingAttempts++
	m.mu.Unlock()
}

// IncPostedEntries records one successfully posted journal entry.
func (m *Metrics) IncPostedEntries() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.postedEntries++
	m.mu.Unlock()
}

// IncPostingFailures records one rejected or failed posting attempt without a
// category. Prefer IncPostingFailure so failures stay classifiable.
func (m *Metrics) IncPostingFailures() {
	m.IncPostingFailure("unknown")
}

// IncPostingFailure records one failed posting attempt under a taxonomy
// category (validation, conflict, authorization, infrastructure, invariant).
func (m *Metrics) IncPostingFailure(category string) {
	if m == nil {
		return
	}
	category = strings.TrimSpace(strings.ToLower(category))
	if category == "" {
		category = "unknown"
	}
	m.mu.Lock()
	m.postingFailures++
	m.postingFailuresByCategory[category]++
	m.mu.Unlock()
}

// IncMakerCheckerPending adds one entry to the pending-approval gauge.
func (m *Metrics) IncMakerCheckerPending() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.makerCheckerPending++
	m.mu.Unlock()
}

// DecMakerCheckerPending removes one entry from the pending-approval gauge. The
// gauge never becomes negative.
func (m *Metrics) DecMakerCheckerPending() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.makerCheckerPending > 0 {
		m.makerCheckerPending--
	}
	m.mu.Unlock()
}

// SetMakerCheckerPending replaces the pending-approval gauge.
func (m *Metrics) SetMakerCheckerPending(value int64) {
	if m == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	m.mu.Lock()
	m.makerCheckerPending = value
	m.mu.Unlock()
}

// SetReconciliationDiscrepancyCount replaces the reconciliation discrepancy
// gauge with the number of accounts that drifted.
func (m *Metrics) SetReconciliationDiscrepancyCount(count int) {
	if m == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	m.mu.Lock()
	m.reconciliationDiscrepancies = int64(count)
	m.mu.Unlock()
}

// PostingDurationCount returns the number of posting observations.
func (m *Metrics) PostingDurationCount() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.durationN
}

// TransactionDurationCount returns the number of transaction observations.
func (m *Metrics) TransactionDurationCount() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transactionN
}

// PostingAttempts returns the number of posting attempts started.
func (m *Metrics) PostingAttempts() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.postingAttempts
}

// PostingFailuresByCategory returns a copy of the per-category failure counts.
func (m *Metrics) PostingFailuresByCategory() map[string]uint64 {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	categories := make(map[string]uint64, len(m.postingFailuresByCategory))
	for category, count := range m.postingFailuresByCategory {
		categories[category] = count
	}
	return categories
}

// PostedEntries returns the number of successfully posted entries.
func (m *Metrics) PostedEntries() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.postedEntries
}

// MakerCheckerPending returns the current pending-approval gauge.
func (m *Metrics) MakerCheckerPending() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.makerCheckerPending
}

// ReconciliationDiscrepancyCount returns the current reconciliation gauge.
func (m *Metrics) ReconciliationDiscrepancyCount() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconciliationDiscrepancies
}

// Reversal metrics
func (m *Metrics) IncReversalAttempts() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.reversalAttempts++
	m.mu.Unlock()
}

func (m *Metrics) IncReversedEntries() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.reversedEntries++
	m.mu.Unlock()
}

func (m *Metrics) IncReversalFailures() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.reversalFailures++
	m.mu.Unlock()
}

func (m *Metrics) ReversalAttempts() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reversalAttempts
}

func (m *Metrics) ReversedEntries() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reversedEntries
}

func (m *Metrics) ReversalFailures() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reversalFailures
}

// Ingestion event metrics
func (m *Metrics) IncEventsReceived() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.eventsReceived++
	m.mu.Unlock()
}

func (m *Metrics) IncEventsProcessed() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.eventsProcessed++
	m.mu.Unlock()
}

func (m *Metrics) IncEventsFailed() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.eventsFailed++
	m.mu.Unlock()
}

func (m *Metrics) EventsReceived() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventsReceived
}

func (m *Metrics) EventsProcessed() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventsProcessed
}

func (m *Metrics) EventsFailed() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventsFailed
}

// Error taxonomy counters
func (m *Metrics) IncValidationError() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.validationErrors++
	m.mu.Unlock()
}

func (m *Metrics) IncConflictError() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.conflictErrors++
	m.mu.Unlock()
}

func (m *Metrics) IncDBError() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.dbErrors++
	m.mu.Unlock()
}

func (m *Metrics) ValidationErrors() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.validationErrors
}

func (m *Metrics) ConflictErrors() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conflictErrors
}

func (m *Metrics) DBErrors() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dbErrors
}

// Deadlock & retry metrics
func (m *Metrics) IncDeadlock() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.deadlocksDetected++
	m.mu.Unlock()
}

func (m *Metrics) IncTransactionRetry() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.transactionRetries++
	m.mu.Unlock()
}

func (m *Metrics) DeadlocksDetected() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deadlocksDetected
}

func (m *Metrics) TransactionRetries() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transactionRetries
}

// Outbox metrics
func (m *Metrics) SetOutboxPendingCount(count int64) {
	if m == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	m.mu.Lock()
	m.outboxPendingCount = count
	m.mu.Unlock()
}

func (m *Metrics) SetOutboxLagSeconds(seconds float64) {
	if m == nil {
		return
	}
	if seconds < 0 {
		seconds = 0
	}
	m.mu.Lock()
	m.outboxLagSeconds = seconds
	m.mu.Unlock()
}

func (m *Metrics) OutboxPendingCount() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outboxPendingCount
}

func (m *Metrics) OutboxLagSeconds() float64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outboxLagSeconds
}

// WritePrometheus writes the registry in the Prometheus text exposition format
// (version 0.0.4).
func (m *Metrics) WritePrometheus(w io.Writer) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var builder strings.Builder
	builder.WriteString("# HELP accounting_posting_duration_seconds Time taken to post a journal entry, in seconds.\n")
	builder.WriteString("# TYPE accounting_posting_duration_seconds histogram\n")
	cumulative := uint64(0)
	for i, upper := range m.buckets {
		cumulative += m.bucketCounts[i]
		fmt.Fprintf(&builder, "accounting_posting_duration_seconds_bucket{le=%q} %d\n",
			formatFloat(upper), cumulative)
	}
	cumulative += m.overflow
	fmt.Fprintf(&builder, "accounting_posting_duration_seconds_bucket{le=\"+Inf\"} %d\n", cumulative)
	fmt.Fprintf(&builder, "accounting_posting_duration_seconds_sum %s\n", formatFloat(m.durationSum))
	fmt.Fprintf(&builder, "accounting_posting_duration_seconds_count %d\n", m.durationN)

	builder.WriteString("# HELP accounting_posted_entries_total Total number of successfully posted journal entries.\n")
	builder.WriteString("# TYPE accounting_posted_entries_total counter\n")
	fmt.Fprintf(&builder, "accounting_posted_entries_total %d\n", m.postedEntries)

	builder.WriteString("# HELP accounting_posting_attempts_total Total number of posting attempts started.\n")
	builder.WriteString("# TYPE accounting_posting_attempts_total counter\n")
	fmt.Fprintf(&builder, "accounting_posting_attempts_total %d\n", m.postingAttempts)

	builder.WriteString("# HELP accounting_posting_failures_total Total number of rejected or failed posting attempts.\n")
	builder.WriteString("# TYPE accounting_posting_failures_total counter\n")
	fmt.Fprintf(&builder, "accounting_posting_failures_total %d\n", m.postingFailures)

	builder.WriteString("# HELP accounting_posting_failures_by_category_total Failed posting attempts grouped by error taxonomy category.\n")
	builder.WriteString("# TYPE accounting_posting_failures_by_category_total counter\n")
	categories := make([]string, 0, len(m.postingFailuresByCategory))
	for category := range m.postingFailuresByCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		fmt.Fprintf(&builder, "accounting_posting_failures_by_category_total{category=%q} %d\n",
			category, m.postingFailuresByCategory[category])
	}

	builder.WriteString("# HELP accounting_transaction_duration_seconds Time taken by a complete posting transaction, in seconds.\n")
	builder.WriteString("# TYPE accounting_transaction_duration_seconds histogram\n")
	cumulative = 0
	for i, upper := range m.buckets {
		cumulative += m.transactionBucketCounts[i]
		fmt.Fprintf(&builder, "accounting_transaction_duration_seconds_bucket{le=%q} %d\n",
			formatFloat(upper), cumulative)
	}
	cumulative += m.transactionOverflow
	fmt.Fprintf(&builder, "accounting_transaction_duration_seconds_bucket{le=\"+Inf\"} %d\n", cumulative)
	fmt.Fprintf(&builder, "accounting_transaction_duration_seconds_sum %s\n", formatFloat(m.transactionSum))
	fmt.Fprintf(&builder, "accounting_transaction_duration_seconds_count %d\n", m.transactionN)

	builder.WriteString("# HELP accounting_maker_checker_pending_total Journal entries currently pending maker-checker approval.\n")
	builder.WriteString("# TYPE accounting_maker_checker_pending_total gauge\n")
	fmt.Fprintf(&builder, "accounting_maker_checker_pending_total %d\n", m.makerCheckerPending)

	builder.WriteString("# HELP accounting_reconciliation_discrepancy_count Accounts whose materialized balance differs from the ledger.\n")
	builder.WriteString("# TYPE accounting_reconciliation_discrepancy_count gauge\n")
	fmt.Fprintf(&builder, "accounting_reconciliation_discrepancy_count %d\n", m.reconciliationDiscrepancies)

	// Reversal metrics
	builder.WriteString("# HELP accounting_reversal_attempts_total Total number of entry reversal attempts.\n")
	builder.WriteString("# TYPE accounting_reversal_attempts_total counter\n")
	fmt.Fprintf(&builder, "accounting_reversal_attempts_total %d\n", m.reversalAttempts)

	builder.WriteString("# HELP accounting_reversed_entries_total Total number of successfully reversed journal entries.\n")
	builder.WriteString("# TYPE accounting_reversed_entries_total counter\n")
	fmt.Fprintf(&builder, "accounting_reversed_entries_total %d\n", m.reversedEntries)

	builder.WriteString("# HELP accounting_reversal_failures_total Total number of failed reversal attempts.\n")
	builder.WriteString("# TYPE accounting_reversal_failures_total counter\n")
	fmt.Fprintf(&builder, "accounting_reversal_failures_total %d\n", m.reversalFailures)

	// Ingestion event metrics
	builder.WriteString("# HELP accounting_events_received_total Total number of external economic events received.\n")
	builder.WriteString("# TYPE accounting_events_received_total counter\n")
	fmt.Fprintf(&builder, "accounting_events_received_total %d\n", m.eventsReceived)

	builder.WriteString("# HELP accounting_events_processed_total Total number of external economic events processed into entries.\n")
	builder.WriteString("# TYPE accounting_events_processed_total counter\n")
	fmt.Fprintf(&builder, "accounting_events_processed_total %d\n", m.eventsProcessed)

	builder.WriteString("# HELP accounting_events_failed_total Total number of external economic events that failed processing.\n")
	builder.WriteString("# TYPE accounting_events_failed_total counter\n")
	fmt.Fprintf(&builder, "accounting_events_failed_total %d\n", m.eventsFailed)

	// Error classification counters
	builder.WriteString("# HELP accounting_validation_errors_total Total input validation errors.\n")
	builder.WriteString("# TYPE accounting_validation_errors_total counter\n")
	fmt.Fprintf(&builder, "accounting_validation_errors_total %d\n", m.validationErrors)

	builder.WriteString("# HELP accounting_conflict_errors_total Total concurrency or idempotency conflict errors.\n")
	builder.WriteString("# TYPE accounting_conflict_errors_total counter\n")
	fmt.Fprintf(&builder, "accounting_conflict_errors_total %d\n", m.conflictErrors)

	builder.WriteString("# HELP accounting_db_errors_total Total storage backend errors.\n")
	builder.WriteString("# TYPE accounting_db_errors_total counter\n")
	fmt.Fprintf(&builder, "accounting_db_errors_total %d\n", m.dbErrors)

	// Deadlock & retry metrics
	builder.WriteString("# HELP accounting_deadlocks_detected_total Total transaction deadlocks detected by storage.\n")
	builder.WriteString("# TYPE accounting_deadlocks_detected_total counter\n")
	fmt.Fprintf(&builder, "accounting_deadlocks_detected_total %d\n", m.deadlocksDetected)

	builder.WriteString("# HELP accounting_transaction_retries_total Total storage transaction retries executed.\n")
	builder.WriteString("# TYPE accounting_transaction_retries_total counter\n")
	fmt.Fprintf(&builder, "accounting_transaction_retries_total %d\n", m.transactionRetries)

	// Outbox metrics
	builder.WriteString("# HELP accounting_outbox_pending_count Total entries pending in transactional outbox.\n")
	builder.WriteString("# TYPE accounting_outbox_pending_count gauge\n")
	fmt.Fprintf(&builder, "accounting_outbox_pending_count %d\n", m.outboxPendingCount)

	builder.WriteString("# HELP accounting_outbox_lag_seconds Current lag in seconds of transactional outbox dispatch.\n")
	builder.WriteString("# TYPE accounting_outbox_lag_seconds gauge\n")
	fmt.Fprintf(&builder, "accounting_outbox_lag_seconds %s\n", formatFloat(m.outboxLagSeconds))

	_, err := io.WriteString(w, builder.String())
	return err
}

// Handler exposes the metrics registry as an HTTP endpoint suitable for a
// Prometheus scrape configuration.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = m.WritePrometheus(w)
	})
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
