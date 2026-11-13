package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/storage/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// newBulkTestDraft persists a balanced two-line draft with an explicit
// reference so the stored draft JSON and the caller's candidate stay
// byte-for-byte comparable by posting.DraftContentMatches.
func newBulkTestDraft(
	t *testing.T,
	ctx context.Context,
	driver *postgres.Driver,
	companyID, journalID, periodID, debitAccountID, creditAccountID uuid.UUID,
	amount financialtypes.Money,
	date time.Time,
	reference string,
) posting.JournalEntry {
	t.Helper()
	zero, err := financialtypes.Zero(amount.Currency())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := posting.NewJournalEntry(journalID, date, "bulk-maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountID, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.PeriodID = periodID
	entry.Reference = reference
	if err := driver.SaveDraft(ctx, entry); err != nil {
		t.Fatalf("SaveDraft(%s): %v", reference, err)
	}
	return entry
}

func journalSequence(t *testing.T, ctx context.Context, driver *postgres.Driver, companyID, journalID uuid.UUID) int64 {
	t.Helper()
	var sequence int64
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
			companyID, journalID).Scan(&sequence)
	}); err != nil {
		t.Fatal(err)
	}
	return sequence
}

func assertJournalSequence(t *testing.T, ctx context.Context, driver *postgres.Driver, companyID, journalID uuid.UUID, want int64) {
	t.Helper()
	if got := journalSequence(t, ctx, driver, companyID, journalID); got != want {
		t.Fatalf("journal current_sequence = %d, want %d", got, want)
	}
}

func assertMovesStillDraft(t *testing.T, ctx context.Context, driver *postgres.Driver, companyID uuid.UUID, entries []posting.JournalEntry) {
	t.Helper()
	for _, entry := range entries {
		stored, err := driver.GetEntryByIDForCompany(ctx, companyID, entry.ID)
		if err != nil {
			t.Fatalf("GetEntryByIDForCompany(%s): %v", entry.ID, err)
		}
		if stored.Status != posting.EntryDraft {
			t.Fatalf("move %s status = %v, want draft after rolled-back bulk posting", entry.ID, stored.Status)
		}
		var dbStatus string
		var entryNumber int64
		if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT status, entry_number FROM account_moves WHERE company_id=$1 AND id=$2`,
				companyID, entry.ID).Scan(&dbStatus, &entryNumber)
		}); err != nil {
			t.Fatal(err)
		}
		if dbStatus != "draft" || entryNumber != 0 {
			t.Fatalf("move %s row status=%s entry_number=%d, want draft/0", entry.ID, dbStatus, entryNumber)
		}
	}
}

// TestConcurrentSequenceAllocation posts to one journal through two different
// write paths at the same time: set-based Driver.PostBulk batches (which
// reserve a contiguous range with UPDATE ... RETURNING) and single-entry
// Driver.PostAndSequence calls (which bump the journal row with SELECT ... FOR
// UPDATE). Because every transaction allocates its numbers inside the same
// transaction that persists the posted moves, the committed numbers must be
// exactly 1..N: no duplicates and no gaps.
func TestConcurrentSequenceAllocation(t *testing.T) {
	ctx, driver, _, _, companyID, journalID, debitAccountID, creditAccountID := setupPostgresPostingEnv(t)
	defer driver.Close()

	amount, err := financialtypes.NewMoneyFromString("10.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	entryDate := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	period, err := driver.GetPeriodByDateForCompany(ctx, companyID, entryDate)
	if err != nil {
		t.Fatal(err)
	}

	const (
		bulkWorkers   = 6
		bulkBatchSize = 20
		singleWorkers = 6
		singleCount   = 20
	)
	const totalEntries = bulkWorkers*bulkBatchSize + singleWorkers*singleCount

	// Prepare every draft before the timed concurrent section so the only
	// concurrent work is the posting/sequence allocation itself.
	bulkBatches := make([][]posting.JournalEntry, bulkWorkers)
	for w := 0; w < bulkWorkers; w++ {
		batch := make([]posting.JournalEntry, bulkBatchSize)
		for i := range batch {
			batch[i] = newBulkTestDraft(t, ctx, driver, companyID, journalID, period.ID,
				debitAccountID, creditAccountID, amount, entryDate,
				fmt.Sprintf("CONC-BULK-%d-%02d", w, i))
			batch[i].Status = posting.EntryPosted
		}
		bulkBatches[w] = batch
	}
	singles := make([]posting.JournalEntry, singleWorkers*singleCount)
	for i := range singles {
		singles[i] = newBulkTestDraft(t, ctx, driver, companyID, journalID, period.ID,
			debitAccountID, creditAccountID, amount, entryDate,
			fmt.Sprintf("CONC-SINGLE-%03d", i))
		singles[i].Status = posting.EntryPosted
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		allocated []int64
	)
	errCh := make(chan error, bulkWorkers+singleWorkers)
	collect := func(numbers ...int64) {
		mu.Lock()
		allocated = append(allocated, numbers...)
		mu.Unlock()
	}

	for w := 0; w < bulkWorkers; w++ {
		batch := bulkBatches[w]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := driver.PostBulk(ctx, batch); err != nil {
				errCh <- fmt.Errorf("bulk worker: %w", err)
				return
			}
			numbers := make([]int64, len(batch))
			for i := range batch {
				numbers[i] = batch[i].SequenceNumber
			}
			collect(numbers...)
		}()
	}
	for w := 0; w < singleWorkers; w++ {
		worker := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < singleCount; i++ {
				posted, err := driver.PostAndSequence(ctx, singles[worker*singleCount+i])
				if err != nil {
					errCh <- fmt.Errorf("single worker %d entry %d: %w", worker, i, err)
					return
				}
				collect(posted.SequenceNumber)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent posting failed: %v", err)
	}

	// Committed rows must cover 1..N exactly.
	var count, distinctCount, minNumber, maxNumber int64
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*), COUNT(DISTINCT entry_number),
			COALESCE(MIN(entry_number), 0), COALESCE(MAX(entry_number), 0)
			FROM account_moves
			WHERE company_id=$1 AND journal_id=$2 AND status='posted'`,
			companyID, journalID).Scan(&count, &distinctCount, &minNumber, &maxNumber)
	}); err != nil {
		t.Fatal(err)
	}
	if count != totalEntries || distinctCount != totalEntries {
		t.Fatalf("posted rows=%d distinct entry_number=%d, want %d", count, distinctCount, totalEntries)
	}
	if minNumber != 1 || maxNumber != totalEntries {
		t.Fatalf("entry_number range=[%d,%d], want [1,%d] (gap or duplicate detected)", minNumber, maxNumber, totalEntries)
	}
	assertJournalSequence(t, ctx, driver, companyID, journalID, totalEntries)

	// The numbers returned to callers must be the same complete range.
	sort.Slice(allocated, func(i, j int) bool { return allocated[i] < allocated[j] })
	if len(allocated) != totalEntries {
		t.Fatalf("callers observed %d sequence numbers, want %d", len(allocated), totalEntries)
	}
	for i, number := range allocated {
		if number != int64(i+1) {
			t.Fatalf("allocated[%d]=%d, want %d (duplicate or gap in returned sequences)", i, number, i+1)
		}
	}
}

// TestBulkAtomicity proves Driver.PostBulk is all-or-nothing: a batch that
// fails after its journal range has been reserved must roll back the sequence
// reservation together with the moves, so a later successful batch still
// receives the very first numbers of the journal.
func TestBulkAtomicity(t *testing.T) {
	ctx, driver, _, _, companyID, journalID, debitAccountID, creditAccountID := setupPostgresPostingEnv(t)
	defer driver.Close()

	amount, err := financialtypes.NewMoneyFromString("100.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	entryDate := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	period, err := driver.GetPeriodByDateForCompany(ctx, companyID, entryDate)
	if err != nil {
		t.Fatal(err)
	}
	makeBatch := func(prefix string, n int) []posting.JournalEntry {
		batch := make([]posting.JournalEntry, n)
		for i := range batch {
			batch[i] = newBulkTestDraft(t, ctx, driver, companyID, journalID, period.ID,
				debitAccountID, creditAccountID, amount, entryDate,
				fmt.Sprintf("%s-%02d", prefix, i))
			batch[i].Status = posting.EntryPosted
		}
		return batch
	}

	// 1) Rejected before the transaction opens (duplicate move ID): no
	// sequence may be consumed.
	duplicate := makeBatch("ATOM-DUP", 2)
	duplicate[1] = duplicate[0]
	if err := driver.PostBulk(ctx, duplicate); err == nil {
		t.Fatal("expected duplicate move ID batch to be rejected")
	}
	assertJournalSequence(t, ctx, driver, companyID, journalID, 0)
	assertMovesStillDraft(t, ctx, driver, companyID, []posting.JournalEntry{duplicate[0]})

	// 2) Database trigger failure mid-batch: one tampered functional leg makes
	// the batch unbalanced. The range was already reserved and the set-based
	// flip already started, yet the whole transaction must roll back.
	triggerBatch := makeBatch("ATOM-TRIGGER", 4)
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE account_move_lines
			SET functional_credit_minor = 20000
			WHERE company_id=$1 AND move_id=$2 AND line_id=$3`,
			companyID, triggerBatch[3].ID, triggerBatch[3].Lines[1].ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("expected one tampered line, updated %d", tag.RowsAffected())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = driver.PostBulk(ctx, triggerBatch)
	if err == nil {
		t.Fatal("expected bulk posting to fail on the unbalanced move")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("expected check_violation from balance trigger, got: %v", err)
	}
	assertJournalSequence(t, ctx, driver, companyID, journalID, 0)
	assertMovesStillDraft(t, ctx, driver, companyID, triggerBatch)
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var balances int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM account_balances WHERE company_id=$1`, companyID).Scan(&balances); err != nil {
			return err
		}
		if balances != 0 {
			t.Fatalf("failed bulk posting materialized %d balance rows", balances)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 3) Verification failure after sequence reservation: one candidate has no
	// stored draft (deleted or never persisted, e.g. a lost race). The
	// reserved range must not leak.
	missing := makeBatch("ATOM-MISSING", 2)
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	phantom, err := posting.NewJournalEntry(journalID, entryDate, "bulk-maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountID, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	phantom.CompanyID = companyID
	phantom.PeriodID = period.ID
	phantom.Reference = "ATOM-MISSING-PHANTOM"
	phantom.Status = posting.EntryPosted
	missing = append(missing, phantom)
	err = driver.PostBulk(ctx, missing)
	if err == nil {
		t.Fatal("expected bulk posting to fail when a candidate is no longer a draft")
	}
	if !errors.Is(err, posting.ErrEntryNotDraft) {
		t.Fatalf("expected ErrEntryNotDraft, got: %v", err)
	}
	assertJournalSequence(t, ctx, driver, companyID, journalID, 0)
	assertMovesStillDraft(t, ctx, driver, companyID, missing[:2])

	// 4) The journal must hand out 1..k to the first successful batch: the two
	// failed attempts left no gap and no consumed numbers behind.
	success := makeBatch("ATOM-SUCCESS", 3)
	if err := driver.PostBulk(ctx, success); err != nil {
		t.Fatalf("successful bulk posting after failures: %v", err)
	}
	assertJournalSequence(t, ctx, driver, companyID, journalID, 3)
	for i := range success {
		want := int64(i + 1)
		if success[i].SequenceNumber != want {
			t.Fatalf("success[%d].SequenceNumber = %d, want %d", i, success[i].SequenceNumber, want)
		}
		stored, err := driver.GetEntryByIDForCompany(ctx, companyID, success[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != posting.EntryPosted || stored.SequenceNumber != want {
			t.Fatalf("success[%d] stored status=%v sequence=%d, want posted/%d",
				i, stored.Status, stored.SequenceNumber, want)
		}
	}
}
