package reporting_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func TestGenerateClosingMovesSupportsProfitAndLossPolicies(t *testing.T) {
	for _, test := range []struct {
		name    string
		revenue string
		expense string
		net     string
		profit  bool
	}{
		{name: "profit", revenue: "150", expense: "100", net: "50", profit: true},
		{name: "loss", revenue: "100", expense: "150", net: "-50"},
	} {
		for _, policy := range []reporting.ClosingPolicy{
			reporting.ClosingPolicyDirectToRetainedEarnings,
			reporting.ClosingPolicyIncomeSummary,
		} {
			t.Run(test.name+"/"+string(policy), func(t *testing.T) {
				cashID, revenueID, expenseID, retainedID, summaryID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
				repository := &memoryRepository{
					accounts: []masterdata.Account{
						reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
						reportAccount(revenueID, "4000", "Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
						reportAccount(expenseID, "5000", "Expense", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales),
						reportAccount(retainedID, "3300", "Retained earnings", masterdata.AccountTypeEquity, masterdata.SubClassRetainedEarnings),
						reportAccount(summaryID, "3990", "Income summary", masterdata.AccountTypeEquity, "income_summary"),
					},
				}
				start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
				repository.entries = []posting.JournalEntry{
					reportEntry(t, start.AddDate(0, 1, 0),
						posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, test.revenue, "USD")},
						posting.JournalEntryLine{AccountID: revenueID, Credit: reportMoney(t, test.revenue, "USD")},
					),
					reportEntry(t, start.AddDate(0, 2, 0),
						posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, test.expense, "USD")},
						posting.JournalEntryLine{AccountID: cashID, Credit: reportMoney(t, test.expense, "USD")},
					),
				}
				year := masterdata.GenerateFiscalYear(start)
				year.EndDate = end
				for i := range year.Periods {
					if year.Periods[i].Number <= 12 {
						year.Periods[i].State = masterdata.PeriodHardClosed
					}
				}
				options := reporting.ClosingOptions{Policy: policy}
				if policy == reporting.ClosingPolicyIncomeSummary {
					options.IncomeSummaryAccountID = summaryID
				}
				entries, netIncome, err := reporting.NewClosingEngine(repository).GenerateClosingMovesContext(
					t.Context(), year, uuid.New(), retainedID, "USD", options,
				)
				if err != nil {
					t.Fatalf("GenerateClosingMovesContext: %v", err)
				}
				wantEntries := 1
				if policy == reporting.ClosingPolicyIncomeSummary {
					wantEntries = 2
				}
				if len(entries) != wantEntries {
					t.Fatalf("generated %d entries, want %d", len(entries), wantEntries)
				}
				if !netIncome.Equal(decimal.RequireFromString(test.net)) {
					t.Fatalf("net income = %s, want %s", netIncome, test.net)
				}
				for i, entry := range entries {
					if err := entry.Validate(); err != nil {
						t.Fatalf("entry %d is unbalanced: %v", i, err)
					}
					if entry.Classification != posting.EntryClassClosing || entry.TargetPeriodNumber != 13 ||
						!entry.AdjustmentAuthorized {
						t.Fatalf("entry %d is not an authorized period-13 closing: %+v", i, entry)
					}
				}
				offsetAccount := retainedID
				if policy == reporting.ClosingPolicyIncomeSummary {
					offsetAccount = summaryID
				}
				if !closingLineHasExpectedBalance(entries[0], offsetAccount, !test.profit, decimal.NewFromInt(50)) {
					t.Fatalf("first close did not offset net result to the expected account: %+v", entries[0].Lines)
				}
				if policy == reporting.ClosingPolicyIncomeSummary &&
					(!closingLineHasExpectedBalance(entries[1], summaryID, test.profit, decimal.NewFromInt(50)) ||
						!closingLineHasExpectedBalance(entries[1], retainedID, !test.profit, decimal.NewFromInt(50))) {
					t.Fatalf("income-summary transfer has incorrect directions: %+v", entries[1].Lines)
				}
			})
		}
	}
}

func closingLineHasExpectedBalance(
	entry posting.JournalEntry,
	accountID uuid.UUID,
	debit bool,
	amount decimal.Decimal,
) bool {
	debitTotal, creditTotal := decimal.Zero, decimal.Zero
	found := false
	for _, line := range entry.Lines {
		if line.AccountID != accountID {
			continue
		}
		found = true
		debitTotal = debitTotal.Add(line.Debit.Amount())
		creditTotal = creditTotal.Add(line.Credit.Amount())
	}
	if !found {
		return false
	}
	net := creditTotal.Sub(debitTotal)
	if debit {
		net = debitTotal.Sub(creditTotal)
	}
	return net.Equal(amount)
}

func TestGenerateClosingMovePreservesSourceCurrenciesAndDimensions(t *testing.T) {
	cashID, revenueID, expenseID, retainedID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	costCenterID, segmentID := uuid.New(), uuid.New()
	repository := &memoryRepository{
		accounts: []masterdata.Account{
			reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
			reportAccount(revenueID, "4000", "Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
			reportAccount(expenseID, "5000", "Expense", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales),
			reportAccount(retainedID, "3300", "Retained earnings", masterdata.AccountTypeEquity, masterdata.SubClassRetainedEarnings),
		},
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 1, 0),
			multicurrencyLine(t, cashID, "100", "EUR", "120", "USD", "1.2", true, costCenterID, segmentID),
			multicurrencyLine(t, revenueID, "100", "EUR", "120", "USD", "1.2", false, costCenterID, segmentID),
		),
		reportEntry(t, start.AddDate(0, 2, 0),
			multicurrencyLine(t, expenseID, "50", "GBP", "70", "USD", "1.4", true, costCenterID, segmentID),
			multicurrencyLine(t, cashID, "50", "GBP", "70", "USD", "1.4", false, costCenterID, segmentID),
		),
	}
	year := masterdata.GenerateFiscalYear(start)
	year.EndDate = end
	for i := range year.Periods {
		if year.Periods[i].Number <= 12 {
			year.Periods[i].State = masterdata.PeriodHardClosed
		}
	}
	entry, _, err := reporting.NewClosingEngine(repository).GenerateClosingMoveContext(
		t.Context(), year, uuid.New(), retainedID, "USD",
	)
	if err != nil {
		t.Fatalf("GenerateClosingMoveContext: %v", err)
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("generated multi-currency close is unbalanced: %v", err)
	}
	found := map[string]bool{}
	for _, line := range entry.Lines {
		if line.AccountID != revenueID && line.AccountID != expenseID && line.AccountID != retainedID {
			continue
		}
		found[line.TransactionCurrency] = true
		if line.CostCenterID == nil || *line.CostCenterID != costCenterID ||
			line.SegmentID == nil || *line.SegmentID != segmentID {
			t.Fatalf("closing line lost dimensions: %+v", line)
		}
	}
	if !found["EUR"] || !found["GBP"] {
		t.Fatalf("closing lines did not preserve source currencies on both nominal and retained-earnings lines: %v", found)
	}
	if _, _, err := reporting.NewClosingEngine(repository).GenerateClosingMoveContext(
		t.Context(), year, uuid.New(), retainedID, "EUR",
	); err != reporting.ErrClosingFunctionalCurrencyMismatch {
		t.Fatalf("close in a non-functional currency error = %v", err)
	}
}

func TestGenerateYearEndOpeningMovePreservesSourceCurrencyAndDimensions(t *testing.T) {
	companyID := uuid.New()
	cashID, capitalID, revenueID, retainedID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	costCenterID, segmentID := uuid.New(), uuid.New()
	repository := &memoryRepository{
		accounts: []masterdata.Account{
			reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
			reportAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
			reportAccount(revenueID, "4000", "Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
			reportAccount(retainedID, "3300", "Retained earnings", masterdata.AccountTypeEquity, masterdata.SubClassRetainedEarnings),
		},
	}
	for i := range repository.accounts {
		repository.accounts[i].CompanyID = companyID
	}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	closedYear := masterdata.GenerateFiscalYear(start)
	closedYear.CompanyID = companyID
	nextYear := masterdata.GenerateFiscalYear(closedYear.EndDate.Add(time.Nanosecond))
	nextYear.CompanyID = companyID
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 1, 0),
			multicurrencyLine(t, cashID, "100", "EUR", "120", "USD", "1.2", true, costCenterID, segmentID),
			multicurrencyLine(t, capitalID, "120", "USD", "120", "USD", "1", false, costCenterID, segmentID),
		),
		reportEntry(t, start.AddDate(0, 2, 0),
			multicurrencyLine(t, cashID, "50", "USD", "50", "USD", "1", true, costCenterID, segmentID),
			multicurrencyLine(t, revenueID, "50", "USD", "50", "USD", "1", false, costCenterID, segmentID),
		),
	}
	for i := range repository.entries {
		repository.entries[i].CompanyID = companyID
		for lineIndex := range repository.entries[i].Lines {
			repository.entries[i].Lines[lineIndex].CompanyID = companyID
		}
	}

	entry, err := reporting.NewClosingEngine(repository).GenerateYearEndOpeningMoveContext(
		t.Context(), closedYear, nextYear, uuid.New(), "USD",
	)
	if err != nil {
		t.Fatalf("GenerateYearEndOpeningMoveContext: %v", err)
	}
	if entry.Classification != posting.EntryClassOpening || entry.TargetPeriodNumber != 1 {
		t.Fatalf("generated entry is not a period-1 opening: %+v", entry)
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("generated opening entry is unbalanced: %v", err)
	}
	foundEURCash, foundUSDCash, foundRetained := false, false, false
	for _, line := range entry.Lines {
		if line.CostCenterID == nil || *line.CostCenterID != costCenterID ||
			line.SegmentID == nil || *line.SegmentID != segmentID {
			t.Fatalf("opening line lost analytical dimensions: %+v", line)
		}
		if line.AccountID == cashID && line.TransactionCurrency == "EUR" {
			foundEURCash = line.TransactionAmount.Amount().Equal(decimal.NewFromInt(100)) &&
				line.ExchangeRate.Equal(decimal.RequireFromString("1.2"))
		}
		if line.AccountID == cashID && line.TransactionCurrency == "USD" {
			foundUSDCash = line.TransactionAmount.Amount().Equal(decimal.NewFromInt(50))
		}
		if line.AccountID == retainedID {
			foundRetained = line.Credit.Amount().Equal(decimal.NewFromInt(50))
		}
	}
	if !foundEURCash || !foundUSDCash || !foundRetained {
		t.Fatalf("opening line details were not preserved: EUR cash=%t USD cash=%t retained earnings=%t",
			foundEURCash, foundUSDCash, foundRetained)
	}
	repository.entries[0].Lines[0].FunctionalDebit = reportMoney(t, "121", "USD")
	repository.entries[0].Lines[1].FunctionalCredit = reportMoney(t, "121", "USD")
	if _, err := reporting.NewClosingEngine(repository).GenerateYearEndOpeningMoveContext(
		t.Context(), closedYear, nextYear, uuid.New(), "USD",
	); err != reporting.ErrOpeningCurrencyAggregationUnsupported {
		t.Fatalf("unsupported source-currency aggregation error = %v", err)
	}
}

func multicurrencyLine(
	t *testing.T,
	accountID uuid.UUID,
	transactionAmount, transactionCurrency, functionalAmount, functionalCurrency, rate string,
	debit bool,
	costCenterID, segmentID uuid.UUID,
) posting.JournalEntryLine {
	t.Helper()
	tx, err := financialtypes.NewMoneyFromString(transactionAmount, transactionCurrency)
	if err != nil {
		t.Fatal(err)
	}
	functional, err := financialtypes.NewMoneyFromString(functionalAmount, functionalCurrency)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero(functionalCurrency)
	if err != nil {
		t.Fatal(err)
	}
	line := posting.JournalEntryLine{
		ID: uuid.New(), AccountID: accountID, TransactionAmount: tx, TransactionCurrency: transactionCurrency,
		ExchangeRate: decimal.RequireFromString(rate), CostCenterID: &costCenterID, SegmentID: &segmentID,
		FunctionalDebit: zero, FunctionalCredit: zero,
	}
	if debit {
		line.FunctionalDebit = functional
	} else {
		line.FunctionalCredit = functional
	}
	return line
}
