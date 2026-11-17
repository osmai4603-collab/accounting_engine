package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	internalreporting "github.com/osmai4603-collab/accounting_engine/internal/reporting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/shopspring/decimal"
)

type p9WorkflowFixture struct {
	driver            *memory.Driver
	companyID         uuid.UUID
	journalID         uuid.UUID
	year              masterdata.FiscalYear
	nextYear          masterdata.FiscalYear
	cashAccountID     uuid.UUID
	revenueAccountID  uuid.UUID
	expenseAccountID  uuid.UUID
	retainedAccountID uuid.UUID
	summaryAccountID  uuid.UUID
	cashLineIDs       []uuid.UUID
}

func newP9WorkflowFixture(t *testing.T, closeNextYearPeriodOne bool) p9WorkflowFixture {
	t.Helper()
	ctx := context.Background()
	driver := memory.New()
	companyID, journalID := uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "P9-TEST", LegalName: "P9 Test Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General",
	}); err != nil {
		t.Fatal(err)
	}

	cashID, revenueID, expenseID, retainedID, summaryID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	accounts := []masterdata.Account{
		{
			CompanyID: companyID, ID: cashID, Code: "1000", Name: "Cash",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
			IsActive: true, IsCashEquivalent: true, CashFlowActivity: masterdata.CashFlowCashEquivalent,
			IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, SubClassification: masterdata.SubClassOperatingRevenue,
			CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryOperating,
		},
		{
			CompanyID: companyID, ID: expenseID, Code: "5000", Name: "Expense",
			AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit,
			IsActive: true, SubClassification: masterdata.SubClassCostOfSales,
			CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryOperating,
		},
		{
			CompanyID: companyID, ID: retainedID, Code: "3300", Name: "Retained earnings",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, SubClassification: masterdata.SubClassRetainedEarnings,
			CashFlowActivity: masterdata.CashFlowFinancing, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: summaryID, Code: "3990", Name: "Income summary",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, SubClassification: "income_summary",
			CashFlowActivity: masterdata.CashFlowFinancing, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
	}
	for _, account := range accounts {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	year := masterdata.GenerateFiscalYear(start)
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	nextYear := masterdata.GenerateFiscalYear(start.AddDate(1, 0, 0))
	nextYear.CompanyID = companyID
	if closeNextYearPeriodOne {
		nextYear.Periods[0].State = masterdata.PeriodHardClosed
	}
	if err := driver.CreateFiscalYear(ctx, nextYear); err != nil {
		t.Fatal(err)
	}

	cash150 := p9Money(t, "150")
	cash100 := p9Money(t, "100")
	zero := p9Money(t, "0")
	receipt := p9WorkflowEntry(t, companyID, journalID, year.Periods[0].ID,
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		posting.JournalEntryLine{CompanyID: companyID, AccountID: cashID, Debit: cash150, Credit: zero},
		posting.JournalEntryLine{CompanyID: companyID, AccountID: revenueID, Debit: zero, Credit: cash150},
	)
	expense := p9WorkflowEntry(t, companyID, journalID, year.Periods[1].ID,
		time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC),
		posting.JournalEntryLine{CompanyID: companyID, AccountID: expenseID, Debit: cash100, Credit: zero},
		posting.JournalEntryLine{CompanyID: companyID, AccountID: cashID, Debit: zero, Credit: cash100},
	)
	postP9FixtureEntry(t, driver, receipt)
	postP9FixtureEntry(t, driver, expense)
	for i := 0; i < 12; i++ {
		if err := driver.UpdatePeriodState(ctx, companyID, year.Periods[i].ID, masterdata.PeriodSoftClosed); err != nil {
			t.Fatal(err)
		}
		if err := driver.UpdatePeriodState(ctx, companyID, year.Periods[i].ID, masterdata.PeriodHardClosed); err != nil {
			t.Fatal(err)
		}
	}
	return p9WorkflowFixture{
		driver: driver, companyID: companyID, journalID: journalID, year: year, nextYear: nextYear,
		cashAccountID: cashID, revenueAccountID: revenueID, expenseAccountID: expenseID,
		retainedAccountID: retainedID, summaryAccountID: summaryID,
		cashLineIDs: []uuid.UUID{receipt.Lines[0].ID, expense.Lines[1].ID},
	}
}

func p9Money(t *testing.T, value string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(value, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func p9WorkflowEntry(
	t *testing.T,
	companyID, journalID, periodID uuid.UUID,
	date time.Time,
	lines ...posting.JournalEntryLine,
) posting.JournalEntry {
	t.Helper()
	for i := range lines {
		if lines[i].ID == uuid.Nil {
			lines[i].ID = uuid.New()
		}
	}
	entry, err := posting.NewJournalEntry(journalID, date, "fixture", lines)
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.PeriodID = periodID
	for i := range entry.Lines {
		entry.Lines[i].CompanyID = companyID
	}
	return entry
}

func postP9FixtureEntry(t *testing.T, driver *memory.Driver, entry posting.JournalEntry) {
	t.Helper()
	ctx := context.Background()
	entry.Status = posting.EntryDraft
	if err := driver.SaveDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(entry); err != nil {
		t.Fatal(err)
	}
}

func buildP9WorkflowEngine(t *testing.T, driver *memory.Driver) Engine {
	t.Helper()
	engine, err := NewBuilder(WithCoreStorage(driver), WithReporting(driver)).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})
	return engine
}

func p9Principal(companyID uuid.UUID) publicapplication.Context {
	return publicapplication.Context{
		Principal: publicapplication.Principal{
			ActorID: uuid.NewString(), CompanyIDs: []types.CompanyID{types.CompanyID(companyID)},
		},
	}
}

func TestYearEndClosingAndReopenAreAtomicWithMemoryAdapter(t *testing.T) {
	fixture := newP9WorkflowFixture(t, false)
	engine := buildP9WorkflowEngine(t, fixture.driver)
	closer, err := YearEndClosing(engine)
	if err != nil {
		t.Fatal(err)
	}
	commandContext := p9Principal(fixture.companyID)
	result, err := closer.ExecuteYearEndClosing(context.Background(), publicapplication.ExecuteYearEndClosingCommand{
		Context: commandContext, CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID:              types.FiscalYearID(fixture.year.ID),
		NextFiscalYearID:          types.FiscalYearID(fixture.nextYear.ID),
		JournalID:                 types.JournalID(fixture.journalID),
		RetainedEarningsAccountID: types.AccountID(fixture.retainedAccountID),
		Policy:                    publicapplication.YearEndClosingDirectToRetainedEarnings,
		Currency:                  "USD", Reason: "Approved annual close",
	})
	if err != nil {
		t.Fatalf("ExecuteYearEndClosing: %v", err)
	}
	if len(result.ClosingEntryIDs) != 1 || result.OpeningEntryID == (types.JournalEntryID{}) ||
		result.NetIncome != "50" {
		t.Fatalf("closing result = %+v, want one close, one opening, net income 50", result)
	}
	closedYear, err := fixture.driver.GetFiscalYearForCompanyContext(
		context.Background(), fixture.companyID, fixture.year.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if closedYear.State != masterdata.FiscalYearHardClosed ||
		closedYear.Periods[12].State != masterdata.PeriodHardClosed ||
		closedYear.Periods[13].State != masterdata.PeriodHardClosed {
		t.Fatalf("year close state = %s/%s/%s, want hard-closed", closedYear.State,
			closedYear.Periods[12].State, closedYear.Periods[13].State)
	}

	reopened, err := closer.ReopenYearEndClosing(context.Background(), publicapplication.ReopenYearEndClosingCommand{
		Context: commandContext, CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID:      types.FiscalYearID(fixture.year.ID),
		ApprovalReference: uuid.NewString(), Reason: "Approved post-audit adjustment",
	})
	if err != nil {
		t.Fatalf("ReopenYearEndClosing: %v", err)
	}
	if len(reopened.ReversalEntryIDs) != 2 {
		t.Fatalf("reversal count = %d, want opening and closing reversals", len(reopened.ReversalEntryIDs))
	}
	openedYear, err := fixture.driver.GetFiscalYearForCompanyContext(
		context.Background(), fixture.companyID, fixture.year.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if openedYear.State != masterdata.FiscalYearOpen ||
		openedYear.Periods[12].State != masterdata.PeriodOpen ||
		openedYear.Periods[13].State != masterdata.PeriodOpen {
		t.Fatalf("year reopen state = %s/%s/%s, want open", openedYear.State,
			openedYear.Periods[12].State, openedYear.Periods[13].State)
	}
	for _, entryID := range []types.JournalEntryID{result.ClosingEntryIDs[0], result.OpeningEntryID} {
		entry, err := fixture.driver.GetEntryByIDForCompanyContext(
			context.Background(), fixture.companyID, uuid.UUID(entryID),
		)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Status != posting.EntryReversed {
			t.Fatalf("original entry %s status = %s, want reversed", entryID, entry.Status)
		}
	}
}

func TestYearEndClosingSupportsIncomeSummaryPolicy(t *testing.T) {
	fixture := newP9WorkflowFixture(t, false)
	engine := buildP9WorkflowEngine(t, fixture.driver)
	closer, err := YearEndClosing(engine)
	if err != nil {
		t.Fatal(err)
	}
	result, err := closer.ExecuteYearEndClosing(context.Background(), publicapplication.ExecuteYearEndClosingCommand{
		Context: p9Principal(fixture.companyID), CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID: types.FiscalYearID(fixture.year.ID), JournalID: types.JournalID(fixture.journalID),
		RetainedEarningsAccountID: types.AccountID(fixture.retainedAccountID),
		IncomeSummaryAccountID:    types.AccountID(fixture.summaryAccountID),
		Policy:                    publicapplication.YearEndClosingThroughIncomeSummary,
		Currency:                  "USD", Reason: "Approved annual close",
	})
	if err != nil {
		t.Fatalf("ExecuteYearEndClosing: %v", err)
	}
	if len(result.ClosingEntryIDs) != 2 || result.OpeningEntryID != (types.JournalEntryID{}) ||
		result.NetIncome != "50" {
		t.Fatalf("closing result = %+v, want two close entries and net income 50", result)
	}
}

func TestYearEndClosingRollsBackWhenOpeningPostCannotProceed(t *testing.T) {
	fixture := newP9WorkflowFixture(t, true)
	engine := buildP9WorkflowEngine(t, fixture.driver)
	closer, err := YearEndClosing(engine)
	if err != nil {
		t.Fatal(err)
	}
	_, err = closer.ExecuteYearEndClosing(context.Background(), publicapplication.ExecuteYearEndClosingCommand{
		Context: p9Principal(fixture.companyID), CompanyID: types.CompanyID(fixture.companyID),
		FiscalYearID:              types.FiscalYearID(fixture.year.ID),
		NextFiscalYearID:          types.FiscalYearID(fixture.nextYear.ID),
		JournalID:                 types.JournalID(fixture.journalID),
		RetainedEarningsAccountID: types.AccountID(fixture.retainedAccountID),
		Currency:                  "USD", Reason: "Approved annual close",
	})
	if err == nil {
		t.Fatal("ExecuteYearEndClosing succeeded with a closed next-year opening period")
	}
	year, getErr := fixture.driver.GetFiscalYearForCompanyContext(
		context.Background(), fixture.companyID, fixture.year.ID,
	)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if year.State != masterdata.FiscalYearOpen || year.Periods[12].State != masterdata.PeriodOpen {
		t.Fatalf("year state after rollback = %s/%s, want open/open", year.State, year.Periods[12].State)
	}
	entries, err := fixture.driver.ListEntriesForCompanyContext(
		context.Background(), fixture.companyID, fixture.year.StartDate, fixture.year.EndDate,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Classification == posting.EntryClassClosing && entry.Status == posting.EntryPosted {
			t.Fatalf("closing entry %s persisted despite transaction rollback", entry.ID)
		}
	}
}

func TestReportingServiceExposesDirectAndIndirectCashFlow(t *testing.T) {
	fixture := newP9WorkflowFixture(t, false)
	engine := buildP9WorkflowEngine(t, fixture.driver)
	amount, err := types.NewMoneyFromString("25", "USD")
	if err != nil {
		t.Fatal(err)
	}
	request := reporting.CashFlowRequest{
		CompanyID: types.CompanyID(fixture.companyID), StartDate: fixture.year.StartDate,
		EndDate: fixture.year.EndDate, Currency: "USD", Method: reporting.CashFlowMethodDirect,
		Profile:           reporting.CashFlowProfileIAS7,
		CashEquivalentIDs: []types.AccountID{types.AccountID(fixture.cashAccountID)},
		Transactions: []reporting.CashFlowTransactionClassification{
			{CashLineID: types.JournalEntryLineID(fixture.cashLineIDs[0]), Activity: reporting.CashFlowActivityOperating},
			{CashLineID: types.JournalEntryLineID(fixture.cashLineIDs[1]), Activity: reporting.CashFlowActivityOperating},
		},
		NonCashTransactions: []reporting.NonCashTransaction{
			{Code: "LEASE", Description: "Non-cash lease addition", Amount: amount},
		},
	}
	direct, err := engine.Reporting().GetCashFlow(context.Background(), request)
	if err != nil {
		t.Fatalf("GetCashFlow direct: %v", err)
	}
	if !direct.Reconciled || direct.Method != reporting.CashFlowMethodDirect ||
		!direct.NetChangeInCash.Amount().Equal(decimal.NewFromInt(50)) ||
		len(direct.NonCashTransactions) != 1 {
		t.Fatalf("direct cash flow = %+v, want reconciled net change 50 and one non-cash item", direct)
	}

	request.Method = reporting.CashFlowMethodIndirect
	request.Profile = reporting.CashFlowProfileIFRS18
	request.Transactions = nil
	indirect, err := engine.Reporting().GetCashFlow(context.Background(), request)
	if err != nil {
		t.Fatalf("GetCashFlow indirect IFRS 18: %v", err)
	}
	if !indirect.Reconciled || indirect.Method != reporting.CashFlowMethodIndirect ||
		indirect.Profile != reporting.CashFlowProfileIFRS18 ||
		!indirect.NetChangeInCash.Amount().Equal(decimal.NewFromInt(50)) {
		t.Fatalf("indirect cash flow = %+v, want reconciled IFRS 18 statement", indirect)
	}

	request.Method = reporting.CashFlowMethodDirect
	request.Transactions = nil
	if _, err := engine.Reporting().GetCashFlow(context.Background(), request); !errors.Is(
		err, internalreporting.ErrCashFlowTransactionClassificationRequired,
	) {
		t.Fatalf("unclassified direct cash flow error = %v, want explicit classification failure", err)
	}
}
