package benchmark

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
)

type benchmarkStore interface {
	posting.Repository
	reporting.Repository
	CreateCompany(context.Context, masterdata.Company) error
	CreateAccount(context.Context, masterdata.Account) error
	CreateFiscalYear(context.Context, masterdata.FiscalYear) error
	UpdatePeriodStateForCompanyContext(context.Context, uuid.UUID, uuid.UUID, masterdata.PeriodState) error
}

type benchmarkAdapter struct {
	name       string
	driver     benchmarkStore
	masterdata publicmasterdata.Store
	cleanup    func()
}

type benchmarkFixture struct {
	adapter   benchmarkAdapter
	companyID uuid.UUID
	journalID uuid.UUID
	periodID  uuid.UUID
	debitID   uuid.UUID
	creditID  uuid.UUID
	date      time.Time
	year      masterdata.FiscalYear
	posting   *posting.Engine
	amount    financialtypes.Money
	zero      financialtypes.Money
}

func openBenchmarkAdapter(b *testing.B, name string) benchmarkAdapter {
	b.Helper()
	adapter := createBenchmarkAdapter(b, name)
	if adapter.cleanup != nil {
		b.Cleanup(adapter.cleanup)
	}
	return adapter
}

func createBenchmarkAdapter(b *testing.B, name string) benchmarkAdapter {
	b.Helper()
	switch name {
	case "memory":
		return newBenchmarkAdapter(b, name, memory.New(), nil)
	case "postgres":
		return openPostgresBenchmarkAdapter(b)
	default:
		b.Fatalf("unknown benchmark adapter %q", name)
		return benchmarkAdapter{}
	}
}

func newBenchmarkAdapter(b *testing.B, name string, driver benchmarkStore, cleanup func()) benchmarkAdapter {
	b.Helper()
	provider, ok := any(driver).(interface {
		MasterDataStore() publicmasterdata.Store
	})
	if !ok {
		b.Fatalf("%s adapter does not expose public master-data storage", name)
	}
	return benchmarkAdapter{
		name: name, driver: driver, masterdata: provider.MasterDataStore(), cleanup: cleanup,
	}
}

func openPostgresBenchmarkAdapter(b *testing.B) benchmarkAdapter {
	b.Helper()
	ctx := context.Background()
	connectionString := os.Getenv("POSTGRES_URL")
	if connectionString == "" {
		connectionString = "postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql"
	}

	adminPool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		b.Skipf("PostgreSQL benchmark unavailable: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		b.Skipf("PostgreSQL benchmark unavailable: %v", err)
	}

	schemaName := "benchmark_" + uuid.NewString()
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminPool.Close()
		b.Fatalf("create isolated benchmark schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		adminPool.Close()
		b.Fatalf("parse PostgreSQL benchmark connection: %v", err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	testPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		adminPool.Close()
		b.Fatalf("open isolated PostgreSQL benchmark schema: %v", err)
	}
	var currentSchema string
	if err := testPool.QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		testPool.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		adminPool.Close()
		b.Fatalf("verify isolated PostgreSQL benchmark schema: %v", err)
	}
	if currentSchema != schemaName {
		testPool.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		adminPool.Close()
		b.Fatalf("PostgreSQL benchmark search_path selected %q, want %q", currentSchema, schemaName)
	}
	if err := postgres.EnsureSchema(ctx, testPool); err != nil {
		testPool.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		adminPool.Close()
		b.Fatalf("initialize isolated PostgreSQL benchmark schema: %v", err)
	}

	driver := postgres.NewWithPool(testPool)
	cleanup := func() {
		driver.Close()
		if _, err := adminPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			b.Errorf("drop isolated PostgreSQL benchmark schema: %v", err)
		}
		adminPool.Close()
	}
	return newBenchmarkAdapter(b, "postgres", driver, cleanup)
}

func newBenchmarkFixture(b *testing.B, adapter benchmarkAdapter, yearNumber int) benchmarkFixture {
	b.Helper()
	ctx := context.Background()
	companyID, journalID := uuid.New(), uuid.New()
	code := fmt.Sprintf("BENCH-%s", companyID.String()[:8])
	if err := adapter.driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: code, LegalName: "Performance Benchmark",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		b.Fatalf("create benchmark company: %v", err)
	}
	if err := adapter.masterdata.CreateJournal(ctx, publicmasterdata.Journal{
		ID: publicmasterdata.JournalID(journalID), CompanyID: types.CompanyID(companyID),
		Code: "GEN", Name: "General",
	}); err != nil {
		b.Fatalf("create benchmark journal: %v", err)
	}

	date := time.Date(yearNumber, time.January, 15, 12, 0, 0, 0, time.UTC)
	year := masterdata.GenerateFiscalYear(time.Date(yearNumber, time.January, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := adapter.driver.CreateFiscalYear(ctx, year); err != nil {
		b.Fatalf("create benchmark fiscal year: %v", err)
	}

	debitID, creditID := uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{
			CompanyID: companyID, ID: debitID, Code: "1000", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
			IsActive: true, IsCashEquivalent: true, CashFlowActivity: masterdata.CashFlowCashEquivalent,
			IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: creditID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, SubClassification: masterdata.SubClassOperatingRevenue,
			CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryOperating,
		},
	} {
		if err := adapter.driver.CreateAccount(ctx, account); err != nil {
			b.Fatalf("create benchmark account %s: %v", account.Code, err)
		}
	}

	amount, err := financialtypes.NewMoneyFromString("1.00", "USD")
	if err != nil {
		b.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		b.Fatal(err)
	}
	engine := posting.NewEngine(adapter.driver, nil)
	if locker, ok := adapter.driver.(posting.PostingLocker); ok {
		engine.SetPostingLocker(locker)
	}
	return benchmarkFixture{
		adapter: adapter, companyID: companyID, journalID: journalID,
		periodID: year.Periods[0].ID, debitID: debitID, creditID: creditID,
		date: date, year: year, posting: engine, amount: amount, zero: zero,
	}
}

func (f benchmarkFixture) newEntry(b *testing.B, lineCount int, date time.Time) posting.JournalEntry {
	b.Helper()
	lines := make([]posting.JournalEntryLine, lineCount)
	for i := range lines {
		line := posting.JournalEntryLine{
			CompanyID: f.companyID, ID: uuid.New(),
			Debit: f.zero, Credit: f.zero,
		}
		if i%2 == 0 {
			line.AccountID, line.Debit = f.debitID, f.amount
		} else {
			line.AccountID, line.Credit = f.creditID, f.amount
		}
		lines[i] = line
	}
	entry, err := posting.NewJournalEntry(f.journalID, date, "benchmark", lines)
	if err != nil {
		b.Fatalf("create benchmark journal entry: %v", err)
	}
	entry.CompanyID = f.companyID
	entry.PeriodID = f.periodID
	return entry
}

func postBenchmarkEntry(b *testing.B, f benchmarkFixture, entry posting.JournalEntry) {
	b.Helper()
	if err := f.posting.CreateDraftContext(context.Background(), entry); err != nil {
		b.Fatalf("create benchmark draft: %v", err)
	}
	if err := f.posting.PostEntryContext(context.Background(), &entry, "benchmark-poster"); err != nil {
		b.Fatalf("post benchmark entry: %v", err)
	}
}
