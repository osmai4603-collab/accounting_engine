package benchmark

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

var benchmarkAdapters = []string{"memory", "postgres"}

func BenchmarkDraftCreation(b *testing.B) {
	for _, adapterName := range benchmarkAdapters {
		b.Run(adapterName, func(b *testing.B) {
			adapter := openBenchmarkAdapter(b, adapterName)
			fixture := newBenchmarkFixture(b, adapter, 2026)
			for _, lineCount := range []int{2, 10, 50} {
				b.Run(fmt.Sprintf("%d_lines", lineCount), func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						entry := fixture.newEntry(b, lineCount, fixture.date)
						if err := fixture.posting.CreateDraftContext(context.Background(), entry); err != nil {
							b.Fatalf("create benchmark draft: %v", err)
						}
					}
					reportOperationsPerSecond(b)
				})
			}
		})
	}
}

func BenchmarkPostingPipeline(b *testing.B) {
	for _, adapterName := range benchmarkAdapters {
		b.Run(adapterName, func(b *testing.B) {
			fixture := newBenchmarkFixture(b, openBenchmarkAdapter(b, adapterName), 2026)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				postBenchmarkEntry(b, fixture, fixture.newEntry(b, 2, fixture.date))
			}
			reportOperationsPerSecond(b)
		})
	}
}

func BenchmarkConcurrentPosting(b *testing.B) {
	for _, adapterName := range benchmarkAdapters {
		b.Run(adapterName, func(b *testing.B) {
			fixture := newBenchmarkFixture(b, openBenchmarkAdapter(b, adapterName), 2026)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					lines := []posting.JournalEntryLine{
						{CompanyID: fixture.companyID, ID: uuid.New(), AccountID: fixture.debitID,
							Debit: fixture.amount, Credit: fixture.zero},
						{CompanyID: fixture.companyID, ID: uuid.New(), AccountID: fixture.creditID,
							Debit: fixture.zero, Credit: fixture.amount},
					}
					entry, err := posting.NewJournalEntry(fixture.journalID, fixture.date, "benchmark", lines)
					if err != nil {
						b.Errorf("create concurrent benchmark entry: %v", err)
						return
					}
					entry.CompanyID, entry.PeriodID = fixture.companyID, fixture.periodID
					if err := fixture.posting.CreateDraftContext(context.Background(), entry); err != nil {
						b.Errorf("create concurrent benchmark draft: %v", err)
						return
					}
					if err := fixture.posting.PostEntryContext(context.Background(), &entry, "benchmark-poster"); err != nil {
						b.Errorf("post concurrent benchmark entry: %v", err)
						return
					}
				}
			})
			reportOperationsPerSecond(b)
		})
	}
}

func BenchmarkBalanceSheetGeneration(b *testing.B) {
	for _, adapterName := range benchmarkAdapters {
		b.Run(adapterName, func(b *testing.B) {
			fixture := newBenchmarkFixture(b, openBenchmarkAdapter(b, adapterName), 2026)
			const accountPairs = 500
			lines := make([]posting.JournalEntryLine, 0, accountPairs*2)
			for i := 0; i < accountPairs; i++ {
				assetID, equityID := uuid.New(), uuid.New()
				for _, account := range []masterdata.Account{
					{
						CompanyID: fixture.companyID, ID: assetID, Code: fmt.Sprintf("A%04d", i),
						Name: "Benchmark asset", AccountType: masterdata.AccountTypeAsset,
						NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
						IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
					},
					{
						CompanyID: fixture.companyID, ID: equityID, Code: fmt.Sprintf("E%04d", i),
						Name: "Benchmark equity", AccountType: masterdata.AccountTypeEquity,
						NormalBalance: masterdata.NormalBalanceCredit, IsActive: true,
						IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
					},
				} {
					if err := fixture.adapter.driver.CreateAccount(context.Background(), account); err != nil {
						b.Fatalf("create balance-sheet benchmark account: %v", err)
					}
				}
				lines = append(lines,
					posting.JournalEntryLine{CompanyID: fixture.companyID, ID: uuid.New(),
						AccountID: assetID, Debit: fixture.amount, Credit: fixture.zero},
					posting.JournalEntryLine{CompanyID: fixture.companyID, ID: uuid.New(),
						AccountID: equityID, Debit: fixture.zero, Credit: fixture.amount},
				)
			}
			entry, err := posting.NewJournalEntry(fixture.journalID, fixture.date, "benchmark", lines)
			if err != nil {
				b.Fatal(err)
			}
			entry.CompanyID, entry.PeriodID = fixture.companyID, fixture.periodID
			postBenchmarkEntry(b, fixture, entry)

			engine := reporting.NewBalanceSheetEngine(fixture.adapter.driver)
			asOf := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				report, err := engine.GenerateForCompanyContext(
					context.Background(), fixture.companyID, asOf, "USD",
				)
				if err != nil {
					b.Fatalf("generate balance sheet: %v", err)
				}
				if !report.Balanced {
					b.Fatal("generated benchmark balance sheet is not balanced")
				}
			}
			reportOperationsPerSecond(b)
		})
	}
}

func BenchmarkCashFlowGeneration(b *testing.B) {
	for _, adapterName := range benchmarkAdapters {
		b.Run(adapterName, func(b *testing.B) {
			fixture := newBenchmarkFixture(b, openBenchmarkAdapter(b, adapterName), 2026)
			entry := fixture.newEntry(b, 2, fixture.date)
			postBenchmarkEntry(b, fixture, entry)

			start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
			end := time.Date(2026, time.December, 31, 23, 59, 59, 0, time.UTC)
			engine := reporting.NewCashFlowEngine(fixture.adapter.driver)
			for _, method := range []reporting.CashFlowMethod{
				reporting.CashFlowMethodIndirect, reporting.CashFlowMethodDirect,
			} {
				b.Run(string(method), func(b *testing.B) {
					request := reporting.CashFlowRequest{
						StartDate: start, EndDate: end, Currency: "USD",
						Method: method, Profile: reporting.CashFlowProfileIAS7,
					}
					if method == reporting.CashFlowMethodDirect {
						request.Transactions = map[uuid.UUID]reporting.CashFlowTransactionClassification{
							entry.Lines[0].ID: {
								Activity: reporting.CashFlowActivityOperating,
								Code:     "CUSTOMER_RECEIPTS", Label: "Customer receipts",
							},
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						report, err := engine.GenerateWithRequestForCompanyContext(
							context.Background(), fixture.companyID, request,
						)
						if err != nil {
							b.Fatalf("generate %s cash-flow statement: %v", method, err)
						}
						if !report.Reconciled {
							b.Fatalf("generated %s cash-flow statement is not reconciled", method)
						}
					}
					reportOperationsPerSecond(b)
				})
			}
		})
	}
}

func BenchmarkYearEndClosing(b *testing.B) {
	for _, adapterName := range benchmarkAdapters {
		b.Run(adapterName, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				result, err := benchmarkYearEndClose(b, adapterName, i)
				if err != nil {
					b.Fatal(err)
				}
				if result.OpeningEntryID == (types.JournalEntryID{}) {
					b.Fatal("year-end close did not create an opening entry")
				}
			}
			reportOperationsPerSecond(b)
		})
	}
}

func benchmarkYearEndClose(
	b *testing.B,
	adapterName string,
	iteration int,
) (publicapplication.YearEndClosingResult, error) {
	b.Helper()
	adapter := createBenchmarkAdapter(b, adapterName)
	if adapter.cleanup != nil {
		defer adapter.cleanup()
	}
	fixture := setupYearEndCloseFixture(b, adapter, iteration)
	coreStore, ok := adapter.driver.(legacy.CoreLedgerStore)
	if !ok {
		return publicapplication.YearEndClosingResult{}, fmt.Errorf(
			"%s adapter does not implement the SDK core storage contract", adapterName,
		)
	}
	reportingStore, ok := adapter.driver.(storage.ReportingStore)
	if !ok {
		return publicapplication.YearEndClosingResult{}, fmt.Errorf(
			"%s adapter does not implement the SDK reporting storage contract", adapterName,
		)
	}
	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(coreStore),
		sdk.WithReporting(reportingStore),
	).Build(context.Background())
	if err != nil {
		return publicapplication.YearEndClosingResult{}, fmt.Errorf("build SDK for year-end benchmark: %w", err)
	}
	closer, err := sdk.YearEndClosing(engine)
	if err != nil {
		_ = engine.Close()
		return publicapplication.YearEndClosingResult{}, fmt.Errorf("get year-end closing capability: %w", err)
	}
	command := publicapplication.ExecuteYearEndClosingCommand{
		Context: publicapplication.Context{
			Principal: publicapplication.Principal{
				ActorID:    uuid.NewString(),
				CompanyIDs: []types.CompanyID{types.CompanyID(fixture.companyID)},
			},
		},
		CompanyID:                 types.CompanyID(fixture.companyID),
		FiscalYearID:              types.FiscalYearID(fixture.year.ID),
		NextFiscalYearID:          types.FiscalYearID(fixture.nextYear.ID),
		JournalID:                 types.JournalID(fixture.journalID),
		RetainedEarningsAccountID: types.AccountID(fixture.retainedID),
		Policy:                    publicapplication.YearEndClosingDirectToRetainedEarnings,
		Currency:                  "USD", Reason: "Performance benchmark annual close",
	}
	b.StartTimer()
	result, closeErr := closer.ExecuteYearEndClosing(context.Background(), command)
	b.StopTimer()
	engineCloseErr := engine.Close()
	if closeErr != nil {
		return publicapplication.YearEndClosingResult{}, fmt.Errorf("execute atomic year-end close: %w", closeErr)
	}
	if engineCloseErr != nil {
		return publicapplication.YearEndClosingResult{}, fmt.Errorf("close SDK benchmark engine: %w", engineCloseErr)
	}
	return result, nil
}

func setupYearEndCloseFixture(b *testing.B, adapter benchmarkAdapter, iteration int) struct {
	companyID  uuid.UUID
	journalID  uuid.UUID
	year       masterdata.FiscalYear
	nextYear   masterdata.FiscalYear
	retainedID uuid.UUID
} {
	b.Helper()
	ctx := context.Background()
	fixture := newBenchmarkFixture(b, adapter, 2025)
	retainedID, expenseID := uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{
			CompanyID: fixture.companyID, ID: retainedID, Code: "3300", Name: "Retained earnings",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			SubClassification: masterdata.SubClassRetainedEarnings, IsActive: true,
		},
		{
			CompanyID: fixture.companyID, ID: expenseID, Code: "5000", Name: "Operating expense",
			AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit,
			SubClassification: masterdata.SubClassCostOfSales, IsActive: true,
		},
		{
			CompanyID: fixture.companyID, ID: uuid.New(), Code: "3990", Name: "Income summary",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			SubClassification: "income_summary", IsActive: true,
		},
	} {
		if err := adapter.driver.CreateAccount(ctx, account); err != nil {
			b.Fatalf("create year-end benchmark account: %v", err)
		}
	}
	nextYear := masterdata.GenerateFiscalYear(fixture.year.StartDate.AddDate(1, 0, 0))
	nextYear.CompanyID = fixture.companyID
	if err := adapter.driver.CreateFiscalYear(ctx, nextYear); err != nil {
		b.Fatalf("create next benchmark fiscal year: %v", err)
	}

	closeDate := fixture.year.StartDate.AddDate(0, 0, 14)
	expenseDate := fixture.year.StartDate.AddDate(0, 1, 14)
	revenueAmount, err := financialtypes.NewMoneyFromString("2.00", "USD")
	if err != nil {
		b.Fatal(err)
	}
	postBenchmarkEntry(b, fixture, benchmarkPairEntry(
		b, fixture, fixture.year.Periods[0].ID, closeDate, fixture.debitID, fixture.creditID, revenueAmount, iteration*2,
	))
	postBenchmarkEntry(b, fixture, benchmarkPairEntry(
		b, fixture, fixture.year.Periods[1].ID, expenseDate, expenseID, fixture.debitID, fixture.amount, iteration*2+1,
	))
	for i := 0; i < 12; i++ {
		period := fixture.year.Periods[i]
		if err := adapter.driver.UpdatePeriodStateForCompanyContext(
			ctx, fixture.companyID, period.ID, masterdata.PeriodSoftClosed,
		); err != nil {
			b.Fatalf("soft-close benchmark fiscal period: %v", err)
		}
		if err := adapter.driver.UpdatePeriodStateForCompanyContext(
			ctx, fixture.companyID, period.ID, masterdata.PeriodHardClosed,
		); err != nil {
			b.Fatalf("hard-close benchmark fiscal period: %v", err)
		}
	}
	return struct {
		companyID  uuid.UUID
		journalID  uuid.UUID
		year       masterdata.FiscalYear
		nextYear   masterdata.FiscalYear
		retainedID uuid.UUID
	}{
		companyID: fixture.companyID, journalID: fixture.journalID,
		year: fixture.year, nextYear: nextYear, retainedID: retainedID,
	}
}

func benchmarkPairEntry(
	b *testing.B,
	fixture benchmarkFixture,
	periodID uuid.UUID,
	date time.Time,
	debitID, creditID uuid.UUID,
	amount financialtypes.Money,
	iteration int,
) posting.JournalEntry {
	b.Helper()
	lines := []posting.JournalEntryLine{
		{CompanyID: fixture.companyID, ID: uuid.New(), AccountID: debitID,
			Debit: amount, Credit: fixture.zero},
		{CompanyID: fixture.companyID, ID: uuid.New(), AccountID: creditID,
			Debit: fixture.zero, Credit: amount},
	}
	entry, err := posting.NewJournalEntry(fixture.journalID, date, "benchmark", lines)
	if err != nil {
		b.Fatalf("create year-end benchmark entry: %v", err)
	}
	entry.CompanyID, entry.PeriodID = fixture.companyID, periodID
	entry.Reference = fmt.Sprintf("YE-BENCH-%d", iteration)
	return entry
}

func reportOperationsPerSecond(b *testing.B) {
	b.Helper()
	if elapsed := b.Elapsed().Seconds(); elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed, "ops/s")
	}
}
