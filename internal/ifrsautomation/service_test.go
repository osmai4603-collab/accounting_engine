package ifrsautomation_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"accounting_engine/internal/ifrsautomation"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func TestSchedulerLogsFailuresAndContinuesWithOtherJobs(t *testing.T) {
	store := newMemoryPostingAdapter()
	runner, err := ifrsautomation.NewRunner(store, store, "test-scheduler")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	period, err := ifrsautomation.Month(2026, time.June)
	if err != nil {
		t.Fatal(err)
	}
	goodJobRan := false
	failingJob := ifrsautomation.JobFunc{
		ID: "a-failing-job",
		Execute: func(context.Context, ifrsautomation.Period) ([]posting.JournalEntry, error) {
			return nil, errors.New("source unavailable")
		},
	}
	goodJob := ifrsautomation.JobFunc{
		ID: "z-good-job",
		Execute: func(context.Context, ifrsautomation.Period) ([]posting.JournalEntry, error) {
			goodJobRan = true
			return nil, nil
		},
	}
	scheduler, err := ifrsautomation.NewSchedulerWithLogger(runner, logger, goodJob, failingJob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.RunPeriod(context.Background(), period); err == nil ||
		!strings.Contains(err.Error(), "source unavailable") {
		t.Fatalf("RunPeriod error = %v, want job failure", err)
	}
	if !goodJobRan {
		t.Fatal("scheduler did not continue after a failed job")
	}
	for _, expected := range []string{"a-failing-job", "z-good-job", "source unavailable", "IFRS automation job completed"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("structured log missing %q: %s", expected, output.String())
		}
	}
}

type memoryPostingAdapter struct {
	entries map[uuid.UUID]posting.JournalEntry
}

func newMemoryPostingAdapter() *memoryPostingAdapter {
	return &memoryPostingAdapter{entries: make(map[uuid.UUID]posting.JournalEntry)}
}

func (m *memoryPostingAdapter) CreateDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	m.entries[entry.ID] = entry
	return nil
}

func (m *memoryPostingAdapter) PostContext(ctx context.Context, companyID, entryID uuid.UUID, posterID string) error {
	entry, ok := m.entries[entryID]
	if !ok {
		return posting.ErrEntryNotFound
	}
	entry.Status = posting.EntryPosted
	m.entries[entryID] = entry
	return nil
}

func (m *memoryPostingAdapter) GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	entry, ok := m.entries[entryID]
	if !ok {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return entry, nil
}

func TestSchedulerRunPeriodAndDaemon(t *testing.T) {
	adapter := newMemoryPostingAdapter()
	runner, err := ifrsautomation.NewRunner(adapter, adapter, "test-scheduler")
	if err != nil {
		t.Fatal(err)
	}

	companyID := uuid.New()
	journalID := uuid.New()
	accA := uuid.New()
	accB := uuid.New()

	amount, _ := financialtypes.NewMoneyFromString("500.00", "SAR")
	zero, _ := financialtypes.Zero("SAR")

	dummyJob := ifrsautomation.JobFunc{
		ID: "dummy-accounting-job",
		Execute: func(ctx context.Context, p ifrsautomation.Period) ([]posting.JournalEntry, error) {
			entry, _ := posting.NewJournalEntry(journalID, p.End, "sched-maker", []posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: accA, Debit: amount, Credit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: accB, Debit: zero, Credit: amount},
			})
			entry.CompanyID = companyID
			entry.ID = uuid.New()
			entry.Reference = "SCHED-JOB-REF-001"
			return []posting.JournalEntry{entry}, nil
		},
	}

	scheduler, err := ifrsautomation.NewScheduler(runner, dummyJob)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Test direct RunPeriod
	period, err := ifrsautomation.Month(2026, time.June)
	if err != nil {
		t.Fatal(err)
	}

	res, err := scheduler.RunPeriod(context.Background(), period)
	if err != nil {
		t.Fatalf("RunPeriod failed: %v", err)
	}
	if len(res.PostedEntryIDs) != 1 {
		t.Fatalf("expected 1 posted entry, got %d", len(res.PostedEntryIDs))
	}

	// 2. Test Daemon lifecycle with context cancellation
	daemonCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- scheduler.StartDaemon(daemonCtx, 10*time.Millisecond)
	}()

	time.Sleep(15 * time.Millisecond)
	if !scheduler.IsRunning() {
		t.Fatal("expected scheduler to report running")
	}

	// Verify preventing duplicate concurrent daemon run
	if err := scheduler.StartDaemon(daemonCtx, 10*time.Millisecond); err == nil {
		t.Fatal("expected ErrSchedulerAlreadyRunning, got nil")
	}

	<-daemonCtx.Done()
	err = <-errChan
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("daemon unexpected exit error: %v", err)
	}

	if scheduler.IsRunning() {
		t.Fatal("expected scheduler to report stopped after cancellation")
	}
}
