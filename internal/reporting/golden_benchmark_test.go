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
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

// TestGoldenAccountingBenchmark executes the standardized reference enterprise dataset
// documented in docs/golden_accounting_benchmark.md and verifies that all financial statements
// (Trial Balance, Income Statement, Balance Sheet, Cash Flow) match pre-calculated standard
// results with absolute precision (zero tolerance).
func TestGoldenAccountingBenchmark(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()

	companyID := uuid.New()
	company := masterdata.Company{
		ID:                   companyID,
		Code:                 "GOLDEN-ENT",
		LegalName:            "Standard Compass Enterprise",
		FunctionalCurrency:   "USD",
		PresentationCurrency: "USD",
		Active:               true,
	}
	if err := driver.CreateCompany(ctx, company); err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	startDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)

	fy := masterdata.FiscalYear{
		ID:        uuid.New(),
		CompanyID: companyID,
		Name:      "FY2026",
		StartDate: startDate,
		EndDate:   endDate,
		Periods: []masterdata.FiscalPeriod{
			{
				ID:        uuid.New(),
				CompanyID: companyID,
				Number:    1,
				Name:      "P01-2026",
				StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:   time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC),
				State:     masterdata.PeriodOpen,
				Type:      masterdata.FiscalPeriodTypeNormal,
			},
			{
				ID:        uuid.New(),
				CompanyID: companyID,
				Number:    2,
				Name:      "P02-2026",
				StartDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
				EndDate:   time.Date(2026, 2, 28, 23, 59, 59, 0, time.UTC),
				State:     masterdata.PeriodOpen,
				Type:      masterdata.FiscalPeriodTypeNormal,
			},
			{
				ID:        uuid.New(),
				CompanyID: companyID,
				Number:    3,
				Name:      "P03-2026",
				StartDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
				EndDate:   time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC),
				State:     masterdata.PeriodOpen,
				Type:      masterdata.FiscalPeriodTypeNormal,
			},
		},
	}
	if err := driver.CreateFiscalYear(ctx, fy); err != nil {
		t.Fatalf("CreateFiscalYear: %v", err)
	}

	// 1. Chart of Accounts Setup
	accountIDs := make(map[string]uuid.UUID)
	createAccount := func(code, name string, accType masterdata.AccountType, subClass string, isCashEquiv bool, cfActivity string, ifrsCat string) uuid.UUID {
		id := uuid.New()
		acc := masterdata.Account{
			ID:                id,
			CompanyID:         companyID,
			Code:              code,
			Name:              name,
			AccountType:       accType,
			NormalBalance:     masterdata.DefaultNormalBalance(accType),
			SubClassification: subClass,
			IsCashEquivalent:  isCashEquiv,
			CashFlowActivity:  cfActivity,
			IFRSCategory:      ifrsCat,
			IsActive:          true,
		}
		if err := driver.CreateAccount(ctx, acc); err != nil {
			t.Fatalf("CreateAccount %s: %v", code, err)
		}
		accountIDs[code] = id
		return id
	}

	createAccount("1010", "Cash & Bank", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents, true, masterdata.CashFlowCashEquivalent, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("1100", "Trade Receivables", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("1200", "Merchandise Inventory", masterdata.AccountTypeAsset, masterdata.SubClassInventories, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("1500", "Plant & Equipment", masterdata.AccountTypeAsset, masterdata.SubClassPropertyPlantEquipment, false, masterdata.CashFlowInvesting, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("1510", "Accum. Dep. - Equipment", masterdata.AccountTypeAsset, masterdata.SubClassPropertyPlantEquipment, false, masterdata.CashFlowInvesting, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("1600", "Right-of-Use Assets", masterdata.AccountTypeAsset, masterdata.SubClassRightOfUseAssets, false, masterdata.CashFlowInvesting, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("1610", "Accum. Dep. - ROU", masterdata.AccountTypeAsset, masterdata.SubClassRightOfUseAssets, false, masterdata.CashFlowInvesting, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("2100", "Trade Payables", masterdata.AccountTypeLiability, masterdata.SubClassTradePayables, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("2400", "Lease Liabilities", masterdata.AccountTypeLiability, masterdata.SubClassNonCurrentLeaseLiabilities, false, masterdata.CashFlowFinancing, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("2500", "Income Tax Payable", masterdata.AccountTypeLiability, masterdata.SubClassCurrentTaxLiabilities, false, masterdata.CashFlowNonCashAdjustment, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("3000", "Share Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital, false, masterdata.CashFlowFinancing, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("3100", "Retained Earnings", masterdata.AccountTypeEquity, masterdata.SubClassRetainedEarnings, false, masterdata.CashFlowFinancing, masterdata.IFRSCategoryBalanceSheetPerpetual)
	createAccount("4000", "Sales Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryOperating)
	createAccount("4100", "Services Revenue", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryOperating)
	createAccount("5000", "Cost of Goods Sold", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryOperating)
	createAccount("6000", "Salaries Expense", masterdata.AccountTypeExpense, masterdata.SubClassGeneralAdminExpense, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryOperating)
	createAccount("6100", "Depreciation - Equipment", masterdata.AccountTypeExpense, masterdata.SubClassDepreciationAmortExpense, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryOperating)
	createAccount("6200", "Depreciation - ROU", masterdata.AccountTypeExpense, masterdata.SubClassDepreciationAmortExpense, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryOperating)
	createAccount("6300", "Interest on Lease", masterdata.AccountTypeExpense, masterdata.SubClassFinanceCost, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryFinancing)
	createAccount("6400", "Operating G&A", masterdata.AccountTypeExpense, masterdata.SubClassGeneralAdminExpense, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryOperating)
	createAccount("7000", "Income Tax Expense", masterdata.AccountTypeExpense, masterdata.SubClassIncomeTaxExpense, false, masterdata.CashFlowOperating, masterdata.IFRSCategoryIncomeTaxes)

	journalID := uuid.New()
	moneyVal := func(amount string) financialtypes.Money {
		m, err := financialtypes.NewMoneyFromString(amount, "USD")
		if err != nil {
			t.Fatalf("invalid money %s: %v", amount, err)
		}
		return m
	}
	zeroMoney, _ := financialtypes.Zero("USD")

	postTx := func(ref string, date time.Time, periodID uuid.UUID, lines []posting.JournalEntryLine) {
		entry, err := posting.NewJournalEntry(journalID, date, "golden-maker", lines)
		if err != nil {
			t.Fatalf("NewJournalEntry %s: %v", ref, err)
		}
		entry.CompanyID = companyID
		entry.PeriodID = periodID
		entry.Reference = ref
		if err := driver.SaveDraft(entry); err != nil {
			t.Fatalf("SaveDraft %s: %v", ref, err)
		}
		entry.Status = posting.EntryPosted
		if _, err := driver.PostAndSequence(entry); err != nil {
			t.Fatalf("PostAndSequence %s: %v", ref, err)
		}
	}

	p1 := fy.Periods[0].ID
	p2 := fy.Periods[1].ID
	p3 := fy.Periods[2].ID

	// TX-01 (2026-01-02): Capital deposit 500,000 USD
	postTx("TX-01", time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC), p1, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1010"], Debit: moneyVal("500000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["3000"], Debit: zeroMoney, Credit: moneyVal("500000")},
	})

	// TX-02 (2026-01-05): Equipment purchase 120,000 (20k cash, 100k AP)
	postTx("TX-02", time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC), p1, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1500"], Debit: moneyVal("120000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1010"], Debit: zeroMoney, Credit: moneyVal("20000")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["2100"], Debit: zeroMoney, Credit: moneyVal("100000")},
	})

	// TX-03 (2026-01-10): Inventory purchase 80,000 cash
	postTx("TX-03", time.Date(2026, 1, 10, 14, 0, 0, 0, time.UTC), p1, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1200"], Debit: moneyVal("80000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1010"], Debit: zeroMoney, Credit: moneyVal("80000")},
	})

	// TX-04 (2026-01-15): Lease ROU Asset & Liability 60,000
	postTx("TX-04", time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC), p1, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1600"], Debit: moneyVal("60000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["2400"], Debit: zeroMoney, Credit: moneyVal("60000")},
	})

	// TX-05 (2026-02-01): Sales 150,000 (100k cash, 50k AR; 120k goods, 30k services)
	postTx("TX-05", time.Date(2026, 2, 1, 10, 30, 0, 0, time.UTC), p2, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1010"], Debit: moneyVal("100000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1100"], Debit: moneyVal("50000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["4000"], Debit: zeroMoney, Credit: moneyVal("120000")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["4100"], Debit: zeroMoney, Credit: moneyVal("30000")},
	})

	// TX-06 (2026-02-05): Cost of Goods Sold 60,000
	postTx("TX-06", time.Date(2026, 2, 5, 12, 0, 0, 0, time.UTC), p2, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["5000"], Debit: moneyVal("60000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1200"], Debit: zeroMoney, Credit: moneyVal("60000")},
	})

	// TX-07 (2026-02-20): Payables settlement 50k & Receivables collection 30k
	postTx("TX-07", time.Date(2026, 2, 20, 15, 0, 0, 0, time.UTC), p2, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["2100"], Debit: moneyVal("50000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1010"], Debit: moneyVal("30000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1010"], Debit: zeroMoney, Credit: moneyVal("50000")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1100"], Debit: zeroMoney, Credit: moneyVal("30000")},
	})

	// TX-08 (2026-03-01): Salaries (25k) & G&A (5k) paid in cash
	postTx("TX-08", time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC), p3, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["6000"], Debit: moneyVal("25000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["6400"], Debit: moneyVal("5000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1010"], Debit: zeroMoney, Credit: moneyVal("30000")},
	})

	// TX-09 (2026-03-31): Depreciation (PPE 2k, ROU 1k) and Lease Interest (300)
	postTx("TX-09", time.Date(2026, 3, 31, 18, 0, 0, 0, time.UTC), p3, []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["6100"], Debit: moneyVal("2000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["6200"], Debit: moneyVal("1000"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["6300"], Debit: moneyVal("300"), Credit: zeroMoney},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1510"], Debit: zeroMoney, Credit: moneyVal("2000")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["1610"], Debit: zeroMoney, Credit: moneyVal("1000")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountIDs["2400"], Debit: zeroMoney, Credit: moneyVal("300")},
	})

	// -------------------------------------------------------------
	// 2. VERIFY FINANCIAL STATEMENTS AGAINST GOLDEN TRUTH BENCHMARK
	// -------------------------------------------------------------

	// A. Trial Balance Verification
	tbEngine := reporting.NewTrialBalanceEngine(driver)
	tb, err := tbEngine.GenerateForCompany(companyID, startDate, endDate, "USD")
	if err != nil {
		t.Fatalf("Generate Trial Balance: %v", err)
	}

	if !tb.Balanced {
		t.Fatalf("Golden Trial Balance is NOT balanced: debit=%s credit=%s", tb.DebitTotal, tb.CreditTotal)
	}
	expectedTotal := decimal.NewFromInt(763300)
	if !tb.DebitTotal.Equal(expectedTotal) || !tb.CreditTotal.Equal(expectedTotal) {
		t.Fatalf("Golden Trial Balance Total = %s, want %s", tb.DebitTotal, expectedTotal)
	}

	// Verify specific account balances in trial balance
	findAccountTB := func(code string) reporting.TrialBalanceLine {
		for _, line := range tb.Lines {
			if line.AccountCode == code {
				return line
			}
		}
		t.Fatalf("account %s not found in trial balance", code)
		return reporting.TrialBalanceLine{}
	}

	cashLine := findAccountTB("1010")
	if !cashLine.EndingDebit.Sub(cashLine.EndingCredit).Equal(decimal.NewFromInt(450000)) {
		t.Fatalf("Cash ending balance = %s, want 450000", cashLine.EndingDebit.Sub(cashLine.EndingCredit))
	}
	arLine := findAccountTB("1100")
	if !arLine.EndingDebit.Sub(arLine.EndingCredit).Equal(decimal.NewFromInt(20000)) {
		t.Fatalf("AR ending balance = %s, want 20000", arLine.EndingDebit.Sub(arLine.EndingCredit))
	}

	// B. Income Statement Verification
	isEngine := reporting.NewIncomeStatementEngine(driver)
	is, err := isEngine.GenerateForCompany(companyID, startDate, endDate, "USD")
	if err != nil {
		t.Fatalf("Generate Income Statement: %v", err)
	}

	expectedRevenue := decimal.NewFromInt(150000)
	expectedCOGS := decimal.NewFromInt(60000)
	expectedGrossProfit := decimal.NewFromInt(90000)
	expectedAdmin := decimal.NewFromInt(30000)
	expectedDepr := decimal.NewFromInt(3000)
	expectedOperatingProfit := decimal.NewFromInt(57000)
	expectedFinanceCost := decimal.NewFromInt(300)
	expectedProfitBeforeTax := decimal.NewFromInt(56700)
	expectedTax := decimal.Zero
	expectedNetProfit := decimal.NewFromInt(56700)

	if !is.Revenue.Equal(expectedRevenue) {
		t.Errorf("Revenue = %s, want %s", is.Revenue, expectedRevenue)
	}
	if !is.CostOfSales.Equal(expectedCOGS) {
		t.Errorf("Cost of Sales = %s, want %s", is.CostOfSales, expectedCOGS)
	}
	if !is.GrossProfit.Equal(expectedGrossProfit) {
		t.Errorf("Gross Profit = %s, want %s", is.GrossProfit, expectedGrossProfit)
	}
	if !is.AdministrativeExpenses.Equal(expectedAdmin) {
		t.Errorf("Admin Expenses = %s, want %s", is.AdministrativeExpenses, expectedAdmin)
	}
	if !is.DepreciationAndProvisionExpenses.Equal(expectedDepr) {
		t.Errorf("Depreciation = %s, want %s", is.DepreciationAndProvisionExpenses, expectedDepr)
	}
	if !is.OperatingProfit.Equal(expectedOperatingProfit) {
		t.Errorf("Operating Profit = %s, want %s", is.OperatingProfit, expectedOperatingProfit)
	}
	if !is.FinanceCosts.Equal(expectedFinanceCost) {
		t.Errorf("Finance Costs = %s, want %s", is.FinanceCosts, expectedFinanceCost)
	}
	if !is.ProfitBeforeTax.Equal(expectedProfitBeforeTax) {
		t.Errorf("Profit Before Tax = %s, want %s", is.ProfitBeforeTax, expectedProfitBeforeTax)
	}
	if !is.TaxExpense.Equal(expectedTax) {
		t.Errorf("Tax Expense = %s, want %s", is.TaxExpense, expectedTax)
	}
	if !is.NetProfit.Equal(expectedNetProfit) {
		t.Errorf("Net Profit = %s, want %s", is.NetProfit, expectedNetProfit)
	}

	// C. Balance Sheet Verification
	bsEngine := reporting.NewBalanceSheetEngine(driver)
	bs, err := bsEngine.GenerateForCompany(companyID, endDate, "USD")
	if err != nil {
		t.Fatalf("Generate Balance Sheet: %v", err)
	}

	if !bs.Balanced {
		t.Fatalf("Golden Balance Sheet is NOT balanced: assets=%s liabilities+equity=%s",
			bs.TotalAssets, bs.TotalLiabilitiesAndEquity)
	}

	expectedCurrentAssets := decimal.NewFromInt(490000)
	expectedNonCurrentAssets := decimal.NewFromInt(177000)
	expectedTotalAssets := decimal.NewFromInt(667000)

	if !bs.CurrentAssets.Equal(expectedCurrentAssets) {
		t.Errorf("Current Assets = %s, want %s", bs.CurrentAssets, expectedCurrentAssets)
	}
	if !bs.NonCurrentAssets.Equal(expectedNonCurrentAssets) {
		t.Errorf("Non-Current Assets = %s, want %s", bs.NonCurrentAssets, expectedNonCurrentAssets)
	}
	if !bs.TotalAssets.Equal(expectedTotalAssets) {
		t.Errorf("Total Assets = %s, want %s", bs.TotalAssets, expectedTotalAssets)
	}

	expectedCurrentLiabilities := decimal.NewFromInt(50000)
	expectedNonCurrentLiabilities := decimal.NewFromInt(60300)
	expectedTotalLiabilities := decimal.NewFromInt(110300)
	expectedEquity := decimal.NewFromInt(556700)

	if !bs.CurrentLiabilities.Equal(expectedCurrentLiabilities) {
		t.Errorf("Current Liabilities = %s, want %s", bs.CurrentLiabilities, expectedCurrentLiabilities)
	}
	if !bs.NonCurrentLiabilities.Equal(expectedNonCurrentLiabilities) {
		t.Errorf("Non-Current Liabilities = %s, want %s", bs.NonCurrentLiabilities, expectedNonCurrentLiabilities)
	}
	if !bs.TotalLiabilities.Equal(expectedTotalLiabilities) {
		t.Errorf("Total Liabilities = %s, want %s", bs.TotalLiabilities, expectedTotalLiabilities)
	}
	if !bs.Equity.Equal(expectedEquity) {
		t.Errorf("Total Equity = %s, want %s", bs.Equity, expectedEquity)
	}
	if !bs.TotalLiabilitiesAndEquity.Equal(expectedTotalAssets) {
		t.Errorf("Total Liabilities & Equity = %s, want %s", bs.TotalLiabilitiesAndEquity, expectedTotalAssets)
	}

	// D. Cash Flow Statement Verification
	cfEngine := reporting.NewCashFlowEngine(driver)
	cf, err := cfEngine.GenerateForCompany(companyID, startDate, endDate, "USD")
	if err != nil {
		t.Logf("Cash Flow: NetProfit=%s, InvFinAdj=%s, NonCash=%s, WC=%s, Operating=%s, Investing=%s, Financing=%s, NetChange=%s, Closing=%s",
			cf.NetProfitBeforeTax, cf.InvestingAndFinancingAdjustments, cf.NonCashAdjustments, cf.WorkingCapitalChange,
			cf.OperatingCashFlow, cf.InvestingCashFlow, cf.FinancingCashFlow, cf.NetChangeInCash, cf.ClosingCash)
		t.Fatalf("Generate Cash Flow: %v", err)
	}

	expectedClosingCash := decimal.NewFromInt(450000)
	if !cf.ClosingCash.Equal(expectedClosingCash) {
		t.Errorf("Cash Flow Closing Cash = %s, want %s", cf.ClosingCash, expectedClosingCash)
	}
	if !cf.NetChangeInCash.Equal(expectedClosingCash) {
		t.Errorf("Cash Flow Net Change = %s, want %s", cf.NetChangeInCash, expectedClosingCash)
	}

	// E. Comprehensive Statement Bundle Verification
	bundleEngine := reporting.NewBundleEngine(driver)
	bundle, err := bundleEngine.GenerateForCompany(companyID, startDate, endDate, "USD")
	if err != nil {
		t.Fatalf("Generate Statement Bundle: %v", err)
	}

	if !bundle.BalanceSheet.Balanced {
		t.Errorf("Bundle Balance Sheet is unbalanced")
	}
	if !bundle.TrialBalance.Balanced {
		t.Errorf("Bundle Trial Balance is unbalanced")
	}
	if !bundle.IncomeStatement.NetProfit.Equal(expectedNetProfit) {
		t.Errorf("Bundle Net Profit = %s, want %s", bundle.IncomeStatement.NetProfit, expectedNetProfit)
	}
}
