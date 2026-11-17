package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
)

type p901PostgresFixture struct {
	ctx        context.Context
	driver     *postgres.Driver
	engine     sdk.Engine
	companyID  uuid.UUID
	journalID  uuid.UUID
	year       masterdata.FiscalYear
	nextYear   masterdata.FiscalYear
	retainedID uuid.UUID
	summaryID  uuid.UUID
	actorID    string
}

func setupP901PostgresFixture(
	t *testing.T,
	closeOpeningPeriod bool,
	amounts ...string,
) p901PostgresFixture {
	t.Helper()
	revenueAmount, expenseAmount := "150", "100"
	if len(amounts) == 2 {
		revenueAmount, expenseAmount = amounts[0], amounts[1]
	}
	ctx, env := setupIsolatedPostingEnv(t)
	expenseID, retainedID, summaryID := uuid.New(), uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{
			CompanyID: env.CompanyID, ID: expenseID, Code: "5000", Name: "Operating expense",
			AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit,
			SubClassification: masterdata.SubClassCostOfSales, IsActive: true,
		},
		{
			CompanyID: env.CompanyID, ID: retainedID, Code: "3300", Name: "Retained earnings",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			SubClassification: masterdata.SubClassRetainedEarnings, IsActive: true,
		},
		{
			CompanyID: env.CompanyID, ID: summaryID, Code: "3990", Name: "Income summary",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			SubClassification: "income_summary", IsActive: true,
		},
	} {
		if err := env.Driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}

	period, err := env.Driver.GetPeriodByDateForCompany(ctx, env.CompanyID,
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	year, err := env.Driver.GetFiscalYearForCompanyContext(ctx, env.CompanyID, period.FiscalYearID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Driver.GetPeriodByNumberForCompany(ctx, env.CompanyID, year.ID, 13); err != nil {
		t.Fatalf("load period 13 from PostgreSQL fixture: %v", err)
	}
	nextYear := masterdata.GenerateFiscalYear(year.StartDate.AddDate(1, 0, 0))
	nextYear.CompanyID = env.CompanyID
	if err := env.Driver.CreateFiscalYear(ctx, nextYear); err != nil {
		t.Fatal(err)
	}
	if closeOpeningPeriod {
		if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, nextYear.Periods[0].ID, masterdata.PeriodSoftClosed); err != nil {
			t.Fatal(err)
		}
		if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, nextYear.Periods[0].ID, masterdata.PeriodHardClosed); err != nil {
			t.Fatal(err)
		}
	}

	postingEngine := posting.NewEngine(env.Driver, nil)
	for _, fixture := range []struct {
		date     time.Time
		debitID  uuid.UUID
		creditID uuid.UUID
		amount   string
	}{
		{date: year.StartDate.AddDate(0, 0, 14), debitID: env.DebitAccID, creditID: env.CreditAccID, amount: revenueAmount},
		{date: year.StartDate.AddDate(0, 1, 14), debitID: expenseID, creditID: env.DebitAccID, amount: expenseAmount},
	} {
		amount, err := financialtypes.NewMoneyFromString(fixture.amount, "USD")
		if err != nil {
			t.Fatal(err)
		}
		entry := createDraftTestEntry(t, ctx, postingEngine, env.CompanyID, env.JournalID,
			fixture.debitID, fixture.creditID, amount, fixture.date)
		if err := postingEngine.PostEntryContext(ctx, &entry, "fixture-poster"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 12; i++ {
		period := year.Periods[i]
		if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, period.ID, masterdata.PeriodSoftClosed); err != nil {
			t.Fatal(err)
		}
		if err := env.Driver.UpdatePeriodState(ctx, env.CompanyID, period.ID, masterdata.PeriodHardClosed); err != nil {
			t.Fatal(err)
		}
	}

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(env.Driver),
		sdk.WithReporting(env.Driver),
	).Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close SDK engine: %v", err)
		}
	})
	return p901PostgresFixture{
		ctx: ctx, driver: env.Driver, engine: engine, companyID: env.CompanyID,
		journalID: env.JournalID, year: year, nextYear: nextYear,
		retainedID: retainedID, summaryID: summaryID, actorID: uuid.NewString(),
	}
}

func (f p901PostgresFixture) commandContext() publicapplication.Context {
	return publicapplication.Context{
		Principal: publicapplication.Principal{ActorID: f.actorID, CompanyIDs: []types.CompanyID{types.CompanyID(f.companyID)}},
	}
}

func TestP901PostgresAtomicYearEndCloseAndReopen(t *testing.T) {
	fixture := setupP901PostgresFixture(t, false)
	closer, err := sdk.YearEndClosing(fixture.engine)
	if err != nil {
		t.Fatal(err)
	}
	result, err := closer.ExecuteYearEndClosing(fixture.ctx, publicapplication.ExecuteYearEndClosingCommand{
		Context: fixture.commandContext(), CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID: types.FiscalYearID(fixture.year.ID), NextFiscalYearID: types.FiscalYearID(fixture.nextYear.ID),
		JournalID: types.JournalID(fixture.journalID), RetainedEarningsAccountID: types.AccountID(fixture.retainedID),
		Policy: publicapplication.YearEndClosingDirectToRetainedEarnings, Currency: "USD",
		Reason: "Approved annual close",
	})
	if err != nil {
		t.Fatalf("execute PostgreSQL annual close: %v", err)
	}
	if result.NetIncome != "50" || len(result.ClosingEntryIDs) != 1 || result.OpeningEntryID == (types.JournalEntryID{}) {
		t.Fatalf("close result = %+v, want net income 50, one close and one opening", result)
	}
	year, err := fixture.driver.GetFiscalYearForCompanyContext(fixture.ctx, fixture.companyID, fixture.year.ID)
	if err != nil {
		t.Fatal(err)
	}
	if year.State != masterdata.FiscalYearHardClosed || year.Periods[12].State != masterdata.PeriodHardClosed {
		t.Fatalf("persisted close state = %s/%s, want hard-closed", year.State, year.Periods[12].State)
	}

	reopened, err := closer.ReopenYearEndClosing(fixture.ctx, publicapplication.ReopenYearEndClosingCommand{
		Context: fixture.commandContext(), CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID: types.FiscalYearID(fixture.year.ID), ApprovalReference: uuid.NewString(),
		Reason: "Approved post-audit adjustment",
	})
	if err != nil {
		t.Fatalf("reopen PostgreSQL annual close: %v", err)
	}
	if len(reopened.ReversalEntryIDs) != 2 {
		t.Fatalf("reversal count = %d, want closing and opening reversals", len(reopened.ReversalEntryIDs))
	}
}

func TestP901PostgresYearEndCloseSupportsLoss(t *testing.T) {
	fixture := setupP901PostgresFixture(t, false, "100", "150")
	closer, err := sdk.YearEndClosing(fixture.engine)
	if err != nil {
		t.Fatal(err)
	}
	result, err := closer.ExecuteYearEndClosing(fixture.ctx, publicapplication.ExecuteYearEndClosingCommand{
		Context: fixture.commandContext(), CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID: types.FiscalYearID(fixture.year.ID), JournalID: types.JournalID(fixture.journalID),
		RetainedEarningsAccountID: types.AccountID(fixture.retainedID),
		Policy:                    publicapplication.YearEndClosingDirectToRetainedEarnings,
		Currency:                  "USD", Reason: "Approved annual loss close",
	})
	if err != nil {
		t.Fatalf("execute PostgreSQL annual loss close: %v", err)
	}
	if result.NetIncome != "-50" || len(result.ClosingEntryIDs) != 1 {
		t.Fatalf("loss close result = %+v, want net loss -50 and one closing entry", result)
	}
}

func TestP901PostgresYearEndCloseSupportsIncomeSummaryPolicy(t *testing.T) {
	fixture := setupP901PostgresFixture(t, false)
	closer, err := sdk.YearEndClosing(fixture.engine)
	if err != nil {
		t.Fatal(err)
	}
	result, err := closer.ExecuteYearEndClosing(fixture.ctx, publicapplication.ExecuteYearEndClosingCommand{
		Context: fixture.commandContext(), CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID: types.FiscalYearID(fixture.year.ID), JournalID: types.JournalID(fixture.journalID),
		RetainedEarningsAccountID: types.AccountID(fixture.retainedID),
		IncomeSummaryAccountID:    types.AccountID(fixture.summaryID),
		Policy:                    publicapplication.YearEndClosingThroughIncomeSummary,
		Currency:                  "USD", Reason: "Approved income-summary close",
	})
	if err != nil {
		t.Fatalf("execute PostgreSQL income-summary close: %v", err)
	}
	if result.NetIncome != "50" || len(result.ClosingEntryIDs) != 2 {
		t.Fatalf("income-summary close result = %+v, want net income 50 and two closing entries", result)
	}
}

func TestP901PostgresYearEndCloseRollsBackWhenOpeningPeriodIsClosed(t *testing.T) {
	fixture := setupP901PostgresFixture(t, true)
	closer, err := sdk.YearEndClosing(fixture.engine)
	if err != nil {
		t.Fatal(err)
	}
	_, err = closer.ExecuteYearEndClosing(fixture.ctx, publicapplication.ExecuteYearEndClosingCommand{
		Context: fixture.commandContext(), CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID: types.FiscalYearID(fixture.year.ID), NextFiscalYearID: types.FiscalYearID(fixture.nextYear.ID),
		JournalID: types.JournalID(fixture.journalID), RetainedEarningsAccountID: types.AccountID(fixture.retainedID),
		Policy: publicapplication.YearEndClosingDirectToRetainedEarnings, Currency: "USD",
		Reason: "Approved annual close",
	})
	if err == nil {
		t.Fatal("annual close succeeded with a hard-closed opening period")
	}
	year, getErr := fixture.driver.GetFiscalYearForCompanyContext(fixture.ctx, fixture.companyID, fixture.year.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if year.State != masterdata.FiscalYearOpen || year.Periods[12].State != masterdata.PeriodOpen {
		t.Fatalf("fiscal-year state after rollback = %s/%s, want open/open", year.State, year.Periods[12].State)
	}
	entries, err := fixture.driver.ListEntriesForCompanyContext(
		fixture.ctx, fixture.companyID, fixture.year.StartDate, fixture.year.EndDate,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Classification == posting.EntryClassClosing && entry.Status == posting.EntryPosted {
			t.Fatalf("closing entry %s persisted despite rollback", entry.ID)
		}
	}
}
