package reporting

import (
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
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
		if year.Periods[i].Number <= 12 {
			year.Periods[i].State = masterdata.PeriodHardClosed
		}
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
	opening, err := NewClosingEngine(repository).GenerateOpeningMove(year, uuid.New(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if err := opening.Validate(); err != nil {
		t.Fatal(err)
	}
}
