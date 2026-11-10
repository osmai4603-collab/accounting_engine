package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestBulkPhaseTiming is a temporary profiling harness (not part of the suite).
func TestBulkPhaseTiming(t *testing.T) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping: %v", err)
	}
	defer driver.Close()

	companyID, journalID, periodID, debitAccID, creditAccID := setupBenchmarkCompany(t, driver)
	amount, _ := financialtypes.NewMoneyFromString("100.00", "SAR")
	zero, _ := financialtypes.Zero("SAR")

	const totalEntries = 3000
	entryDate := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	mkEntries := func(prefix string) []posting.JournalEntry {
		entries := make([]posting.JournalEntry, totalEntries)
		for i := 0; i < totalEntries; i++ {
			entry, err := posting.NewJournalEntry(journalID, entryDate, "prof-maker",
				[]posting.JournalEntryLine{
					{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccID, Debit: amount, Credit: zero},
					{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccID, Debit: zero, Credit: amount},
				})
			if err != nil {
				t.Fatal(err)
			}
			entry.CompanyID = companyID
			entry.PeriodID = periodID
			entry.Reference = fmt.Sprintf("%s-%06d", prefix, i)
			if err := driver.SaveDraft(ctx, entry); err != nil {
				t.Fatalf("SaveDraft: %v", err)
			}
			entry.Status = posting.EntryPosted
			entries[i] = entry
		}
		return entries
	}

	entries := mkEntries("PROF1")
	start := time.Now()
	if err := driver.PostBulk(ctx, entries); err != nil {
		t.Fatalf("PostBulk failed: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("PostBulk (pipelined batch): %d entries in %v (%.2f entries/sec)", totalEntries, elapsed, float64(totalEntries)/elapsed.Seconds())

	// Measure the pure database-side cost: flip 3000 drafts to posted in ONE
	// statement (row triggers still fire per row, statement trigger once).
	entries2 := mkEntries("PROF2")
	ids := make([]uuid.UUID, totalEntries)
	for i := range entries2 {
		ids[i] = entries2[i].ID
	}
	start = time.Now()
	err = driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE account_moves
			SET status='posted', entry_number = sub.seq + $3,
			    entry = jsonb_set(entry, '{Status}', '"posted"', true)
			FROM (SELECT id, row_number() OVER () AS seq FROM unnest($2::uuid[]) WITH ORDINALITY AS u(id, ord)) sub
			WHERE account_moves.company_id=$1 AND account_moves.id=sub.id AND account_moves.status='draft'`,
			companyID, ids, totalEntries)
		if err != nil {
			return err
		}
		t.Logf("single-statement flip rows=%d", tag.RowsAffected())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	elapsed = time.Since(start)
	t.Logf("single-statement draft->posted flip: %v", elapsed)
}
