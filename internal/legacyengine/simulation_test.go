package legacyengine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/governance"
	accountingengine "github.com/osmai4603-collab/accounting_engine/internal/legacyengine"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestConcurrentPostingSequenceIntegrity(t *testing.T) {
	const workers = 50
	const entriesPerWorker = 1000
	const totalEntries = workers * entriesPerWorker
	driver := memory.New()
	journalID := uuid.New()
	debitAccountID, creditAccountID := uuid.New(), uuid.New()
	amount, err := financialtypes.NewMoneyFromString("1", "USD")
	if err != nil {
		t.Fatal(err)
	}

	sequences := make(chan int64, totalEntries)
	errorsFound := make(chan error, workers)
	var workersGroup sync.WaitGroup
	for worker := range workers {
		workersGroup.Add(1)
		go func(worker int) {
			defer workersGroup.Done()
			for range entriesPerWorker {
				entry, err := posting.NewJournalEntry(journalID, time.Now().UTC(), "maker",
					[]posting.JournalEntryLine{
						{ID: uuid.New(), AccountID: debitAccountID, Debit: amount},
						{ID: uuid.New(), AccountID: creditAccountID, Credit: amount},
					})
				if err != nil {
					errorsFound <- err
					return
				}
				if err := driver.SaveDraft(entry); err != nil {
					errorsFound <- err
					return
				}
				entry.Status = posting.EntryPosted
				posted, err := driver.PostAndSequence(entry)
				if err != nil {
					errorsFound <- err
					return
				}
				sequences <- posted.SequenceNumber
			}
		}(worker)
	}
	workersGroup.Wait()
	close(sequences)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	seen := make([]bool, totalEntries+1)
	count := 0
	for sequence := range sequences {
		if sequence < 1 || sequence > totalEntries || seen[sequence] {
			t.Fatalf("invalid or duplicate journal sequence %d", sequence)
		}
		seen[sequence] = true
		count++
	}
	if count != totalEntries {
		t.Fatalf("posted entries = %d, want %d", count, totalEntries)
	}
	for sequence := 1; sequence <= totalEntries; sequence++ {
		if !seen[sequence] {
			t.Fatalf("journal sequence %d is missing", sequence)
		}
	}
}

func TestAccountingEngineExposesCompanyFinancialDimensions(t *testing.T) {
	driver := memory.New()
	ctx := context.Background()
	engine, err := accountingengine.New(driver, accountingengine.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	companyID, centerID, projectID := uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "DIM-API", LegalName: "Dimension API",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SaveCostCenter(context.Background(), masterdata.CostCenter{
		ID: centerID, CompanyID: companyID, Code: "OPS", Name: "Operations", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SaveAnalyticalDimension(context.Background(), masterdata.AnalyticalDimensionValue{
		ID: projectID, CompanyID: companyID, Kind: masterdata.DimensionProject,
		Code: "PROJECT-1", Name: "Project 1", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DimensionProfitAndLossForCompanyContext(
		context.Background(), companyID, time.Now().AddDate(0, 0, -1), time.Now(), "USD",
	); err != nil {
		t.Fatalf("company-scoped dimension report: %v", err)
	}
}

func TestRejectedPostingDoesNotConsumeSequence(t *testing.T) {
	driver := memory.New()
	journalID := uuid.New()
	amount, err := financialtypes.NewMoneyFromString("1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	entry := mustEntry(t, journalID, time.Now().UTC(), uuid.New(), uuid.New(), amount, true)
	if err := driver.SaveDraft(entry); err != nil {
		t.Fatal(err)
	}
	modified := entry
	modified.Reference = "changed-after-draft"
	modified.Status = posting.EntryPosted
	if _, err := driver.PostAndSequence(modified); !errors.Is(err, financialtypes.ErrCannotModifyPostedMove) {
		t.Fatalf("PostAndSequence() error = %v, want immutable-draft error", err)
	}
	entry.Status = posting.EntryPosted
	posted, err := driver.PostAndSequence(entry)
	if err != nil {
		t.Fatal(err)
	}
	if posted.SequenceNumber != 1 {
		t.Fatalf("first valid entry sequence = %d, want 1", posted.SequenceNumber)
	}
}

func TestFullFiscalYearSimulationFacade(t *testing.T) {
	driver := memory.New()
	ctx := context.Background()
	engine, err := accountingengine.New(driver, accountingengine.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "SIM-2026", LegalName: "Simulation Company",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	year := masterdata.FiscalYear{ID: uuid.New(), CompanyID: companyID, Name: "FY2026", StartDate: start, EndDate: end}
	for month := 1; month <= 12; month++ {
		periodStart := time.Date(2026, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		periodEnd := periodStart.AddDate(0, 1, 0).Add(-time.Nanosecond)
		year.Periods = append(year.Periods, masterdata.FiscalPeriod{
			ID: uuid.New(), FiscalYearID: year.ID, Number: month, Name: periodStart.Month().String(),
			StartDate: periodStart, EndDate: periodEnd, State: masterdata.PeriodOpen,
		})
	}
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	accounts := map[string]uuid.UUID{}
	definitions := []masterdata.Account{
		{Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset},
		{Code: "1200", Name: "Accounts receivable", AccountType: masterdata.AccountTypeAsset},
		{Code: "1500", Name: "Fixed assets", AccountType: masterdata.AccountTypeAsset},
		{Code: "3000", Name: "Share capital", AccountType: masterdata.AccountTypeEquity},
		{Code: "4000", Name: "Sales", AccountType: masterdata.AccountTypeIncome, IFRSCategory: masterdata.IFRSCategoryOperating},
		{Code: "5000", Name: "Expenses", AccountType: masterdata.AccountTypeExpense, IFRSCategory: masterdata.IFRSCategoryOperating},
	}
	for _, account := range definitions {
		account.ID = uuid.New()
		account.CompanyID = companyID
		if err := engine.MasterData.RegisterAccount(account); err != nil {
			t.Fatal(err)
		}
		accounts[account.Code] = account.ID
	}

	journalID := uuid.New()
	if err := driver.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General", Type: masterdata.JournalTypeGeneral, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	amount, _ := financialtypes.NewMoneyFromString("100000", "USD")
	capital, err := posting.NewJournalEntry(journalID, start.Add(24*time.Hour), "maker",
		[]posting.JournalEntryLine{
			{ID: uuid.New(), AccountID: accounts["1000"], Debit: amount},
			{ID: uuid.New(), AccountID: accounts["3000"], Credit: amount},
		})
	if err != nil {
		t.Fatal(err)
	}
	capital.CompanyID = companyID
	if err := engine.Posting.CreateDraft(capital); err != nil {
		t.Fatal(err)
	}
	if err := engine.Posting.Post(capital.CompanyID, capital.ID, "checker"); err != nil {
		t.Fatal(err)
	}

	asset, _ := financialtypes.NewMoneyFromString("60000", "USD")
	sales, _ := financialtypes.NewMoneyFromString("40000", "USD")
	for _, entry := range []posting.JournalEntry{
		mustEntry(t, journalID, start.Add(30*24*time.Hour), accounts["1500"], accounts["1000"], asset, true),
		mustEntry(t, journalID, start.Add(60*24*time.Hour), accounts["1200"], accounts["4000"], sales, true),
		mustEntry(t, journalID, start.Add(90*24*time.Hour), accounts["5000"], accounts["1000"], sales, true),
	} {
		entry.CompanyID = companyID
		if err := engine.Posting.CreateDraft(entry); err != nil {
			t.Fatal(err)
		}
		if err := engine.Posting.Post(entry.CompanyID, entry.ID, "checker"); err != nil {
			t.Fatal(err)
		}
	}

	revenueExpense, _ := financialtypes.NewMoneyFromString("40000", "USD")
	partner := uuid.New()
	if _, err := engine.Subledgers.Receivables.AddInvoice(partner, revenueExpense, start, end, "INV-1"); err != nil {
		t.Fatal(err)
	}
	event := subledgers.EconomicEvent{IdempotencyKey: "simulation-1", EventType: "sale", Amount: revenueExpense, Date: start.Add(60 * 24 * time.Hour), PartnerID: &partner}
	if err := engine.Subledgers.Repository.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}

	trial, err := engine.Reporting.TrialBalance.Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !trial.Balanced || !trial.DebitTotal.Equal(trial.CreditTotal) {
		t.Fatalf("trial balance is not balanced: debit=%s credit=%s", trial.DebitTotal, trial.CreditTotal)
	}

	for range 25 {
		log := governance.FinancialAuditLog{
			EntityType: "move", EntityID: uuid.New(), Action: "POST", ActorID: uuid.New(),
			PayloadBefore: []byte("null"), PayloadAfter: []byte(`{"posted":true}`),
			Timestamp: time.Now().UTC(),
		}
		if err := engine.Governance.Append(context.Background(), log); err != nil {
			t.Fatal(err)
		}
	}
	valid, err := engine.Governance.VerifyIntegrity(context.Background())
	if err != nil || !valid {
		t.Fatalf("audit integrity = (%v, %v)", valid, err)
	}

	balanceSheet, err := engine.Reporting.BalanceSheet.Generate(end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !balanceSheet.Balanced {
		t.Fatal("balance sheet is not balanced")
	}
}

func mustEntry(t *testing.T, journalID uuid.UUID, date time.Time, debitAccount, creditAccount uuid.UUID, amount financialtypes.Money, debitThenCredit bool) posting.JournalEntry {
	t.Helper()
	if !debitThenCredit {
		debitAccount, creditAccount = creditAccount, debitAccount
	}
	lines := []posting.JournalEntryLine{
		{ID: uuid.New(), AccountID: debitAccount, Debit: amount},
		{ID: uuid.New(), AccountID: creditAccount, Credit: amount},
	}
	entry, err := posting.NewJournalEntry(journalID, date, "maker", lines)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestFiveYearFiscalCycleSimulation(t *testing.T) {
	driver := memory.New()
	ctx := context.Background()
	companyID := uuid.New()
	company := masterdata.Company{
		ID:                   companyID,
		Code:                 "SAR-CORP",
		LegalName:            "Saudi Pure Financial Corp",
		FunctionalCurrency:   "SAR",
		PresentationCurrency: "SAR",
		Active:               true,
	}
	if err := driver.CreateCompany(ctx, company); err != nil {
		t.Fatal(err)
	}

	accounts := map[string]uuid.UUID{}
	definitions := []masterdata.Account{
		{Code: "1000", Name: "Cash and Cash Equivalents", AccountType: masterdata.AccountTypeAsset, CashFlowActivity: masterdata.CashFlowCashEquivalent, SubClassification: masterdata.SubClassCashAndEquivalents},
		{Code: "1200", Name: "Trade Receivables", AccountType: masterdata.AccountTypeAsset, CashFlowActivity: masterdata.CashFlowOperating, SubClassification: masterdata.SubClassAccountsReceivable},
		{Code: "1500", Name: "Property Plant & Equipment", AccountType: masterdata.AccountTypeAsset, CashFlowActivity: masterdata.CashFlowInvesting, SubClassification: masterdata.SubClassPropertyPlantEquipment},
		{Code: "1590", Name: "Accumulated Depreciation", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceCredit, IsContraAccount: true, CashFlowActivity: masterdata.CashFlowNonCashAdjustment, SubClassification: masterdata.SubClassPropertyPlantEquipment},
		{Code: "2000", Name: "Trade Payables", AccountType: masterdata.AccountTypeLiability, CashFlowActivity: masterdata.CashFlowOperating, SubClassification: masterdata.SubClassAccountsPayable},
		{Code: "3000", Name: "Share Capital", AccountType: masterdata.AccountTypeEquity, CashFlowActivity: masterdata.CashFlowFinancing, SubClassification: "capital"},
		{Code: "3200", Name: "Retained Earnings", AccountType: masterdata.AccountTypeEquity, CashFlowActivity: masterdata.CashFlowFinancing, SubClassification: masterdata.SubClassRetainedEarnings},
		{Code: "4000", Name: "Operating Revenue", AccountType: masterdata.AccountTypeIncome, IFRSCategory: masterdata.IFRSCategoryOperating, CashFlowActivity: masterdata.CashFlowOperating},
		{Code: "5000", Name: "Operating Expenses", AccountType: masterdata.AccountTypeExpense, IFRSCategory: masterdata.IFRSCategoryOperating, CashFlowActivity: masterdata.CashFlowOperating},
		{Code: "5500", Name: "Depreciation Expense", AccountType: masterdata.AccountTypeExpense, IFRSCategory: masterdata.IFRSCategoryOperating, CashFlowActivity: masterdata.CashFlowNonCashAdjustment, SubClassification: masterdata.SubClassDepreciationAmortExpense},
		{Code: "5800", Name: "Audit Adjustment Expense", AccountType: masterdata.AccountTypeExpense, IFRSCategory: masterdata.IFRSCategoryOperating, CashFlowActivity: masterdata.CashFlowOperating},
	}
	for _, acc := range definitions {
		acc.ID = uuid.New()
		acc.CompanyID = companyID
		if acc.NormalBalance == "" {
			acc.NormalBalance = masterdata.DefaultNormalBalance(acc.AccountType)
		}
		if err := driver.CreateAccount(ctx, acc); err != nil {
			t.Fatal(err)
		}
		accounts[acc.Code] = acc.ID
	}

	journalID := uuid.New()
	if err := driver.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General", Type: masterdata.JournalTypeGeneral, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	var postingFiscalYear masterdata.FiscalYear
	postEntry := func(date time.Time, lines []posting.JournalEntryLine, ref string) posting.JournalEntry {
		entry, err := posting.NewJournalEntry(journalID, date, "maker", lines)
		if err != nil {
			t.Fatalf("NewJournalEntry(%s): %v", ref, err)
		}
		entry.CompanyID = companyID
		entry.Reference = ref
		periodNumber := 0
		if strings.Contains(ref, "P13") {
			periodNumber = 13
		} else if strings.Contains(ref, "P14") {
			periodNumber = 14
		}
		for _, period := range postingFiscalYear.Periods {
			if (periodNumber != 0 && period.Number == periodNumber) ||
				(periodNumber == 0 && period.Contains(date)) {
				entry.PeriodID = period.ID
				break
			}
		}
		if entry.PeriodID == uuid.Nil {
			t.Fatalf("no fiscal period found for posting %s", ref)
		}
		if err := driver.SaveDraft(entry); err != nil {
			t.Fatalf("SaveDraft(%s): %v", ref, err)
		}
		entry.Status = posting.EntryPosted
		posted, err := driver.PostAndSequence(entry)
		if err != nil {
			t.Fatalf("PostAndSequence(%s): %v", ref, err)
		}
		return posted
	}

	// 5-Year Simulation Loop (2022 to 2026)
	salesPerMonth, _ := financialtypes.NewMoneyFromString("20000", "SAR")
	collPerMonth, _ := financialtypes.NewMoneyFromString("15000", "SAR")
	opexPerMonth, _ := financialtypes.NewMoneyFromString("10000", "SAR")
	deprPeriod13, _ := financialtypes.NewMoneyFromString("5000", "SAR")
	auditPeriod14, _ := financialtypes.NewMoneyFromString("1000", "SAR")
	zero, _ := financialtypes.Zero("SAR")
	var previousFiscalYearID uuid.UUID

	for yearIndex := range 5 {
		yearNum := 2022 + yearIndex
		yearStart := time.Date(yearNum, 1, 1, 0, 0, 0, 0, time.UTC)
		yearEnd := time.Date(yearNum, 12, 31, 23, 59, 59, 0, time.UTC)
		fiscalYear := masterdata.FiscalYear{
			ID:        uuid.New(),
			CompanyID: companyID,
			Name:      fmt.Sprintf("FY%d", yearNum),
			StartDate: yearStart,
			EndDate:   yearEnd,
		}
		// 12 regular monthly periods + Period 13 (Year-end) + Period 14 (Audit)
		for m := 1; m <= 12; m++ {
			pStart := time.Date(yearNum, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
			pEnd := pStart.AddDate(0, 1, 0).Add(-time.Nanosecond)
			fiscalYear.Periods = append(fiscalYear.Periods, masterdata.FiscalPeriod{
				ID: uuid.New(), CompanyID: companyID, FiscalYearID: fiscalYear.ID,
				Number: m, Name: fmt.Sprintf("%d-%02d", yearNum, m),
				StartDate: pStart, EndDate: pEnd, State: masterdata.PeriodOpen,
				Type: masterdata.FiscalPeriodTypeNormal,
			})
		}
		fiscalYear.Periods = append(fiscalYear.Periods,
			masterdata.FiscalPeriod{
				ID: uuid.New(), CompanyID: companyID, FiscalYearID: fiscalYear.ID,
				Number: 13, Name: fmt.Sprintf("%d-Adj13", yearNum),
				StartDate: yearEnd.Add(-2 * time.Hour), EndDate: yearEnd, State: masterdata.PeriodOpen,
				Type: masterdata.FiscalPeriodTypeYearEndAdjustment,
			},
			masterdata.FiscalPeriod{
				ID: uuid.New(), CompanyID: companyID, FiscalYearID: fiscalYear.ID,
				Number: 14, Name: fmt.Sprintf("%d-Audit14", yearNum),
				StartDate: yearEnd.Add(-time.Hour), EndDate: yearEnd, State: masterdata.PeriodOpen,
				Type: masterdata.FiscalPeriodTypeAuditAdjustment,
			},
		)
		if err := driver.CreateFiscalYear(ctx, fiscalYear); err != nil {
			t.Fatalf("CreateFiscalYear(FY%d): %v", yearNum, err)
		}
		postingFiscalYear = fiscalYear
		if previousFiscalYearID != uuid.Nil {
			closedYear, err := driver.GetFiscalYearForCompany(ctx, companyID, previousFiscalYearID)
			if err != nil {
				t.Fatalf("GetFiscalYearForCompany previous year: %v", err)
			}
			if err := reporting.NewClosingEngine(driver).CarryForwardBalances(
				ctx, closedYear, fiscalYear.Periods[0].ID,
			); err != nil {
				t.Fatalf("CarryForwardBalances FY%d: %v", yearNum, err)
			}
		}
		if yearIndex == 0 {
			capMoney, _ := financialtypes.NewMoneyFromString("500000", "SAR")
			assetMoney, _ := financialtypes.NewMoneyFromString("100000", "SAR")
			initDate := time.Date(2022, 1, 1, 10, 0, 0, 0, time.UTC)
			postEntry(initDate, []posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1000"], Debit: capMoney, Credit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["3000"], Credit: capMoney, Debit: zero},
			}, "INIT-CAPITAL")
			postEntry(initDate.Add(time.Hour), []posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1500"], Debit: assetMoney, Credit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1000"], Credit: assetMoney, Debit: zero},
			}, "ASSET-PURCHASE")
		}

		// Monthly postings (Months 1-12)
		for m := 1; m <= 12; m++ {
			entryDate := time.Date(yearNum, time.Month(m), 15, 12, 0, 0, 0, time.UTC)
			// Sales
			postEntry(entryDate, []posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1200"], Debit: salesPerMonth, Credit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["4000"], Credit: salesPerMonth, Debit: zero},
			}, fmt.Sprintf("SALE-%d-%02d", yearNum, m))

			// Collection
			postEntry(entryDate.Add(time.Hour), []posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1000"], Debit: collPerMonth, Credit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1200"], Credit: collPerMonth, Debit: zero},
			}, fmt.Sprintf("COLL-%d-%02d", yearNum, m))

			// OpEx
			postEntry(entryDate.Add(2*time.Hour), []posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["5000"], Debit: opexPerMonth, Credit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1000"], Credit: opexPerMonth, Debit: zero},
			}, fmt.Sprintf("OPEX-%d-%02d", yearNum, m))
		}

		// Period 13 Adjustment (Year-end Depreciation: 5,000 SAR)
		p13Date := yearEnd.Add(-90 * time.Minute)
		postEntry(p13Date, []posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["5500"], Debit: deprPeriod13, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["1590"], Credit: deprPeriod13, Debit: zero},
		}, fmt.Sprintf("DEPR-ADJ-%d-P13", yearNum))

		// Period 14 Audit Adjustment (Accrued Audit Expense: 1,000 SAR)
		p14Date := yearEnd.Add(-30 * time.Minute)
		postEntry(p14Date, []posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["5800"], Debit: auditPeriod14, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: accounts["2000"], Credit: auditPeriod14, Debit: zero},
		}, fmt.Sprintf("AUDIT-ADJ-%d-P14", yearNum))

		// Verify Trial Balance before closing
		tbEngine := reporting.NewTrialBalanceEngine(driver)
		tb, err := tbEngine.GenerateForCompany(companyID, yearStart, yearEnd, "SAR")
		if err != nil {
			t.Fatalf("TrialBalance FY%d: %v", yearNum, err)
		}
		if !tb.Balanced || !tb.DebitTotal.Equal(tb.CreditTotal) {
			t.Fatalf("Unbalanced TB FY%d: debits=%s credits=%s", yearNum, tb.DebitTotal, tb.CreditTotal)
		}

		// Verify Income Statement for the year
		incEngine := reporting.NewIncomeStatementEngine(driver)
		inc, err := incEngine.GenerateForCompany(companyID, yearStart, yearEnd, "SAR")
		if err != nil {
			t.Fatalf("IncomeStatement FY%d: %v", yearNum, err)
		}
		expectedRevenue := decimal.NewFromInt(20000 * 12)                                     // 240,000
		expectedOpEx := decimal.NewFromInt(10000 * 12)                                        // 120,000
		expectedDepr := decimal.NewFromInt(5000)                                              // 5,000
		expectedAudit := decimal.NewFromInt(1000)                                             // 1,000
		expectedNet := expectedRevenue.Sub(expectedOpEx).Sub(expectedDepr).Sub(expectedAudit) // 114,000
		if !inc.NetProfit.Equal(expectedNet) {
			t.Fatalf("NetProfit FY%d = %s, want %s", yearNum, inc.NetProfit, expectedNet)
		}

		// Close monthly periods 1-12 to prepare for closing move
		for _, p := range fiscalYear.Periods {
			if p.Number >= 1 && p.Number <= 12 {
				_ = driver.UpdatePeriodState(ctx, companyID, p.ID, masterdata.PeriodSoftClosed)
				_ = driver.UpdatePeriodState(ctx, companyID, p.ID, masterdata.PeriodHardClosed)
			}
		}
		currentYear, err := driver.GetFiscalYearForCompany(ctx, companyID, fiscalYear.ID)
		if err != nil {
			t.Fatalf("GetFiscalYearForCompany FY%d: %v", yearNum, err)
		}

		// Generate and Post Closing Move
		closingEngine := reporting.NewClosingEngine(driver)
		closeEntry, _, err := closingEngine.GenerateClosingMove(currentYear, journalID, accounts["3200"], "SAR")
		if err != nil {
			t.Fatalf("GenerateClosingMove FY%d: %v", yearNum, err)
		}
		for _, period := range fiscalYear.Periods {
			if period.Number == 13 {
				closeEntry.PeriodID = period.ID
				break
			}
		}
		if err := driver.SaveDraft(closeEntry); err != nil {
			t.Fatalf("SaveDraft(CloseEntry FY%d): %v", yearNum, err)
		}
		closeEntry.Status = posting.EntryPosted
		if _, err := driver.PostAndSequence(closeEntry); err != nil {
			t.Fatalf("Post ClosingMove FY%d: %v", yearNum, err)
		}
		if err := closingEngine.ConfirmFiscalYearCloseContext(ctx, currentYear, closeEntry.ID); err != nil {
			t.Fatalf("ConfirmFiscalYearClose FY%d: %v", yearNum, err)
		}
		previousFiscalYearID = fiscalYear.ID

		// Verify Balance Sheet after closing
		bsEngine := reporting.NewBalanceSheetEngine(driver)
		bs, err := bsEngine.GenerateForCompany(companyID, yearEnd, "SAR")
		if err != nil {
			t.Fatalf("BalanceSheet FY%d: %v", yearNum, err)
		}
		if !bs.Balanced {
			t.Fatalf("BalanceSheet FY%d is unbalanced: assets=%s liabilities=%s equity=%s total_l_and_e=%s",
				yearNum, bs.TotalAssets, bs.TotalLiabilities, bs.Equity, bs.TotalLiabilitiesAndEquity)
		}
	}
}
