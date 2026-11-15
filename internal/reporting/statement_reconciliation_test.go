package reporting_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/shopspring/decimal"
)

func TestStatementReconciler_EndToEndReconciliation(t *testing.T) {
	cashID := uuid.New()
	arID := uuid.New()
	equityID := uuid.New()
	revenueID := uuid.New()
	expenseID := uuid.New()

	accounts := []masterdata.Account{
		reportAccount(cashID, "1010", "Cash & Cash Equivalents", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(arID, "1020", "Trade Accounts Receivable", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables),
		reportAccount(equityID, "3010", "Share Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		reportAccount(revenueID, "4010", "Service Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
		reportAccount(expenseID, "5010", "Operating Expenses", masterdata.AccountTypeExpense, masterdata.SubClassGeneralAdminExpense),
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	repo := &memoryRepository{
		accounts: accounts,
		entries: []posting.JournalEntry{
			// Capital injection: Dr Cash 100,000, Cr Equity 100,000
			reportEntry(t, start.AddDate(0, 0, 1),
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100000", "USD")},
			),
			// Revenue earned on account: Dr AR 40,000, Cr Revenue 40,000
			reportEntry(t, start.AddDate(0, 0, 10),
				posting.JournalEntryLine{AccountID: arID, Debit: reportMoney(t, "40000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: revenueID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "40000", "USD")},
			),
			// Operating expenses paid in cash: Dr Expense 15,000, Cr Cash 15,000
			reportEntry(t, start.AddDate(0, 0, 15),
				posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "15000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "15000", "USD")},
			),
		},
	}

	reconciler := reporting.NewStatementReconciler(repo)
	report, err := reconciler.Reconcile(context.Background(), uuid.Nil, start, end, "USD")
	if err != nil {
		t.Fatalf("unexpected error during reconciliation: %v", err)
	}

	if !report.TrialBalanceBalanced {
		t.Errorf("expected TB to be balanced, got debit: %s, credit: %s", report.TBDebitTotal, report.TBCreditTotal)
	}
	if !report.BSBalanced {
		t.Errorf("expected Balance Sheet to be balanced, assets: %s, total liab+equity: %s", report.BSAssets, report.BSTotalLiabAndEquity)
	}
	if !report.NetIncomeReconciled {
		t.Errorf("expected Net Income to reconcile, TB net income: %s, IS net income: %s, variance: %s",
			report.TBNetIncome, report.ISNetIncome, report.NetIncomeVariance)
	}

	// Net Income should be 40000 - 15000 = 25000
	expectedNetIncome := decimal.NewFromInt(25000)
	if !report.TBNetIncome.Equal(expectedNetIncome) {
		t.Errorf("expected TB net income %s, got %s", expectedNetIncome, report.TBNetIncome)
	}
	if !report.ISNetIncome.Equal(expectedNetIncome) {
		t.Errorf("expected IS net income %s, got %s", expectedNetIncome, report.ISNetIncome)
	}

	// Assets should be Cash (85000) + AR (40000) = 125000
	expectedAssets := decimal.NewFromInt(125000)
	if !report.BSAssets.Equal(expectedAssets) {
		t.Errorf("expected assets %s, got %s", expectedAssets, report.BSAssets)
	}

	// Unclassified accounts should be 0
	if len(report.UnclassifiedAccounts) != 0 {
		t.Errorf("expected 0 unclassified accounts, got %d", len(report.UnclassifiedAccounts))
	}
	// Abnormal balances should be 0
	if len(report.AbnormalBalances) != 0 {
		t.Errorf("expected 0 abnormal balances, got %d", len(report.AbnormalBalances))
	}
}

func TestStatementReconciler_UnclassifiedAccountsExposedAndNeverHidden(t *testing.T) {
	cashID := uuid.New()
	unclassifiedID := uuid.New()
	equityID := uuid.New()

	accounts := []masterdata.Account{
		reportAccount(cashID, "1010", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		// Account with invalid empty subclassification
		reportAccount(unclassifiedID, "9999", "Suspense Unknown Account", masterdata.AccountTypeAsset, ""),
		reportAccount(equityID, "3010", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	repo := &memoryRepository{
		accounts: accounts,
		entries: []posting.JournalEntry{
			reportEntry(t, start.AddDate(0, 0, 1),
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "50000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "50000", "USD")},
			),
			reportEntry(t, start.AddDate(0, 0, 5),
				posting.JournalEntryLine{AccountID: unclassifiedID, Debit: reportMoney(t, "1200", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "1200", "USD")},
			),
		},
	}

	reconciler := reporting.NewStatementReconciler(repo)
	report, err := reconciler.Reconcile(context.Background(), uuid.Nil, start, end, "USD")
	if err != nil {
		t.Fatalf("unexpected error during reconciliation: %v", err)
	}

	// Must detect the unclassified account and NOT hide it
	if len(report.UnclassifiedAccounts) == 0 {
		t.Fatalf("expected at least 1 unclassified account detected, got 0")
	}
	found := false
	for _, unclass := range report.UnclassifiedAccounts {
		if unclass.AccountID == unclassifiedID {
			found = true
			if unclass.AccountCode != "9999" {
				t.Errorf("expected code 9999, got %s", unclass.AccountCode)
			}
			if !unclass.NetBalance.Equal(decimal.NewFromInt(1200)) {
				t.Errorf("expected net balance 1200, got %s", unclass.NetBalance)
			}
		}
	}
	if !found {
		t.Errorf("expected unclassified account %s to be listed in report", unclassifiedID)
	}
	// Reconciled should be false because unclassified accounts are present
	if report.Reconciled {
		t.Errorf("expected report.Reconciled to be false when unclassified accounts are present")
	}
}

func TestStatementReconciler_AbnormalBalancesDetected(t *testing.T) {
	cashID := uuid.New()
	apID := uuid.New()
	equityID := uuid.New()

	accounts := []masterdata.Account{
		reportAccount(cashID, "1010", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(apID, "2010", "Trade Payables", masterdata.AccountTypeLiability, masterdata.SubClassTradePayables),
		reportAccount(equityID, "3010", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	// Create an overpayment on Trade Payables making it have a debit balance (Abnormal liability)
	repo := &memoryRepository{
		accounts: accounts,
		entries: []posting.JournalEntry{
			reportEntry(t, start.AddDate(0, 0, 1),
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "50000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "50000", "USD")},
			),
			// Overpayment: Dr AP 5,000, Cr Cash 5,000 when AP had 0 balance!
			reportEntry(t, start.AddDate(0, 0, 10),
				posting.JournalEntryLine{AccountID: apID, Debit: reportMoney(t, "5000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "5000", "USD")},
			),
		},
	}

	reconciler := reporting.NewStatementReconciler(repo)
	report, err := reconciler.Reconcile(context.Background(), uuid.Nil, start, end, "USD")
	if err != nil {
		t.Fatalf("unexpected error during reconciliation: %v", err)
	}

	if len(report.AbnormalBalances) == 0 {
		t.Fatalf("expected at least 1 abnormal balance detected for AP debit position, got 0")
	}
	found := false
	for _, ab := range report.AbnormalBalances {
		if ab.AccountID == apID {
			found = true
			if ab.AccountType != masterdata.AccountTypeLiability {
				t.Errorf("expected liability account type, got %v", ab.AccountType)
			}
		}
	}
	if !found {
		t.Errorf("expected liability account %s with abnormal debit balance to be flagged", apID)
	}
}

func TestStatementReconciler_ComparativePeriodsTied(t *testing.T) {
	cashID := uuid.New()
	equityID := uuid.New()

	accounts := []masterdata.Account{
		reportAccount(cashID, "1010", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(equityID, "3010", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
	}

	priorStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	priorEnd := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	currentStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	currentEnd := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	repo := &memoryRepository{
		accounts: accounts,
		entries: []posting.JournalEntry{
			// Prior period entry
			reportEntry(t, priorStart.AddDate(0, 5, 0),
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "80000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "80000", "USD")},
			),
		},
	}

	reconciler := reporting.NewStatementReconciler(repo)
	compReport, err := reconciler.ReconcileComparativePeriods(
		context.Background(), uuid.Nil,
		priorStart, priorEnd, currentStart, currentEnd, "USD",
	)
	if err != nil {
		t.Fatalf("unexpected error during comparative reconciliation: %v", err)
	}

	if !compReport.OpeningBalancesTied {
		t.Errorf("expected opening balances to tie with prior ending balances, got discrepancies: %d", compReport.DiscrepanciesCount)
	}
}
