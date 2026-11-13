package sdk_test

import (
	"context"
	"os"
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
	const failureReason = "simulated post-response failure"
	if err := idempotencyStore.FailPublicEventIngestion(ctx, uuid.UUID(companyID), "recover-me", failureReason); err != nil {
		t.Fatalf("mark ingestion failed for retry: %v", err)
	}
	failedRecord, found, err := idempotencyStore.GetPublicEventIngestion(ctx, uuid.UUID(companyID), "recover-me")
	if err != nil || !found {
		t.Fatalf("read failed idempotency record: found=%t err=%v", found, err)
	}
	if failedRecord.Status != "failed" || failedRecord.FailureReason != failureReason {
		t.Fatalf("failed idempotency state = %q/%q, want failed/%q",
			failedRecord.Status, failedRecord.FailureReason, failureReason)
	}
	retryEntry := draft
	retryEntry.ID = types.NewJournalEntryID()
	retry, claimed, err := idempotencyStore.ClaimPublicEventIngestion(ctx, sdk.PublicEventIngestion{
		CompanyID: uuid.UUID(companyID), IdempotencyKey: "recover-me", EventType: "sale",
		PayloadHash: record.PayloadHash, EntryID: uuid.UUID(retryEntry.ID), Entry: retryEntry, Status: "processing",
	})
	if err != nil || !claimed {
		t.Fatalf("reclaim failed ingestion: claimed=%t err=%v", claimed, err)
	}
	if retry.EntryID != uuid.UUID(entryID) || uuid.UUID(retry.Entry.ID) != uuid.UUID(entryID) {
		t.Fatalf("retry replaced original entry ID: record=%s snapshot=%s, want %s",
			retry.EntryID, retry.Entry.ID, entryID)
	}
	if err := idempotencyStore.CompletePublicEventIngestion(ctx, uuid.UUID(companyID), "recover-me", result); err != nil {
		t.Fatalf("complete retried ingestion: %v", err)
	}
	conflictingEntry := result
	conflictingEntry.ID = types.NewJournalEntryID()
	if err := idempotencyStore.CompletePublicEventIngestion(ctx, uuid.UUID(companyID), "recover-me", conflictingEntry); err != sdk.ErrIdempotencyStateConflict {
		t.Fatalf("conflicting completion error = %v, want %v", err, sdk.ErrIdempotencyStateConflict)
	}
}
