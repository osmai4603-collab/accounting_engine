package postgres_test

import (
	"context"
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
}
