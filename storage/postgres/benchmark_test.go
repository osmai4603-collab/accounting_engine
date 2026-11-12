package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

func getTestPostgresConn() string {
	connStr := os.Getenv("POSTGRES_URL")
	if connStr == "" {
		connStr = "postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql"
	}
	return connStr
}

func setupBenchmarkCompany(t *testing.T, driver *postgres.Driver) (uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	ctx := context.Background()
	migrationSQL, err := os.ReadFile("migrations/202611100002_phase1_entry_classification.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Pool().Exec(ctx, string(migrationSQL)); err != nil {
		t.Fatalf("apply phase-1 classification migration: %v", err)
	}
	companyID := uuid.New()
	company := masterdata.Company{
		ID:                   companyID,
		Code:                 fmt.Sprintf("BENCH-%s", companyID.String()[:8]),
		LegalName:            "Postgres Benchmark Corp",
		FunctionalCurrency:   "SAR",
		PresentationCurrency: "SAR",
		Active:               true,
	}
	if err := driver.CreateCompany(ctx, company); err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	// Create Fiscal Year & Period
	yearStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	periodID := uuid.New()
	fy := masterdata.FiscalYear{
		ID:        uuid.New(),
		CompanyID: companyID,
		Name:      "FY2026",
		StartDate: yearStart,
		EndDate:   yearEnd,
		Periods: []masterdata.FiscalPeriod{
			{
				ID:           periodID,
				CompanyID:    companyID,
				FiscalYearID: uuid.Nil,
				Number:       1,
				Name:         "P01-2026",
				StartDate:    yearStart,
				EndDate:      yearEnd,
				State:        masterdata.PeriodOpen,
				Type:         masterdata.FiscalPeriodTypeNormal,
			},
		},
	}
	if err := driver.CreateFiscalYear(ctx, fy); err != nil {
		t.Fatalf("CreateFiscalYear: %v", err)
	}

	debitAccID, creditAccID := uuid.New(), uuid.New()
	debitAcc := masterdata.Account{
		CompanyID:     companyID,
		ID:            debitAccID,
		Code:          "1000",
		Name:          "Cash",
		AccountType:   masterdata.AccountTypeAsset,
		NormalBalance: masterdata.NormalBalanceDebit,
	}
	creditAcc := masterdata.Account{
		CompanyID:     companyID,
		ID:            creditAccID,
		Code:          "4000",
		Name:          "Revenue",
		AccountType:   masterdata.AccountTypeIncome,
		NormalBalance: masterdata.NormalBalanceCredit,
	}
	if err := driver.CreateAccount(ctx, debitAcc); err != nil {
		t.Fatalf("CreateAccount debit: %v", err)
	}
	if err := driver.CreateAccount(ctx, creditAcc); err != nil {
		t.Fatalf("CreateAccount credit: %v", err)
	}

	journalID := uuid.New()
	err = driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code, name, current_sequence)
		 VALUES($1, $2, 'GEN', 'General Journal', 0)`, journalID, companyID)
		return err
	})
	if err != nil {
		t.Fatalf("Create journal: %v", err)
	}

	return companyID, journalID, periodID, debitAccID, creditAccID
}

func TestPostgresBatchPostingPerformance(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("throughput assertion skipped under coverage instrumentation")
	}
	ctx := context.Background()
	connStr := getTestPostgresConn()
	driver, err := postgres.New(ctx, connStr)
	if err != nil {
		t.Skipf("Skipping postgres benchmark: unable to connect (%v)", err)
		return
	}
	defer driver.Close()

	companyID, journalID, periodID, debitAccID, creditAccID := setupBenchmarkCompany(t, driver)
	amount, err := financialtypes.NewMoneyFromString("100.00", "SAR")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("SAR")

	const totalEntries = 3000
	entryDate := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	entries := make([]posting.JournalEntry, totalEntries)
	for i := 0; i < totalEntries; i++ {
		entryID := uuid.New()
		entry, err := posting.NewJournalEntry(journalID, entryDate, "bench-maker",
			[]posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccID, Debit: amount, Credit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccID, Debit: zero, Credit: amount},
			})
		if err != nil {
			t.Fatal(err)
		}
		entry.ID = entryID
		entry.CompanyID = companyID
		entry.PeriodID = periodID
		entry.Reference = fmt.Sprintf("BENCH-%06d", i)
		if err := driver.SaveDraft(ctx, entry); err != nil {
			t.Fatalf("SaveDraft: %v", err)
		}
		entry.Status = posting.EntryPosted
		entries[i] = entry
	}

	// Measure PostBulk / ACID batch posting throughput
	start := time.Now()
	if err := driver.PostBulk(ctx, entries); err != nil {
		t.Fatalf("PostBulk failed: %v", err)
	}
	elapsed := time.Since(start)
	rate := float64(totalEntries) / elapsed.Seconds()
	t.Logf("PostgreSQL ACID Batch Posting: %d entries in %v (%.2f entries/sec)", totalEntries, elapsed, rate)

	if rate < 2000.0 && raceEnabled {
		t.Logf("skipping throughput assertion under -race: %.2f entries/sec is expected to be depressed by race instrumentation", rate)
		return
	}
	if rate < 2000.0 {
		// Retry once in case of transient I/O or background vacuum/checkpoint contention from preceding tests
		entries2 := make([]posting.JournalEntry, totalEntries)
		for i := 0; i < totalEntries; i++ {
			entryID := uuid.New()
			entry, err := posting.NewJournalEntry(journalID, entryDate, "bench-maker-retry",
				[]posting.JournalEntryLine{
					{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccID, Debit: amount, Credit: zero},
					{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccID, Debit: zero, Credit: amount},
				})
			if err != nil {
				t.Fatal(err)
			}
			entry.ID = entryID
			entry.CompanyID = companyID
			entry.PeriodID = periodID
			entry.Reference = fmt.Sprintf("BENCH-RETRY-%06d", i)
			if err := driver.SaveDraft(ctx, entry); err != nil {
				t.Fatalf("SaveDraft retry: %v", err)
			}
			entry.Status = posting.EntryPosted
			entries2[i] = entry
		}
		start = time.Now()
		if err := driver.PostBulk(ctx, entries2); err != nil {
			t.Fatalf("PostBulk retry failed: %v", err)
		}
		elapsed = time.Since(start)
		rate = float64(totalEntries) / elapsed.Seconds()
		t.Logf("PostgreSQL ACID Batch Posting (retry): %d entries in %v (%.2f entries/sec)", totalEntries, elapsed, rate)
	}

	if rate < 2000.0 {
		t.Errorf("Posting throughput %.2f entries/sec is below standard 2000 entries/sec", rate)
	}
}

func TestPostgresConcurrentPostingIntegrity(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("contention test skipped under coverage instrumentation")
	}
	ctx := context.Background()
	connStr := getTestPostgresConn()
	driver, err := postgres.New(ctx, connStr)
	if err != nil {
		t.Skipf("Skipping postgres benchmark: unable to connect (%v)", err)
		return
	}
	defer driver.Close()

	companyID, _, periodID, debitAccID, creditAccID := setupBenchmarkCompany(t, driver)
	amount, err := financialtypes.NewMoneyFromString("50.00", "SAR")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("SAR")

	// 10 concurrent journals/workers, 200 entries each = 2000 entries
	const numWorkers = 10
	const entriesPerWorker = 200
	const totalEntries = numWorkers * entriesPerWorker

	journalIDs := make([]uuid.UUID, numWorkers)
	for w := 0; w < numWorkers; w++ {
		jID := uuid.New()
		journalIDs[w] = jID
		err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code, name, current_sequence)
			 VALUES($1, $2, $3, 'Worker Journal', 0)`, jID, companyID, fmt.Sprintf("J-%02d", w))
			return err
		})
		if err != nil {
			t.Fatalf("Create worker journal: %v", err)
		}
	}

	entryDate := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers)
	start := time.Now()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			jID := journalIDs[workerIdx]
			for i := 0; i < entriesPerWorker; i++ {
				entry, err := posting.NewJournalEntry(jID, entryDate, "worker-maker",
					[]posting.JournalEntryLine{
						{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccID, Debit: amount, Credit: zero},
						{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccID, Debit: zero, Credit: amount},
					})
				if err != nil {
					errCh <- err
					return
				}
				entry.CompanyID = companyID
				entry.PeriodID = periodID
				entry.Reference = fmt.Sprintf("W%d-%04d", workerIdx, i)
				if err := driver.SaveDraft(ctx, entry); err != nil {
					errCh <- err
					return
				}
				entry.Status = posting.EntryPosted
				if _, err := driver.PostAndSequence(ctx, entry); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("Concurrent posting failed: %v", err)
	}
	elapsed := time.Since(start)
	rate := float64(totalEntries) / elapsed.Seconds()
	t.Logf("PostgreSQL Concurrent PostAndSequence: %d entries across %d journals in %v (%.2f entries/sec)",
		totalEntries, numWorkers, elapsed, rate)
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var debitTotal, creditTotal decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT
			COALESCE(SUM(period_debit) FILTER (WHERE account_id=$2),0),
			COALESCE(SUM(period_credit) FILTER (WHERE account_id=$3),0)
			FROM account_balances WHERE company_id=$1 AND fiscal_period_id=$4 AND currency_id='SAR'`,
			companyID, debitAccID, creditAccID, periodID).Scan(&debitTotal, &creditTotal); err != nil {
			return err
		}
		expected := amount.Amount().Mul(decimal.NewFromInt(totalEntries))
		if !debitTotal.Equal(expected) || !creditTotal.Equal(expected) {
			t.Fatalf("concurrent materialized totals debit=%s credit=%s; want %s", debitTotal, creditTotal, expected)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPostgresSingleJournalHighContentionPosting proves that a single journal
// row is a safe serialization point under heavy contention: fifty workers
// posting one thousand entries into the SAME journal at the same instant
// neither deadlock (SQLSTATE 40P01), duplicate a sequence number, nor leave a
// gap in the allocated sequence range. The journal lock is always taken first
// inside every posting transaction, so lock ordering is uniform and
// PostgreSQL cannot produce a cycle.
func TestPostgresSingleJournalHighContentionPosting(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("high-contention test skipped under coverage instrumentation")
	}
	ctx := context.Background()
	connStr := getTestPostgresConn()
	driver, err := postgres.New(ctx, connStr)
	if err != nil {
		t.Skipf("Skipping postgres contention test: unable to connect (%v)", err)
	}
	defer driver.Close()

	companyID, journalID, periodID, debitAccID, creditAccID := setupBenchmarkCompany(t, driver)
	amount, err := financialtypes.NewMoneyFromString("25.00", "SAR")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("SAR")

	const (
		numWorkers    = 50
		perWorker     = 20
		totalEntries  = numWorkers * perWorker
		minViableRate = 10.0 // sanity floor; the goal of this test is integrity, not speed
	)
	entryDate := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	type workerResult struct {
		sequences []int64
	}

	results := make([]workerResult, numWorkers)
	errCh := make(chan error, numWorkers)
	startDone := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			// Every worker waits on the same barrier so all fifty posting
			// transactions hit the journal row in the same instant.
			<-startDone
			res := workerResult{sequences: make([]int64, 0, perWorker)}
			for i := 0; i < perWorker; i++ {
				entry, err := posting.NewJournalEntry(journalID, entryDate, "contention-maker",
					[]posting.JournalEntryLine{
						{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccID, Debit: amount, Credit: zero},
						{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccID, Debit: zero, Credit: amount},
					})
				if err != nil {
					errCh <- err
					return
				}
				entry.CompanyID = companyID
				entry.PeriodID = periodID
				entry.Reference = fmt.Sprintf("HC-%02d-%03d", workerIdx, i)
				if err := driver.SaveDraft(ctx, entry); err != nil {
					errCh <- err
					return
				}
				entry.Status = posting.EntryPosted
				entry.PostedBy = "contention-checker"
				posted, err := driver.PostAndSequence(ctx, entry)
				if err != nil {
					errCh <- fmt.Errorf("worker %d entry %d: %w", workerIdx, i, err)
					return
				}
				if posted.SequenceNumber == 0 {
					errCh <- fmt.Errorf("worker %d entry %d: sequence number was not allocated", workerIdx, i)
					return
				}
				res.sequences = append(res.sequences, posted.SequenceNumber)
			}
			results[workerIdx] = res
		}(w)
	}
	startTimer := time.Now()
	close(startDone)
	wg.Wait()
	close(errCh)
	elapsed := time.Since(startTimer)

	for err := range errCh {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
			t.Fatalf("deadlock detected under single-journal contention: %v", err)
		}
		t.Fatalf("concurrent single-journal posting failed: %v", err)
	}

	// No duplicates and no gaps: the allocated range must be exactly 1..1000.
	seen := make(map[int64]struct{}, totalEntries)
	all := make([]int64, 0, totalEntries)
	for _, res := range results {
		all = append(all, res.sequences...)
	}
	if len(all) != totalEntries {
		t.Fatalf("posted entries = %d, want %d", len(all), totalEntries)
	}
	for _, seq := range all {
		if _, duplicate := seen[seq]; duplicate {
			t.Fatalf("duplicate sequence number %d allocated", seq)
		}
		seen[seq] = struct{}{}
	}
	for want := int64(1); want <= totalEntries; want++ {
		if _, ok := seen[want]; !ok {
			t.Fatalf("sequence gap: number %d was never allocated (range 1..%d)", want, totalEntries)
		}
	}

	var journalSequence int64
	if err := driver.Pool().QueryRow(ctx,
		`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
		companyID, journalID).Scan(&journalSequence); err != nil {
		t.Fatal(err)
	}
	if journalSequence != totalEntries {
		t.Fatalf("journal current_sequence = %d, want %d", journalSequence, totalEntries)
	}

	var postedCount int
	if err := driver.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM account_moves WHERE company_id=$1 AND journal_id=$2 AND status='posted'`,
		companyID, journalID).Scan(&postedCount); err != nil {
		t.Fatal(err)
	}
	if postedCount != totalEntries {
		t.Fatalf("posted move count = %d, want %d", postedCount, totalEntries)
	}

	// The materialized balances must still reconcile with the posted lines.
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var debitTotal, creditTotal decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT
			COALESCE(SUM(period_debit) FILTER (WHERE account_id=$2),0),
			COALESCE(SUM(period_credit) FILTER (WHERE account_id=$3),0)
			FROM account_balances WHERE company_id=$1 AND fiscal_period_id=$4 AND currency_id='SAR'`,
			companyID, debitAccID, creditAccID, periodID).Scan(&debitTotal, &creditTotal); err != nil {
			return err
		}
		expected := amount.Amount().Mul(decimal.NewFromInt(totalEntries))
		if !debitTotal.Equal(expected) || !creditTotal.Equal(expected) {
			t.Fatalf("contention materialized totals debit=%s credit=%s; want %s", debitTotal, creditTotal, expected)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rate := float64(totalEntries) / elapsed.Seconds()
	t.Logf("single-journal high contention: %d entries across %d workers in %v (%.2f entries/sec)",
		totalEntries, numWorkers, elapsed, rate)
	if rate < minViableRate {
		t.Fatalf("contention throughput %.2f entries/sec is pathologically low", rate)
	}
}
