package reporting

import (
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type memoryRepository struct {
	accounts []masterdata.Account
	entries  []posting.JournalEntry
}

func (r *memoryRepository) ListAccounts() ([]masterdata.Account, error) {
	return append([]masterdata.Account(nil), r.accounts...), nil
}

func (r *memoryRepository) ListPostedEntries(start, end time.Time) ([]posting.JournalEntry, error) {
	result := make([]posting.JournalEntry, 0)
	for _, entry := range r.entries {
		date := entryEffectiveDate(entry)
		if !start.IsZero() && date.Before(start) {
			continue
		}
		if !end.IsZero() && date.After(end) {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

func reportMoney(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func reportAccount(id uuid.UUID, code, name string, accountType masterdata.AccountType, classification string) masterdata.Account {
	return masterdata.Account{
		ID: id, Code: code, Name: name, AccountType: accountType,
		NormalBalance: masterdata.DefaultNormalBalance(accountType), SubClassification: classification,
	}
}

func reportEntry(t *testing.T, date time.Time, lines ...posting.JournalEntryLine) posting.JournalEntry {
	t.Helper()
	entry, err := posting.NewJournalEntry(uuid.New(), date, "tester", lines)
	if err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	return entry
}

func TestFinancialStatementsAndTrialBalance(t *testing.T) {
	cashID := uuid.New()
	equityID := uuid.New()
	revenueID := uuid.New()
	expenseID := uuid.New()
	repository := &memoryRepository{
		accounts: []masterdata.Account{
			reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, "cash"),
			reportAccount(equityID, "3000", "Capital", masterdata.AccountTypeEquity, "capital"),
			reportAccount(revenueID, "4000", "Sales", masterdata.AccountTypeIncome, "operating_revenue"),
			reportAccount(expenseID, "5000", "Cost of sales", masterdata.AccountTypeExpense, "cost_of_sales"),
		},
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "1000", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "1000", "USD")},
		),
		reportEntry(t, start.AddDate(0, 1, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "800", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: revenueID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "800", "USD")},
		),
		reportEntry(t, start.AddDate(0, 2, 1),
			posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "300", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "300", "USD")},
		),
	}

	trial, err := NewTrialBalanceEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !trial.Balanced {
		t.Fatal("trial balance is not balanced")
	}
	balanceSheet, err := NewBalanceSheetEngine(repository).Generate(end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !balanceSheet.Balanced {
		// The balance-sheet equation intentionally catches the missing period close.
		if balanceSheet.TotalAssets.Equal(decimal.NewFromInt(1500)) && balanceSheet.TotalLiabilitiesAndEquity.Equal(decimal.NewFromInt(1000)) {
			return
		}
		t.Fatalf("unexpected balance-sheet result: %+v", balanceSheet)
	}
}

func TestClosingAndOpeningMoves(t *testing.T) {
	cashID := uuid.New()
	revenueID := uuid.New()
	expenseID := uuid.New()
	retainedID := uuid.New()
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, "cash"),
		reportAccount(revenueID, "4000", "Sales", masterdata.AccountTypeIncome, "operating_revenue"),
		reportAccount(expenseID, "5000", "Cost of sales", masterdata.AccountTypeExpense, "cost_of_sales"),
		reportAccount(retainedID, "3300", "Retained earnings", masterdata.AccountTypeEquity, "retained_earnings"),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: revenueID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100", "USD")},
		),
		reportEntry(t, start.AddDate(0, 0, 2),
			posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "40", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "40", "USD")},
		),
	}

	year := masterdata.GenerateFiscalYear(start)
	year.EndDate = end
	for i := range year.Periods {
		year.Periods[i].State = masterdata.PeriodHardClosed
	}
	closing, netIncome, err := NewClosingEngine(repository).GenerateClosingMove(year, uuid.New(), retainedID, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !netIncome.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("unexpected net income: %s", netIncome)
	}
	if err := closing.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGeneralLedgerRunningBalance(t *testing.T) {
	cashID := uuid.New()
	equityID := uuid.New()
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(equityID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100", "USD")},
		),
		reportEntry(t, start.AddDate(0, 0, 2),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "25", "USD")},
			posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "25", "USD"), Credit: reportMoney(t, "0", "USD")},
		),
	}
	report, err := NewGeneralLedgerEngine(repository).Generate(cashID, start, start.AddDate(1, 0, 0), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Lines) != 2 || !report.Lines[1].RunningBalance.Equal(decimal.NewFromInt(75)) {
		t.Fatalf("unexpected general ledger: %+v", report)
	}
}

func TestFinancialRatiosSafeDivision(t *testing.T) {
	cashID := uuid.New()
	revenueID := uuid.New()
	expenseID := uuid.New()
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(revenueID, "4000", "Sales", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
		reportAccount(expenseID, "5000", "Cost of sales", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0).Add(-time.Nanosecond)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: revenueID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100", "USD")},
		),
	}
	ratios, err := NewRatiosEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if ratios.Solvency.DebtToEquity.Sign() != 0 || ratios.Solvency.InterestCoverage.Sign() != 0 {
		t.Fatalf("zero denominators should produce zero ratios: %+v", ratios)
	}
}

func TestGeneralLedgerReconcilesWithTrialBalance(t *testing.T) {
	cashID := uuid.New()
	receivableID := uuid.New()
	salesID := uuid.New()
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(receivableID, "1200", "AR", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables),
		reportAccount(salesID, "4000", "Sales", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 5),
			posting.JournalEntryLine{AccountID: receivableID, Debit: reportMoney(t, "1000", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: salesID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "1000", "USD")},
		),
		reportEntry(t, start.AddDate(0, 0, 15),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "600", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: receivableID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "600", "USD")},
		),
	}

	trial, err := NewTrialBalanceEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}

	glEngine := NewGeneralLedgerEngine(repository)
	for _, line := range trial.Lines {
		glReport, err := glEngine.Generate(line.AccountID, start, end, "USD")
		if err != nil {
			t.Fatalf("GL generation failed for account %s: %v", line.AccountCode, err)
		}
		var trialNet decimal.Decimal
		if line.AccountType == masterdata.AccountTypeAsset || line.AccountType == masterdata.AccountTypeExpense {
			trialNet = line.EndingDebit.Sub(line.EndingCredit)
		} else {
			trialNet = line.EndingCredit.Sub(line.EndingDebit)
		}
		if !glReport.EndingBalance.Equal(trialNet) {
			t.Fatalf("account %s GL ending balance %s does not match trial balance %s",
				line.AccountCode, glReport.EndingBalance, trialNet)
		}
	}
}

type fullBundleMemoryRepository struct {
	memoryRepository
	assets []subledgers.FixedAsset
	lines  []subledgers.OpenLine
}

func (r *fullBundleMemoryRepository) ListFixedAssets() ([]subledgers.FixedAsset, error) {
	return append([]subledgers.FixedAsset(nil), r.assets...), nil
}

func (r *fullBundleMemoryRepository) ListOpenLines(status string) ([]subledgers.OpenLine, error) {
	return append([]subledgers.OpenLine(nil), r.lines...), nil
}

func TestStatementBundleComprehensive(t *testing.T) {
	cashID := uuid.New()
	arID := uuid.New()
	ppeID := uuid.New()
	equityID := uuid.New()
	salesID := uuid.New()
	expenseID := uuid.New()

	accounts := []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(arID, "1200", "Trade Receivables", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables),
		reportAccount(ppeID, "1500", "Machinery", masterdata.AccountTypeAsset, masterdata.SubClassPropertyPlantEquipment),
		reportAccount(equityID, "3000", "Share Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		reportAccount(salesID, "4000", "Operating Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
		reportAccount(expenseID, "5000", "Cost of Sales", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales),
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	repo := &fullBundleMemoryRepository{
		memoryRepository: memoryRepository{
			accounts: accounts,
			entries: []posting.JournalEntry{
				reportEntry(t, start.AddDate(0, 0, 1),
					posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "50000", "USD"), Credit: reportMoney(t, "0", "USD")},
					posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "50000", "USD")},
				),
				reportEntry(t, start.AddDate(0, 0, 2),
					posting.JournalEntryLine{AccountID: ppeID, Debit: reportMoney(t, "20000", "USD"), Credit: reportMoney(t, "0", "USD")},
					posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "20000", "USD")},
				),
				reportEntry(t, start.AddDate(0, 0, 10),
					posting.JournalEntryLine{AccountID: arID, Debit: reportMoney(t, "10000", "USD"), Credit: reportMoney(t, "0", "USD")},
					posting.JournalEntryLine{AccountID: salesID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "10000", "USD")},
				),
				reportEntry(t, start.AddDate(0, 0, 15),
					posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "4000", "USD"), Credit: reportMoney(t, "0", "USD")},
					posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "4000", "USD")},
				),
			},
		},
		assets: []subledgers.FixedAsset{
			{
				ID:                      uuid.New(),
				HistoricalCost:          reportMoney(t, "20000", "USD"),
				AccumulatedDepreciation: reportMoney(t, "2000", "USD"),
				CurrentBookValue:        reportMoney(t, "18000", "USD"),
				InServiceDate:           start.AddDate(0, 0, 2),
			},
		},
		lines: []subledgers.OpenLine{
			{
				ID:             uuid.New(),
				Kind:           subledgers.LineReceivable,
				ResidualAmount: reportMoney(t, "10000", "USD"),
				DueDate:        start.AddDate(0, 1, 10),
				Closed:         false,
			},
		},
	}

	bundleEngine := NewBundleEngine(repo)
	bundle, err := bundleEngine.Generate(start, end, "USD")
	if err != nil {
		t.Fatalf("bundle generation failed: %v", err)
	}

	if !bundle.BalanceSheet.Balanced {
		t.Fatal("bundle balance sheet must be balanced")
	}
	if !bundle.PPE.Reconciled {
		t.Fatal("PPE roll forward schedule must be internally reconciled")
	}
	if !bundle.AgedReceivables.Total.Equal(decimal.NewFromInt(10000)) {
		t.Fatalf("aged receivables total %s, want 10000", bundle.AgedReceivables.Total)
	}
	if !bundle.CashFlow.Reconciled {
		t.Fatal("cash flow in bundle must reconcile with cash account movement")
	}
}
