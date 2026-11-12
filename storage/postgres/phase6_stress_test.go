package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	accountingengine "accounting_engine"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// TestPhase6ConcurrentStress500Entries50Goroutines is the Phase 6 concurrency
// gate: five hundred entries are posted by fifty concurrent goroutines against
// a single shared journal (so every posting transaction contends for the same
// journal row lock). The test proves:
//   - all goroutines complete without errors or deadlocks inside a bounded
//     deadline;
//   - every entry receives a distinct, gapless sequence number;
//   - the materialized cumulative balances equal the deterministically derived
//     expected debit/credit totals exactly;
//   - the trial balance and continuous reconciliation show zero drift.
func TestPhase6ConcurrentStress500Entries50Goroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrency stress test is skipped in short mode")
	}
	const (
		numGoroutines = 50
		entriesPerGo  = 10
		totalEntries  = numGoroutines * entriesPerGo
		postDeadline  = 90 * time.Second
		currencyCode  = "USD"
		referencePfx  = "PHASE6-STRESS"
	)
	ctx, env := setupIsolatedPostingEnvWithPool(t, numGoroutines+5)

	entryDate := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	zero, err := financialtypes.Zero(currencyCode)
	if err != nil {
		t.Fatal(err)
	}

	// Deterministic amount per global entry index: 1.00 .. 100.99.
	amountFor := func(idx int) (financialtypes.Money, decimal.Decimal, error) {
		dollars := 1 + idx%100
		cents := idx % 100
		amount, err := financialtypes.NewMoneyFromString(
			fmt.Sprintf("%d.%02d", dollars, cents), currencyCode)
		return amount, decimal.NewFromInt(int64(dollars*100 + cents)), err
	}

	drafts := make([]posting.JournalEntry, totalEntries)
	var expectedDebitMinor decimal.Decimal
	for idx := range totalEntries {
		amount, minor, err := amountFor(idx)
		if err != nil {
			t.Fatal(err)
		}
		expectedDebitMinor = expectedDebitMinor.Add(minor)
		entry, err := posting.NewJournalEntry(env.JournalID, entryDate, "phase6-maker", []posting.JournalEntryLine{
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID, Debit: amount, Credit: zero},
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID, Debit: zero, Credit: amount},
		})
		if err != nil {
			t.Fatal(err)
		}
		entry.CompanyID = env.CompanyID
		entry.PeriodID = env.PeriodID
		entry.Reference = fmt.Sprintf("%s-%04d", referencePfx, idx)
		drafts[idx] = entry
	}

	// Stage all drafts first so the concurrent phase exercises posting only.
	if err := env.Driver.PostBulk(ctx, drafts); err != nil {
		t.Fatalf("stage drafts: %v", err)
	}

	postCtx, cancel := context.WithTimeout(ctx, postDeadline)
	defer cancel()

	errCh := make(chan error, totalEntries)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	begin := time.Now()
	for g := 0; g < numGoroutines; g++ {
		go func(worker int) {
			defer wg.Done()
			<-start
			for i := 0; i < entriesPerGo; i++ {
				idx := worker*entriesPerGo + i
				posted := drafts[idx]
				posted.Status = posting.EntryPosted
				posted.PostedBy = "phase6-checker"
				if err := env.Driver.PostBulk(postCtx, []posting.JournalEntry{posted}); err != nil {
					errCh <- fmt.Errorf("goroutine %d entry %d: %w", worker, idx, err)
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	elapsed := time.Since(begin)
	close(errCh)

	workerErrors := 0
	for err := range errCh {
		workerErrors++
		if workerErrors == 1 {
			t.Errorf("concurrent posting error: %v", err)
		}
	}
	if workerErrors > 0 {
		t.Fatalf("concurrent posting finished with %d error(s) in %v", workerErrors, elapsed)
	}
	if postCtx.Err() != nil {
		t.Fatalf("concurrent posting exceeded the %v deadline (possible deadlock): %v", postDeadline, postCtx.Err())
	}
	t.Logf("posted %d entries from %d goroutines in %v (%.0f entries/sec)",
		totalEntries, numGoroutines, elapsed, float64(totalEntries)/elapsed.Seconds())

	// Every entry posted exactly once with a gapless sequence range.
	var postedMoves, moveLines int
	var distinctNumbers, minNumber, maxNumber, sequence int64
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM account_moves WHERE company_id=$1 AND status='posted'`,
			env.CompanyID).Scan(&postedMoves); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM account_move_lines WHERE company_id=$1`,
			env.CompanyID).Scan(&moveLines); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
			env.CompanyID, env.JournalID).Scan(&sequence); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT COUNT(DISTINCT entry_number), MIN(entry_number), MAX(entry_number)
			 FROM account_moves WHERE company_id=$1 AND journal_id=$2 AND status='posted'`,
			env.CompanyID, env.JournalID).Scan(&distinctNumbers, &minNumber, &maxNumber)
	}); err != nil {
		t.Fatal(err)
	}
	if postedMoves != totalEntries {
		t.Fatalf("posted moves = %d, want %d", postedMoves, totalEntries)
	}
	if moveLines != totalEntries*2 {
		t.Fatalf("move lines = %d, want %d", moveLines, totalEntries*2)
	}
	if sequence != totalEntries || distinctNumbers != totalEntries || minNumber != 1 || maxNumber != totalEntries {
		t.Fatalf("journal sequence integrity: sequence=%d distinct=%d min=%d max=%d, want %d distinct 1..%d",
			sequence, distinctNumbers, minNumber, maxNumber, totalEntries, totalEntries)
	}

	// Cumulative materialized balances must equal the expected totals exactly.
	hundred := decimal.NewFromInt(100)
	wantDebit := expectedDebitMinor.Div(hundred)
	var cashDebit, revenueCredit decimal.Decimal
	var balanceRows int
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Query(ctx,
			`SELECT account_id, period_debit, period_credit FROM account_balances
			 WHERE company_id=$1 AND currency_id=$2`, env.CompanyID, currencyCode)
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			var accountID uuid.UUID
			var debit, credit decimal.Decimal
			if err := result.Scan(&accountID, &debit, &credit); err != nil {
				return err
			}
			balanceRows++
			switch accountID {
			case env.DebitAccID:
				cashDebit = debit
				if !credit.IsZero() {
					return fmt.Errorf("cash account has unexpected credit %s", credit)
				}
			case env.CreditAccID:
				revenueCredit = credit
				if !debit.IsZero() {
					return fmt.Errorf("revenue account has unexpected debit %s", debit)
				}
			}
		}
		return result.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if balanceRows != 2 {
		t.Fatalf("materialized balance rows = %d, want 2", balanceRows)
	}
	if !cashDebit.Equal(wantDebit) {
		t.Fatalf("cash cumulative debit = %s, want %s", cashDebit, wantDebit)
	}
	if !revenueCredit.Equal(wantDebit) {
		t.Fatalf("revenue cumulative credit = %s, want %s", revenueCredit, wantDebit)
	}
	if !cashDebit.Equal(revenueCredit) {
		t.Fatalf("cumulative balances: debits %s != credits %s", cashDebit, revenueCredit)
	}

	engine, err := accountingengine.New(env.Driver, accountingengine.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	// The trial balance generated through the public engine facade must balance
	// and agree with the cumulative materialized totals.
	yearStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := yearStart.AddDate(1, 0, 0).Add(-time.Nanosecond)
	trialBalance, err := engine.Reporting.TrialBalance.GenerateForCompanyContext(
		ctx, env.CompanyID, yearStart, yearEnd, currencyCode)
	if err != nil {
		t.Fatalf("generate trial balance after stress: %v", err)
	}
	if !trialBalance.Balanced || !trialBalance.DebitTotal.Equal(trialBalance.CreditTotal) {
		t.Fatalf("trial balance after stress: balanced=%v debits=%s credits=%s",
			trialBalance.Balanced, trialBalance.DebitTotal, trialBalance.CreditTotal)
	}
	if !trialBalance.DebitTotal.Equal(wantDebit) {
		t.Fatalf("trial balance debit total = %s, want %s", trialBalance.DebitTotal, wantDebit)
	}

	// Continuous reconciliation must confirm zero drift after the storm.
	reconciliation, err := engine.ReconcileBalances(ctx, env.CompanyID, env.PeriodID, currencyCode)
	if err != nil {
		t.Fatalf("reconcile balances after stress: %v", err)
	}
	if reconciliation == nil || !reconciliation.ZeroDrift || reconciliation.DriftedAccounts() != 0 {
		t.Fatalf("reconciliation after stress = %+v, want zero drift", reconciliation)
	}
}
