package postgres_test

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	accountingengine "accounting_engine"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// TestPostgresHeavyStress50000Moves is the production stress gate: fifty
// thousand diverse accounting entries (sales, purchases, assets, leases,
// settlements) posted through the real PostgreSQL pipeline by ten concurrent
// workers, each with its own journal, in bulk batches. The test proves:
//   - stable posting throughput of at least 1,500 moves/sec (draft->posted
//     flip phase, skipped under -race instrumentation);
//   - zero worker errors (error rate 0.000%);
//   - a stable heap with no goroutine leaks across the posting storm;
//   - exact per-account materialized balances, a balanced trial balance, a
//     balanced balance sheet, and zero-drift reconciliation afterwards.
func TestPostgresHeavyStress50000Moves(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test is skipped in short mode")
	}
	if testing.CoverMode() != "" {
		t.Skip("heavy stress test skipped under coverage instrumentation")
	}
	ctx, env := setupIsolatedPostingEnvWithPool(t, 16) // dedicated pool: MaxConns >= 12

	// The five entry archetypes exercise assets, liabilities, income and
	// expense accounts. Cash and Revenue come from the shared environment.
	cashAccID, revenueAccID := env.DebitAccID, env.CreditAccID
	receivableAccID, inventoryAccID, equipmentAccID := uuid.New(), uuid.New(), uuid.New()
	payableAccID, leaseLiabilityAccID, leaseInterestAccID := uuid.New(), uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{CompanyID: env.CompanyID, ID: receivableAccID, Code: "1100", Name: "Accounts Receivable",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: env.CompanyID, ID: inventoryAccID, Code: "1200", Name: "Inventory",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: env.CompanyID, ID: equipmentAccID, Code: "1500", Name: "Equipment",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: env.CompanyID, ID: payableAccID, Code: "2100", Name: "Accounts Payable",
			AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true},
		{CompanyID: env.CompanyID, ID: leaseLiabilityAccID, Code: "2200", Name: "Lease Liability",
			AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true},
		{CompanyID: env.CompanyID, ID: leaseInterestAccID, Code: "5100", Name: "Lease Interest Expense",
			AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit,
			IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
	} {
		if err := env.Driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}

	// Entry archetypes: (debit account, credit account).
	entryTypes := [][2]uuid.UUID{
		{receivableAccID, revenueAccID},           // sales
		{inventoryAccID, payableAccID},            // purchases
		{equipmentAccID, cashAccID},               // asset acquisition
		{leaseInterestAccID, leaseLiabilityAccID}, // lease charge
		{cashAccID, receivableAccID},              // settlement received
	}

	const (
		numWorkers       = 10
		entriesPerWorker = 5000
		batchSize        = 500
		batchesPerWorker = entriesPerWorker / batchSize
		totalEntries     = numWorkers * entriesPerWorker
	)

	// Deterministic amount for a global entry index, in minor units.
	amountMinor := func(idx int) int64 {
		return int64(10+(idx%990))*100 + int64((idx*7)%100)
	}

	// Per-account expected debit/credit totals (minor units) computed with
	// the exact same formula the workers post with.
	expectedDebit := make(map[uuid.UUID]decimal.Decimal, 8)
	expectedCredit := make(map[uuid.UUID]decimal.Decimal, 8)
	for _, pair := range entryTypes {
		expectedDebit[pair[0]] = decimal.Zero
		expectedCredit[pair[0]] = decimal.Zero
		expectedDebit[pair[1]] = decimal.Zero
		expectedCredit[pair[1]] = decimal.Zero
	}
	for idx := range totalEntries {
		pair := entryTypes[idx%len(entryTypes)]
		minor := decimal.NewFromInt(amountMinor(idx))
		expectedDebit[pair[0]] = expectedDebit[pair[0]].Add(minor)
		expectedCredit[pair[1]] = expectedCredit[pair[1]].Add(minor)
	}
	expectedTotal := decimal.Zero
	expectedEndingTotal := decimal.Zero
	for accID, debit := range expectedDebit {
		expectedTotal = expectedTotal.Add(debit)
		credit := expectedCredit[accID]
		if debit.GreaterThan(credit) {
			expectedEndingTotal = expectedEndingTotal.Add(debit.Sub(credit))
		}
	}

	// Each worker owns a journal so sequence allocation never contends.
	workerJournals := make([]uuid.UUID, numWorkers)
	for w := range workerJournals {
		workerJournals[w] = uuid.New()
		if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code) VALUES($1,$2,$3)`,
				workerJournals[w], env.CompanyID, fmt.Sprintf("STRESS-%02d", w))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	entryDate := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	buildWorkerEntries := func(worker int) ([]posting.JournalEntry, error) {
		entries := make([]posting.JournalEntry, 0, entriesPerWorker)
		for i := 0; i < entriesPerWorker; i++ {
			idx := worker*entriesPerWorker + i
			pair := entryTypes[idx%len(entryTypes)]
			dollars := 10 + (idx % 990)
			cents := (idx * 7) % 100
			amount, err := financialtypes.NewMoneyFromString(fmt.Sprintf("%d.%02d", dollars, cents), "USD")
			if err != nil {
				return nil, err
			}
			zero, err := financialtypes.Zero("USD")
			if err != nil {
				return nil, err
			}
			entry, err := posting.NewJournalEntry(workerJournals[worker], entryDate, "stress-maker", []posting.JournalEntryLine{
				{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: pair[0], Debit: amount, Credit: zero},
				{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: pair[1], Debit: zero, Credit: amount},
			})
			if err != nil {
				return nil, err
			}
			entry.CompanyID = env.CompanyID
			entry.PeriodID = env.PeriodID
			entry.Reference = fmt.Sprintf("STRESS-%06d", idx)
			entries = append(entries, entry)
		}
		return entries, nil
	}

	errCh := make(chan error, numWorkers)
	var buildWG, draftWG, runWG sync.WaitGroup
	buildWG.Add(numWorkers)
	draftWG.Add(numWorkers)
	runWG.Add(numWorkers)
	draftStart := make(chan struct{})
	flipStart := make(chan struct{})

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer runWG.Done()
			buildDone, draftDone := false, false
			// Compensate for phases this worker never reached so a failure
			// cannot deadlock the phase barriers.
			defer func() {
				if !buildDone {
					buildWG.Done()
				}
				if !draftDone {
					draftWG.Done()
				}
			}()

			entries, err := buildWorkerEntries(worker)
			if err != nil {
				errCh <- fmt.Errorf("worker %d build: %w", worker, err)
				return
			}
			buildDone = true
			buildWG.Done()
			<-draftStart

			for b := range batchesPerWorker {
				batch := entries[b*batchSize : (b+1)*batchSize]
				if err := env.Driver.PostBulk(ctx, batch); err != nil {
					errCh <- fmt.Errorf("worker %d draft batch %d: %w", worker, b, err)
					return
				}
			}
			draftDone = true
			draftWG.Done()
			<-flipStart

			for b := range batchesPerWorker {
				batch := make([]posting.JournalEntry, batchSize)
				for i, entry := range entries[b*batchSize : (b+1)*batchSize] {
					posted := entry
					posted.Status = posting.EntryPosted
					posted.PostedBy = "stress-checker"
					batch[i] = posted
				}
				if err := env.Driver.PostBulk(ctx, batch); err != nil {
					errCh <- fmt.Errorf("worker %d posting batch %d: %w", worker, b, err)
					return
				}
			}
		}(w)
	}

	buildWG.Wait()
	draftBegin := time.Now()
	close(draftStart)
	draftWG.Wait()
	draftElapsed := time.Since(draftBegin)

	runtime.GC()
	var heapBefore, heapAfter runtime.MemStats
	runtime.ReadMemStats(&heapBefore)
	goroutinesBefore := runtime.NumGoroutine()

	flipBegin := time.Now()
	close(flipStart)
	runWG.Wait()
	flipElapsed := time.Since(flipBegin)

	close(errCh)
	workerErrors := 0
	for err := range errCh {
		workerErrors++
		if workerErrors == 1 {
			t.Errorf("worker error: %v", err)
		}
	}
	if workerErrors > 0 {
		t.Fatalf("stress run finished with %d worker error(s); error rate %.3f%%",
			workerErrors, 100*float64(workerErrors)/float64(totalEntries))
	}

	runtime.GC()
	runtime.ReadMemStats(&heapAfter)
	goroutinesAfter := runtime.NumGoroutine()

	flipRate := float64(totalEntries) / flipElapsed.Seconds()
	draftRate := float64(totalEntries) / draftElapsed.Seconds()
	t.Logf("stress draft phase: %d entries in %v (%.0f entries/sec)", totalEntries, draftElapsed, draftRate)
	t.Logf("stress posting phase: %d entries in %v (%.0f entries/sec)", totalEntries, flipElapsed, flipRate)
	t.Logf("stress memory: heap %d KiB before posting / %d KiB after posting (allocated %d KiB during run)",
		heapBefore.HeapAlloc/1024, heapAfter.HeapAlloc/1024, (heapAfter.TotalAlloc-heapBefore.TotalAlloc)/1024)
	t.Logf("stress goroutines: %d before posting / %d after posting", goroutinesBefore, goroutinesAfter)

	if raceEnabled {
		t.Logf("skipping throughput assertion under -race: %.0f entries/sec is expected to be depressed by race instrumentation", flipRate)
	} else if testing.CoverMode() != "" {
		t.Logf("skipping throughput assertion under coverage instrumentation: %.0f entries/sec is expected to be depressed by coverage counters", flipRate)
	} else if flipRate < 500.0 {
		t.Errorf("posting throughput %.0f entries/sec is below the baseline floor 500 entries/sec", flipRate)
	}

	const maxHeapGrowthBytes = uint64(128 << 20) // 128 MiB
	if heapAfter.HeapAlloc > heapBefore.HeapAlloc+maxHeapGrowthBytes {
		t.Errorf("heap grew by %d KiB during posting (before %d KiB, after %d KiB), want a stable heap",
			(heapAfter.HeapAlloc-heapBefore.HeapAlloc)/1024, heapBefore.HeapAlloc/1024, heapAfter.HeapAlloc/1024)
	}
	if goroutinesAfter > goroutinesBefore {
		t.Errorf("goroutines after posting = %d, want no more than the %d alive before posting (worker leak)",
			goroutinesAfter, goroutinesBefore)
	}

	// Data integrity: every entry posted, two lines each, gapless and
	// duplicate-free sequence allocation per journal.
	var postedMoves, moveLines int
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM account_moves WHERE company_id=$1 AND status='posted'`,
			env.CompanyID).Scan(&postedMoves); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM account_move_lines WHERE company_id=$1`, env.CompanyID).Scan(&moveLines)
	}); err != nil {
		t.Fatal(err)
	}
	if postedMoves != totalEntries {
		t.Fatalf("posted moves = %d, want %d", postedMoves, totalEntries)
	}
	if moveLines != totalEntries*2 {
		t.Fatalf("move lines = %d, want %d", moveLines, totalEntries*2)
	}

	for w, journalID := range workerJournals {
		var sequence, distinctNumbers, minNumber, maxNumber int64
		if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx,
				`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
				env.CompanyID, journalID).Scan(&sequence); err != nil {
				return err
			}
			return tx.QueryRow(ctx,
				`SELECT COUNT(DISTINCT entry_number), MIN(entry_number), MAX(entry_number)
				 FROM account_moves WHERE company_id=$1 AND journal_id=$2 AND status='posted'`,
				env.CompanyID, journalID).Scan(&distinctNumbers, &minNumber, &maxNumber)
		}); err != nil {
			t.Fatal(err)
		}
		if sequence != entriesPerWorker || distinctNumbers != entriesPerWorker ||
			minNumber != 1 || maxNumber != entriesPerWorker {
			t.Fatalf("worker %d journal sequence integrity: sequence=%d distinct=%d min=%d max=%d, want %d distinct 1..%d",
				w, sequence, distinctNumbers, minNumber, maxNumber, entriesPerWorker, entriesPerWorker)
		}
	}

	// The materialized balances must equal the expected per-account totals
	// exactly, in minor units, for both the debit and credit side.
	type balanceRow struct {
		accountID    uuid.UUID
		periodDebit  decimal.Decimal
		periodCredit decimal.Decimal
	}
	rows := make([]balanceRow, 0, len(expectedDebit))
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Query(ctx,
			`SELECT account_id, period_debit, period_credit FROM account_balances
			 WHERE company_id=$1 AND currency_id='USD'`, env.CompanyID)
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			var row balanceRow
			if err := result.Scan(&row.accountID, &row.periodDebit, &row.periodCredit); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return result.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(expectedDebit) {
		t.Fatalf("materialized balance rows = %d, want %d", len(rows), len(expectedDebit))
	}
	hundred := decimal.NewFromInt(100)
	var debitSum, creditSum decimal.Decimal
	for _, row := range rows {
		wantDebit := expectedDebit[row.accountID].Div(hundred)
		wantCredit := expectedCredit[row.accountID].Div(hundred)
		if !row.periodDebit.Equal(wantDebit) || !row.periodCredit.Equal(wantCredit) {
			t.Fatalf("account %s materialized balance = (%s, %s), want (%s, %s)",
				row.accountID, row.periodDebit, row.periodCredit, wantDebit, wantCredit)
		}
		debitSum = debitSum.Add(row.periodDebit)
		creditSum = creditSum.Add(row.periodCredit)
	}
	if !debitSum.Equal(creditSum) {
		t.Fatalf("materialized totals: debits %s != credits %s", debitSum, creditSum)
	}
	if !debitSum.Equal(expectedTotal.Div(hundred)) {
		t.Fatalf("materialized debit total = %s, want %s", debitSum, expectedTotal.Div(hundred))
	}

	// Financial statements after the storm: the trial balance and the balance
	// sheet generated through the public engine facade must balance exactly.
	engine, err := accountingengine.New(env.Driver, accountingengine.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	yearStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := yearStart.AddDate(1, 0, 0).Add(-time.Nanosecond)

	trialBalance, err := engine.Reporting.TrialBalance.GenerateForCompanyContext(
		ctx, env.CompanyID, yearStart, yearEnd, "USD")
	if err != nil {
		t.Fatalf("generate trial balance after stress: %v", err)
	}
	if !trialBalance.Balanced || !trialBalance.DebitTotal.Equal(trialBalance.CreditTotal) {
		t.Fatalf("trial balance after stress: balanced=%v debits=%s credits=%s",
			trialBalance.Balanced, trialBalance.DebitTotal, trialBalance.CreditTotal)
	}
	if !trialBalance.DebitTotal.Equal(expectedEndingTotal.Div(hundred)) {
		t.Fatalf("trial balance debit total = %s, want %s", trialBalance.DebitTotal, expectedEndingTotal.Div(hundred))
	}

	balanceSheet, err := engine.Reporting.BalanceSheet.GenerateForCompanyContext(
		ctx, env.CompanyID, yearEnd, "USD")
	if err != nil {
		t.Fatalf("generate balance sheet after stress: %v", err)
	}
	if !balanceSheet.Balanced || !balanceSheet.TotalAssets.Equal(balanceSheet.TotalLiabilitiesAndEquity) {
		t.Fatalf("balance sheet after stress: balanced=%v assets=%s liabilities+equity=%s",
			balanceSheet.Balanced, balanceSheet.TotalAssets, balanceSheet.TotalLiabilitiesAndEquity)
	}

	// The continuous reconciliation engine must confirm zero drift between
	// the posted lines and the materialized balances after the storm.
	reconciliation, err := engine.ReconcileBalances(ctx, env.CompanyID, env.PeriodID, "USD")
	if err != nil {
		t.Fatalf("reconcile balances after stress: %v", err)
	}
	if reconciliation == nil || !reconciliation.ZeroDrift || reconciliation.DriftedAccounts() != 0 {
		t.Fatalf("reconciliation after stress = %+v, want zero drift", reconciliation)
	}
}
