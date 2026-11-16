package reporting_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

// Helper to create Money value
func p4Money(t *testing.T, amount string, curr string) financialtypes.Money {
	t.Helper()
	m, err := financialtypes.NewMoneyFromString(amount, curr)
	if err != nil {
		t.Fatalf("failed to create money (%s %s): %v", amount, curr, err)
	}
	return m
}

// GS-01: Core Double-Entry & Multi-Line Posting Invariants (IAS 1 / P4-01)
func TestGoldenScenario_GS01_CoreDoubleEntryInvariants(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()

	companyID := uuid.New()
	company := masterdata.Company{
		ID:                   companyID,
		Code:                 "GS01-ENT",
		LegalName:            "Golden Core Enterprise",
		FunctionalCurrency:   "USD",
		PresentationCurrency: "USD",
		Active:               true,
	}
	if err := driver.CreateCompany(ctx, company); err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)

	fy := masterdata.FiscalYear{
		ID:        uuid.New(),
		CompanyID: companyID,
		Name:      "FY2026",
		StartDate: start,
		EndDate:   end,
		Periods: []masterdata.FiscalPeriod{
			{
				ID:        uuid.New(),
				CompanyID: companyID,
				Number:    1,
				Name:      "P01-2026",
				StartDate: start,
				EndDate:   end,
				State:     masterdata.PeriodOpen,
				Type:      masterdata.FiscalPeriodTypeNormal,
			},
		},
	}
	if err := driver.CreateFiscalYear(ctx, fy); err != nil {
		t.Fatalf("CreateFiscalYear: %v", err)
	}

	cashID := uuid.New()
	equipmentID := uuid.New()
	payablesID := uuid.New()
	capitalID := uuid.New()

	accounts := []masterdata.Account{
		{ID: cashID, CompanyID: companyID, Code: "1010", Name: "Cash & Bank", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, SubClassification: masterdata.SubClassCashAndEquivalents, IsCashEquivalent: true, CashFlowActivity: masterdata.CashFlowCashEquivalent, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
		{ID: equipmentID, CompanyID: companyID, Code: "1500", Name: "Equipment", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, SubClassification: masterdata.SubClassPropertyPlantEquipment, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowInvesting, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
		{ID: payablesID, CompanyID: companyID, Code: "2100", Name: "Trade Payables", AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, SubClassification: masterdata.SubClassTradePayables, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
		{ID: capitalID, CompanyID: companyID, Code: "3000", Name: "Share Capital", AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit, SubClassification: masterdata.SubClassShareCapital, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowFinancing, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
	}
	for _, acc := range accounts {
		if err := driver.CreateAccount(ctx, acc); err != nil {
			t.Fatalf("CreateAccount %s: %v", acc.Code, err)
		}
	}

	journalID := uuid.New()
	if err := driver.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General Journal", Type: masterdata.JournalTypeGeneral, Active: true,
	}); err != nil {
		t.Fatalf("CreateJournal: %v", err)
	}

	zeroMoney, _ := financialtypes.Zero("USD")

	// TX-01: Capital Deposit 500,000 USD
	e1, err := posting.NewJournalEntry(journalID, start.AddDate(0, 0, 1), "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: p4Money(t, "500000", "USD"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: capitalID, Debit: zeroMoney, Credit: p4Money(t, "500000", "USD")},
	})
	if err != nil {
		t.Fatalf("NewJournalEntry TX-01: %v", err)
	}
	e1.CompanyID = companyID
	e1.PeriodID = fy.Periods[0].ID
	if err := driver.SaveDraft(e1); err != nil {
		t.Fatalf("SaveDraft TX-01: %v", err)
	}
	e1.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(e1); err != nil {
		t.Fatalf("PostAndSequence TX-01: %v", err)
	}

	// TX-02: Equipment Purchase 120,000 USD (20k Cash, 100k Payables)
	e2, err := posting.NewJournalEntry(journalID, start.AddDate(0, 0, 5), "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: equipmentID, Debit: p4Money(t, "120000", "USD"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: zeroMoney, Credit: p4Money(t, "20000", "USD")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: payablesID, Debit: zeroMoney, Credit: p4Money(t, "100000", "USD")},
	})
	if err != nil {
		t.Fatalf("NewJournalEntry TX-02: %v", err)
	}
	e2.CompanyID = companyID
	e2.PeriodID = fy.Periods[0].ID
	if err := driver.SaveDraft(e2); err != nil {
		t.Fatalf("SaveDraft TX-02: %v", err)
	}
	e2.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(e2); err != nil {
		t.Fatalf("PostAndSequence TX-02: %v", err)
	}

	tbEngine := reporting.NewTrialBalanceEngine(driver)
	tb, err := tbEngine.GenerateForCompany(companyID, start, end, "USD")
	if err != nil {
		t.Fatalf("Generate Trial Balance: %v", err)
	}

	if !tb.Balanced {
		t.Fatalf("GS-01 Trial Balance is unbalanced: Debit=%s Credit=%s", tb.DebitTotal, tb.CreditTotal)
	}
	expectedTotal := decimal.NewFromInt(600000)
	if !tb.DebitTotal.Equal(expectedTotal) || !tb.CreditTotal.Equal(expectedTotal) {
		t.Fatalf("GS-01 Total = %s, want %s", tb.DebitTotal, expectedTotal)
	}
}

// GS-02: Multi-Currency Revaluation & FX Loss (IAS 21 / P4-02 & P4-03)
func TestGoldenScenario_GS02_IAS21MultiCurrencyRevaluation(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()

	companyID := uuid.New()
	company := masterdata.Company{
		ID:                   companyID,
		Code:                 "GS02-FX",
		LegalName:            "Golden FX Enterprise",
		FunctionalCurrency:   "USD",
		PresentationCurrency: "USD",
		Active:               true,
	}
	if err := driver.CreateCompany(ctx, company); err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)

	fy := masterdata.FiscalYear{
		ID:        uuid.New(),
		CompanyID: companyID,
		Name:      "FY2026",
		StartDate: start,
		EndDate:   end,
		Periods: []masterdata.FiscalPeriod{
			{ID: uuid.New(), CompanyID: companyID, Number: 1, Name: "P01-2026", StartDate: start, EndDate: end, State: masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal},
		},
	}
	if err := driver.CreateFiscalYear(ctx, fy); err != nil {
		t.Fatalf("CreateFiscalYear: %v", err)
	}

	cashID := uuid.New()
	eurPayablesID := uuid.New()
	unrealizedLossID := uuid.New()
	capitalID := uuid.New()

	accounts := []masterdata.Account{
		{ID: cashID, CompanyID: companyID, Code: "1010", Name: "Cash USD", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, SubClassification: masterdata.SubClassCashAndEquivalents, IsCashEquivalent: true, CashFlowActivity: masterdata.CashFlowCashEquivalent, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
		{ID: eurPayablesID, CompanyID: companyID, Code: "2100", Name: "Payables EUR", AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, SubClassification: masterdata.SubClassTradePayables, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
		{ID: unrealizedLossID, CompanyID: companyID, Code: "6500", Name: "Unrealized FX Loss", AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit, SubClassification: masterdata.SubClassGeneralAdminExpense, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
		{ID: capitalID, CompanyID: companyID, Code: "3000", Name: "Capital", AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit, SubClassification: masterdata.SubClassShareCapital, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowFinancing, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
	}
	for _, acc := range accounts {
		if err := driver.CreateAccount(ctx, acc); err != nil {
			t.Fatalf("CreateAccount %s: %v", acc.Code, err)
		}
	}

	journalID := uuid.New()
	if err := driver.CreateJournal(ctx, masterdata.Journal{ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General", Type: masterdata.JournalTypeGeneral, Active: true}); err != nil {
		t.Fatalf("CreateJournal: %v", err)
	}

	zeroMoney, _ := financialtypes.Zero("USD")

	// 1. Initial EUR invoice recorded at 1.05 USD/EUR (10,000 EUR = 10,500 USD)
	e1, err := posting.NewJournalEntry(journalID, start.AddDate(0, 0, 1), "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: cashID, Debit: p4Money(t, "10500", "USD"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: eurPayablesID, Debit: zeroMoney, Credit: p4Money(t, "10500", "USD")},
	})
	if err != nil {
		t.Fatalf("NewJournalEntry EUR Invoice: %v", err)
	}
	e1.CompanyID = companyID
	e1.PeriodID = fy.Periods[0].ID
	if err := driver.SaveDraft(e1); err != nil {
		t.Fatalf("SaveDraft EUR Invoice: %v", err)
	}
	e1.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(e1); err != nil {
		t.Fatalf("PostAndSequence EUR Invoice: %v", err)
	}

	// 2. Period-End Revaluation at 1.10 USD/EUR -> Payables should be 11,000 USD (500 USD Unrealized Loss)
	eReval, err := posting.NewJournalEntry(journalID, end, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: unrealizedLossID, Debit: p4Money(t, "500", "USD"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: eurPayablesID, Debit: zeroMoney, Credit: p4Money(t, "500", "USD")},
	})
	if err != nil {
		t.Fatalf("NewJournalEntry FX Reval: %v", err)
	}
	eReval.CompanyID = companyID
	eReval.PeriodID = fy.Periods[0].ID
	if err := driver.SaveDraft(eReval); err != nil {
		t.Fatalf("SaveDraft FX Reval: %v", err)
	}
	eReval.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(eReval); err != nil {
		t.Fatalf("PostAndSequence FX Reval: %v", err)
	}

	tbEngine := reporting.NewTrialBalanceEngine(driver)
	tb, err := tbEngine.GenerateForCompany(companyID, start, end, "USD")
	if err != nil {
		t.Fatalf("Generate Trial Balance: %v", err)
	}

	if !tb.Balanced {
		t.Fatalf("GS-02 FX Trial Balance is unbalanced: Debit=%s Credit=%s", tb.DebitTotal, tb.CreditTotal)
	}
	expectedTotal := decimal.NewFromInt(11000)
	if !tb.DebitTotal.Equal(expectedTotal) {
		t.Fatalf("GS-02 FX Total = %s, want %s", tb.DebitTotal, expectedTotal)
	}
}

// GS-03: Accruals & Auto-Reversals (IAS 1 / P4-04)
func TestGoldenScenario_GS03_IAS1AccrualAutoReversals(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()

	companyID := uuid.New()
	company := masterdata.Company{ID: companyID, Code: "GS03-ACCR", LegalName: "Golden Accrual Enterprise", FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true}
	if err := driver.CreateCompany(ctx, company); err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	p1Start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p1End := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)
	p2Start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	p2End := time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC)

	fy := masterdata.FiscalYear{
		ID:        uuid.New(),
		CompanyID: companyID,
		Name:      "FY2026",
		StartDate: p1Start,
		EndDate:   p2End,
		Periods: []masterdata.FiscalPeriod{
			{ID: uuid.New(), CompanyID: companyID, Number: 1, Name: "P01-2026", StartDate: p1Start, EndDate: p1End, State: masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal},
			{ID: uuid.New(), CompanyID: companyID, Number: 2, Name: "P02-2026", StartDate: p2Start, EndDate: p2End, State: masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal},
		},
	}
	if err := driver.CreateFiscalYear(ctx, fy); err != nil {
		t.Fatalf("CreateFiscalYear: %v", err)
	}

	expenseID := uuid.New()
	accruedLiabID := uuid.New()

	accounts := []masterdata.Account{
		{ID: expenseID, CompanyID: companyID, Code: "6400", Name: "Utilities Expense", AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit, SubClassification: masterdata.SubClassGeneralAdminExpense, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
		{ID: accruedLiabID, CompanyID: companyID, Code: "2200", Name: "Accrued Expenses", AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, SubClassification: masterdata.SubClassTradePayables, IsCashEquivalent: false, CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual, IsActive: true},
	}
	for _, acc := range accounts {
		if err := driver.CreateAccount(ctx, acc); err != nil {
			t.Fatalf("CreateAccount %s: %v", acc.Code, err)
		}
	}

	journalID := uuid.New()
	if err := driver.CreateJournal(ctx, masterdata.Journal{ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General", Type: masterdata.JournalTypeGeneral, Active: true}); err != nil {
		t.Fatalf("CreateJournal: %v", err)
	}

	zeroMoney, _ := financialtypes.Zero("USD")

	// 1. Accrual Entry at Period 1 End (2026-01-31): Dr Utilities 4,000 / Cr Accrued Expenses 4,000
	accrualEntry, err := posting.NewJournalEntry(journalID, p1End, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: expenseID, Debit: p4Money(t, "4000", "USD"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accruedLiabID, Debit: zeroMoney, Credit: p4Money(t, "4000", "USD")},
	})
	if err != nil {
		t.Fatalf("NewJournalEntry Accrual: %v", err)
	}
	accrualEntry.CompanyID = companyID
	accrualEntry.PeriodID = fy.Periods[0].ID
	accrualEntry.Classification = posting.EntryClassRegular
	if err := driver.SaveDraft(accrualEntry); err != nil {
		t.Fatalf("SaveDraft Accrual: %v", err)
	}
	accrualEntry.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(accrualEntry); err != nil {
		t.Fatalf("PostAndSequence Accrual: %v", err)
	}

	// 2. Auto-Reversal Entry on Day 1 of Period 2 (2026-02-01): Dr Accrued Expenses 4,000 / Cr Utilities 4,000
	reversalEntry, err := posting.NewJournalEntry(journalID, p2Start, "system-reverser", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accruedLiabID, Debit: p4Money(t, "4000", "USD"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: expenseID, Debit: zeroMoney, Credit: p4Money(t, "4000", "USD")},
	})
	if err != nil {
		t.Fatalf("NewJournalEntry Reversal: %v", err)
	}
	reversalEntry.CompanyID = companyID
	reversalEntry.PeriodID = fy.Periods[1].ID
	reversalEntry.Classification = posting.EntryClassReversing
	reversalEntry.OriginalEntryID = &accrualEntry.ID
	if err := driver.SaveDraft(reversalEntry); err != nil {
		t.Fatalf("SaveDraft Reversal: %v", err)
	}
	reversalEntry.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(reversalEntry); err != nil {
		t.Fatalf("PostAndSequence Reversal: %v", err)
	}

	// Verify Period 1 TB: Accrued Expenses = 4,000 Cr, Expense = 4,000 Dr
	tbEngine := reporting.NewTrialBalanceEngine(driver)
	tbP1, err := tbEngine.GenerateForCompany(companyID, p1Start, p1End, "USD")
	if err != nil {
		t.Fatalf("Generate P1 TB: %v", err)
	}
	if !tbP1.Balanced || !tbP1.DebitTotal.Equal(decimal.NewFromInt(4000)) {
		t.Fatalf("GS-03 P1 TB invalid: Balanced=%v Total=%s", tbP1.Balanced, tbP1.DebitTotal)
	}

	// Verify Period 2 Cumulative TB (p1Start to p2End): Net Accrued Expenses = 0
	tbCumulative, err := tbEngine.GenerateForCompany(companyID, p1Start, p2End, "USD")
	if err != nil {
		t.Fatalf("Generate Cumulative TB: %v", err)
	}
	if !tbCumulative.Balanced {
		t.Fatalf("GS-03 Cumulative TB unbalanced: Debit=%s Credit=%s", tbCumulative.DebitTotal, tbCumulative.CreditTotal)
	}
}

// GS-05: Subledger to General Ledger Reconciliation Discrepancy Check (P4-06)
func TestGoldenScenario_GS05_SubledgerToGLTieOut(t *testing.T) {
	companyID := uuid.New()
	controlAccountID := uuid.New()

	report := subledgers.DiscrepancyReport{
		CompanyID:        companyID,
		Domain:           "receivables",
		ControlAccountID: controlAccountID,
		AsOfDate:         time.Now().UTC(),
		FunctionalCurr:   "USD",
		SubledgerBalance: decimal.NewFromInt(20000),
		GLBalance:        decimal.NewFromInt(20000),
		Difference:       decimal.Zero,
		DiscoveredAt:     time.Now().UTC(),
	}

	if report.HasVariance() {
		t.Fatalf("GS-05 expected 0 variance, got %s", report.Difference)
	}
}

// GS-06: Statement Reconciliation & Abnormal Balances Check (IAS 1 / P4-07)
func TestGoldenScenario_GS06_StatementReconciliationAndAbnormalBalances(t *testing.T) {
	cashID := uuid.New()
	equityID := uuid.New()
	revenueID := uuid.New()

	accounts := []masterdata.Account{
		reportAccount(cashID, "1010", "Cash & Cash Equivalents", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		reportAccount(equityID, "3010", "Share Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		reportAccount(revenueID, "4010", "Service Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)

	repo := &memoryRepository{
		accounts: accounts,
		entries: []posting.JournalEntry{
			reportEntry(t, start.AddDate(0, 0, 1),
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100000", "USD")},
			),
			reportEntry(t, start.AddDate(0, 0, 10),
				posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "50000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: revenueID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "50000", "USD")},
			),
		},
	}

	reconciler := reporting.NewStatementReconciler(repo)
	report, err := reconciler.Reconcile(context.Background(), uuid.Nil, start, end, "USD")
	if err != nil {
		t.Fatalf("unexpected error during GS-06 reconciliation: %v", err)
	}

	if !report.TrialBalanceBalanced {
		t.Errorf("expected TB balanced in GS-06")
	}
	if !report.BSBalanced {
		t.Errorf("expected BS balanced in GS-06")
	}
	if !report.NetIncomeReconciled {
		t.Errorf("expected Net Income reconciled in GS-06")
	}
	if len(report.AbnormalBalances) > 0 {
		t.Errorf("expected zero abnormal balances in normal scenario, got %d", len(report.AbnormalBalances))
	}
	if len(report.UnclassifiedAccounts) > 0 {
		t.Errorf("expected zero unclassified accounts in normal scenario, got %d", len(report.UnclassifiedAccounts))
	}
}
