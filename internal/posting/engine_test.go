package posting

import (
	"sync"
	"testing"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func money(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func balancedEntry(t *testing.T, creator string) JournalEntry {
	t.Helper()
	entry, err := NewJournalEntry(uuid.New(), utcDate(t, 2026, 1, 15), creator, []JournalEntryLine{
		{AccountID: uuid.New(), Debit: money(t, "100.0000", "USD"), Credit: money(t, "0", "USD")},
		{AccountID: uuid.New(), Credit: money(t, "100.0000", "USD"), Debit: money(t, "0", "USD")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func utcDate(t *testing.T, year, month, day int) time.Time {
	t.Helper()
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func TestRejectsUnbalancedEntry(t *testing.T) {
	entry := balancedEntry(t, "maker")
	entry.Lines[1].Credit = money(t, "99", "USD")
	if err := entry.Validate(); err != financialtypes.ErrUnbalancedMove {
		t.Fatalf("expected unbalanced move, got %v", err)
	}
}

func TestMinimumLinesAndSingleSide(t *testing.T) {
	entry := JournalEntry{Lines: []JournalEntryLine{{AccountID: uuid.New()}}}
	if err := entry.Validate(); err != ErrMinimumLines {
		t.Fatalf("expected minimum-lines error, got %v", err)
	}

	entry = JournalEntry{Lines: []JournalEntryLine{
		{AccountID: uuid.New(), Debit: money(t, "10", "USD"), Credit: money(t, "10", "USD")},
		{AccountID: uuid.New(), Debit: money(t, "0", "USD"), Credit: money(t, "10", "USD")},
	}}
	if err := entry.Validate(); err != ErrLineMustHaveOneSide {
		t.Fatalf("expected single-side error, got %v", err)
	}
}

func TestPostedEntryCannotBeModified(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	entry := balancedEntry(t, "maker")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.GetEntryByID(entry.ID)
	stored.Status = EntryDraft
	if err := repo.SaveDraft(stored); err != financialtypes.ErrCannotModifyPostedMove {
		t.Fatalf("expected immutability error, got %v", err)
	}
}

func TestMakerChecker(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	engine.SetMakerCheckerThreshold(decimal.RequireFromString("500"))
	entry := balancedEntry(t, "maker")
	entry.Lines[0].Debit = money(t, "600", "USD")
	entry.Lines[1].Credit = money(t, "600", "USD")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.ID, "maker"); err != financialtypes.ErrMakerCheckerViolation {
		t.Fatalf("expected maker-checker violation, got %v", err)
	}
	if err := engine.Post(entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSequences(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	journalID := uuid.New()
	const count = 50

	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		entry := balancedEntry(t, "maker")
		entry.JournalID = journalID
		if err := engine.CreateDraft(entry); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			errs <- engine.Post(id, "checker")
		}(entry.ID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	seen := make(map[int64]bool)
	for _, entry := range repo.entries {
		if entry.Status != EntryPosted {
			continue
		}
		if seen[entry.SequenceNumber] {
			t.Fatalf("duplicate sequence %d", entry.SequenceNumber)
		}
		seen[entry.SequenceNumber] = true
	}
}

func TestReversal(t *testing.T) {
	repo := NewMockRepository()
	engine := NewEngine(repo, nil)
	entry := balancedEntry(t, "maker")
	if err := engine.CreateDraft(entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.Post(entry.ID, "checker"); err != nil {
		t.Fatal(err)
	}
	reversal, err := engine.Reverse(entry.ID, "checker")
	if err != nil {
		t.Fatal(err)
	}
	storedOriginal, err := repo.GetEntryByID(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOriginal.Status != EntryReversed || storedOriginal.ReversedEntryID == nil ||
		*storedOriginal.ReversedEntryID != reversal.ID {
		t.Fatalf("original entry was not linked to reversal: %#v", storedOriginal)
	}
	if reversal.Status != EntryPosted || reversal.OriginalEntryID == nil ||
		*reversal.OriginalEntryID != entry.ID {
		t.Fatalf("invalid reversal: %#v", reversal)
	}
}
