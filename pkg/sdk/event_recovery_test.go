package sdk_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	publicmasterdata "accounting_engine/pkg/spi/masterdata"
	"accounting_engine/pkg/spi/storage"
	"accounting_engine/storage/memory"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestPublicEventIngestionRecoversProcessingRecord(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		driver := memory.New()
		runPublicEventIngestionRecovery(t, driver, driver.MasterDataStore())
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("POSTGRES_URL")
		if dsn == "" {
			dsn = "postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql"
		}
		driver := isolatedSDKContractPostgresDriver(t, dsn)
		runPublicEventIngestionRecovery(t, driver, driver.MasterDataStore())
	})
}

func runPublicEventIngestionRecovery(t *testing.T, core storage.CoreLedgerStore, masterData publicmasterdata.Store) {
	t.Helper()
	ctx := context.Background()

	companyID := types.CompanyID(uuid.New())
	journalID := publicmasterdata.NewJournalID()
	debitID := types.AccountID(uuid.New())
	creditID := types.AccountID(uuid.New())
	yearID := types.FiscalYearID(uuid.New())
	periodID := types.FiscalPeriodID(uuid.New())
	entryID := types.JournalEntryID(uuid.New())
	createdAt := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "RECOVERY", LegalName: "Recovery Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "RECOVERY-TEST", Name: "Recovery test journal",
	}); err != nil {
		t.Fatalf("create journal: %v", err)
	}
	for _, account := range []publicmasterdata.Account{
		{
			ID: debitID, CompanyID: companyID, Code: "1000", Name: "Cash",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		{
			ID: creditID, CompanyID: companyID, Code: "4000", Name: "Revenue",
			Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	yearStart := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: yearStart, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID, Number: 1, Name: "January",
		StartDate: yearStart, EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatal(err)
	}

	amount, err := types.NewMoneyFromString("100.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		t.Fatal(err)
	}
	draft := ledger.JournalEntry{
		ID: entryID, CompanyID: companyID, JournalID: types.JournalID(journalID), FiscalPeriodID: periodID,
		Status: ledger.EntryDraft, EffectiveDate: createdAt, PostingDate: createdAt,
		Description: "Recovered event", SourceModule: "recovery-test",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), CreatedBy: "recovery-test",
		Lines: []ledger.EntryLine{
			{
				ID: types.JournalEntryLineID(uuid.New()), AccountID: debitID,
				TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.JournalEntryLineID(uuid.New()), AccountID: creditID,
				TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}
	idempotencyStore, ok := core.(sdk.PublicEventIdempotencyStore)
	if !ok {
		t.Fatal("storage does not implement public event idempotency")
	}
	if _, claimed, err := idempotencyStore.ClaimPublicEventIngestion(ctx, sdk.PublicEventIngestion{
		CompanyID: uuid.UUID(companyID), IdempotencyKey: "recover-me",
		EventType: "sale", EntryID: uuid.UUID(entryID), Entry: draft, Status: "processing",
	}); err != nil || !claimed {
		t.Fatalf("seed processing record: claimed=%t err=%v", claimed, err)
	}

	t.Run("concurrent same-key claim", func(t *testing.T) {
		const attempts = 16
		var wg sync.WaitGroup
		wg.Add(attempts)
		type claimResult struct {
			record  sdk.PublicEventIngestion
			claimed bool
			err     error
		}
		results := make(chan claimResult, attempts)
		for i := 0; i < attempts; i++ {
			go func() {
				defer wg.Done()
				rec, claimed, err := idempotencyStore.ClaimPublicEventIngestion(ctx, sdk.PublicEventIngestion{
					CompanyID: uuid.UUID(companyID), IdempotencyKey: "concurrent-claim",
					EventType: "sale", PayloadHash: "same-payload", EntryID: uuid.UUID(entryID),
					Entry: draft, Status: "processing",
				})
				results <- claimResult{record: rec, claimed: claimed, err: err}
			}()
		}
		wg.Wait()
		close(results)

		claimedCount := 0
		for result := range results {
			if result.err != nil {
				t.Fatalf("claim same key concurrently: %v", result.err)
			}
			if result.claimed {
				claimedCount++
			}
			if result.record.EntryID != uuid.UUID(entryID) {
				t.Fatalf("concurrent claim returned entry %s, want %s", result.record.EntryID, entryID)
			}
		}
		if claimedCount != 1 {
			t.Fatalf("same-key concurrent claims succeeded %d times, want exactly once", claimedCount)
		}
	})

	engine, err := sdk.NewBuilder(sdk.WithCoreStorage(core)).Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close SDK: %v", err)
		}
	}()
	result, err := engine.Events().ProcessEvent(ctx, events.AccountingEvent{
		EventID: "recover-event", EventType: "sale", CompanyID: companyID,
		EffectiveDate: createdAt, IdempotencyKey: "recover-me", JournalDraft: &draft,
	})
	if err != nil {
		t.Fatalf("recover event ingestion: %v", err)
	}
	if result.Status != ledger.EntryPosted || result.ID != entryID {
		t.Fatalf("recovered entry = status %q, ID %s; want posted entry %s", result.Status, result.ID, entryID)
	}
	record, found, err := idempotencyStore.GetPublicEventIngestion(ctx, uuid.UUID(companyID), "recover-me")
	if err != nil || !found {
		t.Fatalf("read recovered idempotency record: found=%t err=%v", found, err)
	}
	if record.Status != "processed" || record.EntryID != uuid.UUID(entryID) {
		t.Fatalf("recovered idempotency record = status %q, entry %s", record.Status, record.EntryID)
	}
	if record.Entry.ID != entryID || record.Entry.Status != ledger.EntryPosted {
		t.Fatalf("completed record snapshot = entry %s status %q, want posted entry %s",
			record.Entry.ID, record.Entry.Status, entryID)
	}
	replayed, err := engine.Events().ProcessEvent(ctx, events.AccountingEvent{
		EventID: "recover-event", EventType: "sale", CompanyID: companyID,
		EffectiveDate: createdAt, IdempotencyKey: "recover-me", JournalDraft: &draft,
	})
	if err != nil {
		t.Fatalf("replay completed event: %v", err)
	}
	if replayed.ID != result.ID || replayed.Status != ledger.EntryPosted {
		t.Fatalf("replayed event = %s/%q, want original posted entry %s", replayed.ID, replayed.Status, result.ID)
	}
	reportingStore, ok := core.(storage.ReportingStore)
	if !ok {
		t.Fatal("storage does not implement reporting")
	}
	trialBalance, err := reportingStore.GetTrialBalance(ctx, companyID, createdAt, "USD")
	if err != nil {
		t.Fatalf("read trial balance after event replay: %v", err)
	}
	if !trialBalance.TotalDebit.Amount().Equal(decimal.NewFromInt(100)) ||
		!trialBalance.TotalCredit.Amount().Equal(decimal.NewFromInt(100)) {
		t.Fatalf("replaying the same event changed posted totals: debit=%s credit=%s, want 100 each",
			trialBalance.TotalDebit, trialBalance.TotalCredit)
	}

	const failureReason = "simulated post-response failure"
	if _, claimed, err := idempotencyStore.ClaimPublicEventIngestion(ctx, sdk.PublicEventIngestion{
		CompanyID: uuid.UUID(companyID), IdempotencyKey: "retry-after-failure", EventType: "sale",
		PayloadHash: record.PayloadHash, EntryID: uuid.UUID(entryID), Entry: draft, Status: "processing",
	}); err != nil || !claimed {
		t.Fatalf("seed ingestion for failure retry: claimed=%t err=%v", claimed, err)
	}
	if err := idempotencyStore.FailPublicEventIngestion(ctx, uuid.UUID(companyID), "retry-after-failure", failureReason); err != nil {
		t.Fatalf("mark ingestion failed for retry: %v", err)
	}
	failedRecord, found, err := idempotencyStore.GetPublicEventIngestion(ctx, uuid.UUID(companyID), "retry-after-failure")
	if err != nil || !found {
		t.Fatalf("read failed idempotency record: found=%t err=%v", found, err)
	}
	if failedRecord.Status != "failed" || failedRecord.FailureReason != failureReason ||
		failedRecord.EntryID != uuid.UUID(entryID) || failedRecord.Entry.ID != entryID {
		t.Fatalf("failed idempotency state = %q/%q entry=%s snapshot=%s, want failed/%q entry=%s",
			failedRecord.Status, failedRecord.FailureReason, failedRecord.EntryID, failedRecord.Entry.ID, failureReason, entryID)
	}
	retryEntry := draft
	retryEntry.ID = types.NewJournalEntryID()
	retry, claimed, err := idempotencyStore.ClaimPublicEventIngestion(ctx, sdk.PublicEventIngestion{
		CompanyID: uuid.UUID(companyID), IdempotencyKey: "retry-after-failure", EventType: "sale",
		PayloadHash: record.PayloadHash, EntryID: uuid.UUID(retryEntry.ID), Entry: retryEntry, Status: "processing",
	})
	if err != nil || !claimed {
		t.Fatalf("reclaim failed ingestion: claimed=%t err=%v", claimed, err)
	}
	if retry.Status != "processing" || retry.FailureReason != "" {
		t.Fatalf("reclaimed ingestion state = %q/%q, want processing with cleared failure reason", retry.Status, retry.FailureReason)
	}
	if retry.EntryID != uuid.UUID(entryID) || uuid.UUID(retry.Entry.ID) != uuid.UUID(entryID) {
		t.Fatalf("retry replaced original entry ID: record=%s snapshot=%s, want %s",
			retry.EntryID, retry.Entry.ID, entryID)
	}
	if err := idempotencyStore.FailPublicEventIngestion(ctx, uuid.UUID(companyID), "recover-me", failureReason); err != sdk.ErrIdempotencyStateConflict {
		t.Fatalf("failing a processed record error = %v, want %v", err, sdk.ErrIdempotencyStateConflict)
	}
	stillProcessed, found, err := idempotencyStore.GetPublicEventIngestion(ctx, uuid.UUID(companyID), "recover-me")
	if err != nil || !found || stillProcessed.Status != "processed" || stillProcessed.FailureReason != "" {
		t.Fatalf("failed transition changed completed record: found=%t record=%+v err=%v", found, stillProcessed, err)
	}
	conflictingEntry := result
	conflictingEntry.ID = types.NewJournalEntryID()
	if err := idempotencyStore.CompletePublicEventIngestion(ctx, uuid.UUID(companyID), "retry-after-failure", conflictingEntry); err != sdk.ErrIdempotencyStateConflict {
		t.Fatalf("conflicting processing completion error = %v, want %v", err, sdk.ErrIdempotencyStateConflict)
	}
	if err := idempotencyStore.CompletePublicEventIngestion(ctx, uuid.UUID(companyID), "retry-after-failure", result); err != nil {
		t.Fatalf("complete retried ingestion: %v", err)
	}
	completedRetry, found, err := idempotencyStore.GetPublicEventIngestion(ctx, uuid.UUID(companyID), "retry-after-failure")
	if err != nil || !found {
		t.Fatalf("read completed retry: found=%t err=%v", found, err)
	}
	if completedRetry.Status != "processed" || completedRetry.FailureReason != "" ||
		completedRetry.EntryID != uuid.UUID(entryID) || completedRetry.Entry.ID != entryID ||
		completedRetry.Entry.Status != ledger.EntryPosted {
		t.Fatalf("completed retry state = %+v, want persisted posted entry %s with no failure", completedRetry, entryID)
	}
	if err := idempotencyStore.CompletePublicEventIngestion(ctx, uuid.UUID(companyID), "retry-after-failure", conflictingEntry); err != sdk.ErrIdempotencyStateConflict {
		t.Fatalf("conflicting completion error = %v, want %v", err, sdk.ErrIdempotencyStateConflict)
	}
}
