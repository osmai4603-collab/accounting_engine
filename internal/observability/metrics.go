package observability

import (
	"fmt"
	"io"
	"net/http"
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

	postedEntries   uint64
	postingFailures uint64

	makerCheckerPending int64

	reconciliationDiscrepancies int64
}

// NewMetrics creates an empty metrics registry with the default histogram
// buckets.
func NewMetrics() *Metrics {
	buckets := append([]float64(nil), DefaultPostingDurationBuckets...)
	return &Metrics{
		buckets:      buckets,
		bucketCounts: make([]uint64, len(buckets)),
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

// IncPostedEntries records one successfully posted journal entry.
func (m *Metrics) IncPostedEntries() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.postedEntries++
	m.mu.Unlock()
}

// IncPostingFailures records one rejected or failed posting attempt.
func (m *Metrics) IncPostingFailures() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.postingFailures++
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

	builder.WriteString("# HELP accounting_posting_failures_total Total number of rejected or failed posting attempts.\n")
	builder.WriteString("# TYPE accounting_posting_failures_total counter\n")
	fmt.Fprintf(&builder, "accounting_posting_failures_total %d\n", m.postingFailures)

	builder.WriteString("# HELP accounting_maker_checker_pending_total Journal entries currently pending maker-checker approval.\n")
	builder.WriteString("# TYPE accounting_maker_checker_pending_total gauge\n")
	fmt.Fprintf(&builder, "accounting_maker_checker_pending_total %d\n", m.makerCheckerPending)

	builder.WriteString("# HELP accounting_reconciliation_discrepancy_count Accounts whose materialized balance differs from the ledger.\n")
	builder.WriteString("# TYPE accounting_reconciliation_discrepancy_count gauge\n")
	fmt.Fprintf(&builder, "accounting_reconciliation_discrepancy_count %d\n", m.reconciliationDiscrepancies)

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
