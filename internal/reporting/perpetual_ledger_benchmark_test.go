package reporting_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"

	"github.com/google/uuid"
)

type historicalOnlyBenchmarkRepository struct {
	*memory.Driver
}

func (r historicalOnlyBenchmarkRepository) ReadAccountPeriodBalancesForCompanyContext(
	ctx context.Context,
	_ uuid.UUID,
	_, _ time.Time,
	_ string,
	_ bool,
) (map[uuid.UUID]posting.AccountPeriodBalance, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func BenchmarkTrialBalanceMaterialized10KAccounts(b *testing.B) {
	ctx := context.Background()
	driver := memory.New()
	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "TB-BENCH-" + companyID.String()[:8], LegalName: "Trial Balance Benchmark",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		b.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	period := masterdata.FiscalPeriod{
		CompanyID: companyID, ID: uuid.New(), Number: 1,
		StartDate: start, EndDate: end, State: masterdata.PeriodOpen,
	}
	year := masterdata.FiscalYear{
		CompanyID: companyID, ID: uuid.New(), Name: "FY2026",
		StartDate: start, EndDate: end, Periods: []masterdata.FiscalPeriod{period},
	}
	period.FiscalYearID = year.ID
	year.Periods[0] = period
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		b.Fatal(err)
	}

	amount, err := financialtypes.NewMoneyFromString("1.00", "USD")
	if err != nil {
		b.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		b.Fatal(err)
	}
	const accountCount = 10_000
	lines := make([]posting.JournalEntryLine, 0, accountCount)
	for i := 0; i < accountCount/2; i++ {
		asset := masterdata.Account{
			CompanyID: companyID, ID: uuid.New(), Code: fmt.Sprintf("A%05d", i), Name: "Asset",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		}
		equity := masterdata.Account{
			CompanyID: companyID, ID: uuid.New(), Code: fmt.Sprintf("E%05d", i), Name: "Equity",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
		}
		for _, account := range []masterdata.Account{asset, equity} {
			if err := driver.CreateAccount(ctx, account); err != nil {
				b.Fatal(err)
			}
		}
		lines = append(lines,
			posting.JournalEntryLine{CompanyID: companyID, ID: uuid.New(), AccountID: asset.ID, Debit: amount, Credit: zero},
			posting.JournalEntryLine{CompanyID: companyID, ID: uuid.New(), AccountID: equity.ID, Debit: zero, Credit: amount},
		)
	}
	entry, err := posting.NewJournalEntry(uuid.New(), start.Add(time.Hour), "benchmark", lines)
	if err != nil {
		b.Fatal(err)
	}
	entry.CompanyID, entry.PeriodID = companyID, period.ID
	if err := driver.SaveDraft(entry); err != nil {
		b.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(entry); err != nil {
		b.Fatal(err)
	}

	for _, benchmark := range []struct {
		name string
		repo reporting.Repository
	}{
		{name: "materialized", repo: driver},
		{name: "historical_aggregation", repo: historicalOnlyBenchmarkRepository{Driver: driver}},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			engine := reporting.NewTrialBalanceEngine(benchmark.repo)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				report, err := engine.GenerateForCompany(companyID, start, end, "USD")
				if err != nil {
					b.Fatal(err)
				}
				if len(report.Lines) != accountCount || !report.Balanced {
					b.Fatalf("trial balance lines=%d balanced=%v", len(report.Lines), report.Balanced)
				}
			}
		})
	}
}
