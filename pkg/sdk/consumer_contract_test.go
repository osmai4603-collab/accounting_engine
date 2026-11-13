package sdk_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

func TestSDKConsumerContractAcrossStorage(t *testing.T) {
	var memoryEntry ledger.JournalEntry
	if !t.Run("memory", func(t *testing.T) {
		memoryDriver := memory.New()
		memoryEntry = runSDKConsumerJourney(t, memoryDriver, memoryDriver.MasterDataStore())
	}) {
		return
	}

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("POSTGRES_URL")
		if dsn == "" {
			t.Skip("POSTGRES_URL is not set")
		}

		driver := isolatedSDKContractPostgresDriver(t, dsn)
		postgresEntry := runSDKConsumerJourney(t, driver, driver.MasterDataStore())
		if got, want := snapshotSDKConsumerEntry(postgresEntry), snapshotSDKConsumerEntry(memoryEntry); !reflect.DeepEqual(got, want) {
			t.Fatalf("PostgreSQL SDK result differs from Memory:\nPostgreSQL: %+v\nMemory:     %+v", got, want)
		}
	})
}

func runSDKConsumerJourney(t *testing.T, core legacy.CoreLedgerStore, masterData publicmasterdata.Store) ledger.JournalEntry {
	t.Helper()
	ctx := context.Background()

	companyID := types.CompanyID(uuid.MustParse("10000000-0000-4000-8000-000000000001"))
	cashAccountID := types.AccountID(uuid.MustParse("20000000-0000-4000-8000-000000000001"))
	revenueAccountID := types.AccountID(uuid.MustParse("20000000-0000-4000-8000-000000000002"))
	yearID := types.FiscalYearID(uuid.MustParse("30000000-0000-4000-8000-000000000001"))
	periodID := types.FiscalPeriodID(uuid.MustParse("40000000-0000-4000-8000-000000000001"))

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "ACME", LegalName: "Acme Example Ltd",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	journalID := publicmasterdata.NewJournalID()
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "SDK-CONTRACT", Name: "SDK contract journal",
	}); err != nil {
		t.Fatalf("create journal: %v", err)
	}

	for _, account := range []publicmasterdata.Account{
		{
			ID: cashAccountID, CompanyID: companyID, Code: "1000", Name: "Cash",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		{
			ID: revenueAccountID, CompanyID: companyID, Code: "4000", Name: "Revenue",
			Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			t.Fatalf("create account %s: %v", account.Code, err)
		}
	}

	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: start, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatalf("create fiscal year: %v", err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}

	engine, err := sdk.NewBuilder(sdk.WithCoreStorage(core)).Build(ctx)
	if err != nil {
		t.Fatalf("build SDK engine: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close SDK engine: %v", err)
		}
	}()

	amount, err := types.NewMoneyFromString("125.00", "USD")
	if err != nil {
		t.Fatalf("create amount: %v", err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		t.Fatalf("create zero amount: %v", err)
	}
	entry := ledger.JournalEntry{
		ID:        types.JournalEntryID(uuid.MustParse("50000000-0000-4000-8000-000000000001")),
		CompanyID: companyID, JournalID: types.JournalID(journalID),
		FiscalPeriodID: periodID, Status: ledger.EntryDraft,
		Classification: ledger.EntryClassRegular,
		EffectiveDate:  time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		PostingDate:    time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		Description:    "Cash sale", SourceModule: "sdk-contract",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
		CreatedBy: "sdk-contract",
		Lines: []ledger.EntryLine{
			{
				ID:        types.JournalEntryLineID(uuid.MustParse("60000000-0000-4000-8000-000000000001")),
				AccountID: cashAccountID, TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID:        types.JournalEntryLineID(uuid.MustParse("60000000-0000-4000-8000-000000000002")),
				AccountID: revenueAccountID, TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}

	posted, err := engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		t.Fatalf("post journal entry through SDK: %v", err)
	}
	readBack, err := engine.Ledger().GetEntry(ctx, companyID, posted.ID)
	if err != nil {
		t.Fatalf("read journal entry through SDK: %v", err)
	}
	if readBack.Status != ledger.EntryPosted || len(readBack.Lines) != 2 {
		t.Fatalf("unexpected SDK read-back: status=%q lines=%d", readBack.Status, len(readBack.Lines))
	}
	if got, want := snapshotSDKConsumerEntry(readBack), snapshotSDKConsumerEntry(posted); !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK read-back differs from posted result:\nRead-back: %+v\nPosted:    %+v", got, want)
	}
	return readBack
}

type sdkConsumerLineSnapshot struct {
	ID                  string
	AccountID           string
	TransactionAmount   string
	TransactionCurrency string
	ExchangeRate        string
	FunctionalDebit     string
	FunctionalCredit    string
}

type sdkConsumerEntryView struct {
	ID                  string
	CompanyID           string
	FiscalPeriodID      string
	EntryNumber         string
	Status              ledger.EntryStatus
	Classification      ledger.EntryClassification
	EffectiveDate       string
	PostingDate         string
	Description         string
	SourceModule        string
	TransactionCurrency string
	ExchangeRate        string
	CreatedBy           string
	Lines               []sdkConsumerLineSnapshot
}

func snapshotSDKConsumerEntry(entry ledger.JournalEntry) sdkConsumerEntryView {
	snapshot := sdkConsumerEntryView{
		ID: uuid.UUID(entry.ID).String(), CompanyID: uuid.UUID(entry.CompanyID).String(),
		FiscalPeriodID: uuid.UUID(entry.FiscalPeriodID).String(), EntryNumber: entry.EntryNumber,
		Status: entry.Status, Classification: entry.Classification,
		EffectiveDate: entry.EffectiveDate.UTC().Format(time.RFC3339Nano),
		PostingDate:   entry.PostingDate.UTC().Format(time.RFC3339Nano),
		Description:   entry.Description, SourceModule: entry.SourceModule,
		TransactionCurrency: entry.TransactionCurrency, ExchangeRate: entry.ExchangeRate.String(),
		CreatedBy: entry.CreatedBy, Lines: make([]sdkConsumerLineSnapshot, 0, len(entry.Lines)),
	}
	for _, line := range entry.Lines {
		snapshot.Lines = append(snapshot.Lines, sdkConsumerLineSnapshot{
			ID: uuid.UUID(line.ID).String(), AccountID: uuid.UUID(line.AccountID).String(),
			TransactionAmount:   line.TransactionAmount.String(),
			TransactionCurrency: line.TransactionCurrency, ExchangeRate: line.ExchangeRate.String(),
			FunctionalDebit: line.FunctionalDebit.String(), FunctionalCredit: line.FunctionalCredit.String(),
		})
	}
	return snapshot
}

func isolatedSDKContractPostgresDriver(t *testing.T, dsn string) *postgres.Driver {
	t.Helper()
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL test DSN: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("ping PostgreSQL test DSN: %v", err)
	}

	schemaName := "sdk_contract_" + uuid.NewString()
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}

	var testPool *pgxpool.Pool
	t.Cleanup(func() {
		if testPool != nil {
			testPool.Close()
		}
		if _, err := adminPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop isolated PostgreSQL schema: %v", err)
		}
		adminPool.Close()
	})

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL test DSN: %v", err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	testPool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create isolated PostgreSQL pool: %v", err)
	}
	var currentSchema string
	if err := testPool.QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		t.Fatalf("verify isolated PostgreSQL search_path: %v", err)
	}
	if currentSchema != schemaName {
		t.Fatalf("PostgreSQL search_path selected %q, want %q", currentSchema, schemaName)
	}
	if err := postgres.EnsureSchema(ctx, testPool); err != nil {
		t.Fatalf("initialize isolated PostgreSQL schema: %v", err)
	}
	return postgres.NewWithPool(testPool)
}
