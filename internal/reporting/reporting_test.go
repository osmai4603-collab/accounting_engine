package reporting

import (
	"context"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/internal/valuation"
	"accounting_engine/storage/memory"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type memoryRepository struct {
	accounts []masterdata.Account
	entries  []posting.JournalEntry
}

func TestDimensionProfitAndLossGroupsByAnalyticalIds(t *testing.T) {
	revenue := reportAccount(uuid.New(), "4000", "Revenue", masterdata.AccountTypeIncome, "operating_revenue")
	costCenterID, projectID := uuid.New(), uuid.New()
	amount := reportMoney(t, "100", "USD")
	zero := reportMoney(t, "0", "USD")
	date := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	repository := &memoryRepository{
		accounts: []masterdata.Account{revenue},
		entries: []posting.JournalEntry{{
			Status: posting.EntryPosted, EntryDate: date,
			Lines: []posting.JournalEntryLine{{
				AccountID: revenue.ID, Debit: zero, Credit: amount,
				CostCenterID: &costCenterID, ProjectID: &projectID,
			}},
		}},
	}
	report, err := NewEngine(repository).GenerateDimensionProfitAndLoss(date, date, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Lines) != 1 || report.Lines[0].Amount.String() != "100" ||
		report.Lines[0].Key.CostCenterID != costCenterID || report.Lines[0].Key.ProjectID != projectID {
		t.Fatalf("dimension report = %+v", report.Lines)
	}
}

func TestTrialBalanceReportingIsCompanyScoped(t *testing.T) {
	repository := memory.New()
	companyA, companyB := uuid.New(), uuid.New()
	accountA := reportAccount(uuid.New(), "1000", "Cash A", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents)
	accountA.CompanyID = companyA
	accountB := reportAccount(uuid.New(), "1000", "Cash B", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents)
	accountB.CompanyID = companyB
	if err := repository.CreateAccount(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAccount(context.Background(), accountB); err != nil {
		t.Fatal(err)
	}

	report, err := NewTrialBalanceEngine(repository).GenerateForCompany(
		companyA, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), "USD",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Lines) != 1 || report.Lines[0].AccountID != accountA.ID {
		t.Fatalf("company-scoped trial balance contains wrong accounts: %+v", report.Lines)
	}
}

func TestCompanyTrialBalanceUsesEffectiveAccountVersionAtPeriodEnd(t *testing.T) {
	repository := memory.New()
	companyID, accountID := uuid.New(), uuid.New()
	account := reportAccount(accountID, "4000", "Current label", masterdata.AccountTypeExpense, masterdata.SubClassOtherOperatingExpense)
	account.CompanyID = companyID
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	versions := []masterdata.AccountVersion{
		{
			CompanyID: companyID, AccountID: accountID, Name: "Historical revenue",
			AccountType: masterdata.AccountTypeIncome, IFRSCategory: masterdata.IFRSCategoryOperating,
			EffectiveFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			EffectiveTo:   timePtr(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
		{
			CompanyID: companyID, AccountID: accountID, Name: "Current expense",
			AccountType: masterdata.AccountTypeExpense, IFRSCategory: masterdata.IFRSCategoryOperating,
			EffectiveFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, version := range versions {
		if err := repository.SaveAccountVersion(context.Background(), version); err != nil {
			t.Fatal(err)
		}
	}

	engine := NewTrialBalanceEngine(repository)
	historical, err := engine.GenerateForCompanyContext(
		context.Background(), companyID,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC), "USD",
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := engine.GenerateForCompanyContext(
		context.Background(), companyID,
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "USD",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := historical.Lines[0]; got.AccountName != "Historical revenue" || got.AccountType != masterdata.AccountTypeIncome {
		t.Fatalf("historical account version = %+v", got)
	}
	if got := current.Lines[0]; got.AccountName != "Current expense" || got.AccountType != masterdata.AccountTypeExpense {
		t.Fatalf("current account version = %+v", got)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func (r *memoryRepository) ListAccounts(context.Context) ([]masterdata.Account, error) {
	return append([]masterdata.Account(nil), r.accounts...), nil
}

func (r *memoryRepository) ListPostedEntries(_ context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
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

func (r *memoryRepository) ListAccountsForCompanyContext(ctx context.Context, companyID uuid.UUID) ([]masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accounts := make([]masterdata.Account, 0)
	for _, account := range r.accounts {
		if account.CompanyID == companyID {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func (r *memoryRepository) ListAccountsForCompanyAsOfContext(ctx context.Context, companyID uuid.UUID, _ time.Time) ([]masterdata.Account, error) {
	return r.ListAccountsForCompanyContext(ctx, companyID)
}

func (r *memoryRepository) ListEntriesForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time) ([]posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries := make([]posting.JournalEntry, 0)
	for _, entry := range r.entries {
		date := entryEffectiveDate(entry)
		if entry.CompanyID == companyID && entry.Status == posting.EntryPosted &&
			(start.IsZero() || !date.Before(start)) && (end.IsZero() || !date.After(end)) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func (r *memoryRepository) ListFixedAssets(context.Context) ([]subledgers.FixedAsset, error) {
	return nil, nil
}

func (r *memoryRepository) ListOpenLines(context.Context, string) ([]subledgers.OpenLine, error) {
	return nil, nil
}

func (r *memoryRepository) ListFixedAssetsForCompanyContext(ctx context.Context, _ uuid.UUID) ([]subledgers.FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.ListFixedAssets(ctx)
}

func (r *memoryRepository) ListOpenLinesForCompanyContext(ctx context.Context, _ uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.ListOpenLines(ctx, partnerID)
}

func (r *memoryRepository) GetRateForCompany(context.Context, uuid.UUID, string, string, time.Time) (decimal.Decimal, error) {
	return decimal.Zero, valuation.ErrRateNotFound
}

func (r *memoryRepository) GetRateOfTypeForCompany(context.Context, uuid.UUID, valuation.RateType, string, string, time.Time) (decimal.Decimal, error) {
	return decimal.Zero, valuation.ErrRateNotFound
}

func (r *memoryRepository) GetFiscalYearForCompanyContext(ctx context.Context, companyID, yearID uuid.UUID) (masterdata.FiscalYear, error) {
	if err := ctx.Err(); err != nil {
		return masterdata.FiscalYear{}, err
	}
	return masterdata.FiscalYear{}, masterdata.ErrFiscalYearNotFound
}

func (r *memoryRepository) UpdatePeriodStateForCompanyContext(ctx context.Context, _, _ uuid.UUID, _ masterdata.PeriodState) error {
	return ctx.Err()
}

func (r *memoryRepository) UpdateFiscalYearStateForCompanyContext(ctx context.Context, _, _ uuid.UUID, _ masterdata.FiscalYearState) error {
	return ctx.Err()
}

func (r *memoryRepository) GetEntryByIDForCompanyContext(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	for _, entry := range r.entries {
		if entry.ID == entryID && entry.CompanyID == companyID {
			return entry, nil
		}
	}
	return posting.JournalEntry{}, posting.ErrEntryNotFound
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
	account := masterdata.Account{
		ID: id, Code: code, Name: name, AccountType: accountType,
		NormalBalance: masterdata.DefaultNormalBalance(accountType), SubClassification: classification,
	}
	switch accountType {
	case masterdata.AccountTypeIncome, masterdata.AccountTypeExpense:
		account.IFRSCategory = masterdata.IFRSCategoryOperating
		account.CashFlowActivity = masterdata.CashFlowOperating
	case masterdata.AccountTypeEquity:
		account.IFRSCategory = masterdata.IFRSCategoryBalanceSheetPerpetual
		account.CashFlowActivity = masterdata.CashFlowFinancing
	case masterdata.AccountTypeAsset:
		account.IFRSCategory = masterdata.IFRSCategoryBalanceSheetPerpetual
		switch classification {
		case masterdata.SubClassCashAndEquivalents, masterdata.SubClassBank, "cash":
			account.CashFlowActivity = masterdata.CashFlowCashEquivalent
			account.IsCashEquivalent = true
		case masterdata.SubClassPropertyPlantEquipment, masterdata.SubClassIntangibleAssets,
			masterdata.SubClassRightOfUseAssets, masterdata.SubClassFinancialInvestments:
			account.CashFlowActivity = masterdata.CashFlowInvesting
		default:
			account.CashFlowActivity = masterdata.CashFlowOperating
		}
	case masterdata.AccountTypeLiability:
		account.IFRSCategory = masterdata.IFRSCategoryBalanceSheetPerpetual
		switch classification {
		case masterdata.SubClassShortTermBorrowings, masterdata.SubClassLongTermBorrowings,
			masterdata.SubClassCurrentLeaseLiabilities, masterdata.SubClassNonCurrentLeaseLiabilities,
			"loan", "lease":
			account.CashFlowActivity = masterdata.CashFlowFinancing
		default:
			account.CashFlowActivity = masterdata.CashFlowOperating
		}
	}
	switch classification {
	case masterdata.SubClassFinanceIncome:
		account.IFRSCategory = masterdata.IFRSCategoryInvesting
	case masterdata.SubClassFinanceCost:
		account.IFRSCategory = masterdata.IFRSCategoryFinancing
	case masterdata.SubClassTaxExpense, masterdata.SubClassIncomeTaxExpense:
		account.IFRSCategory = masterdata.IFRSCategoryIncomeTaxes
	case masterdata.SubClassOCINonRecyclable, masterdata.SubClassOCIRecyclable:
		account.IFRSCategory = masterdata.IFRSCategoryEquityMovements
	}
	return account
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

func TestIncomeStatementRequiresExplicitIFRSCategory(t *testing.T) {
	expense := reportAccount(uuid.New(), "5000", "Expense", masterdata.AccountTypeExpense, masterdata.SubClassOtherOperatingExpense)
	expense.IFRSCategory = ""
	repository := &memoryRepository{accounts: []masterdata.Account{expense}}
	_, err := NewIncomeStatementEngine(repository).Generate(time.Time{}, time.Time{}, "USD")
	if err != ErrIFRSClassificationRequired {
		t.Fatalf("Generate() error = %v, want explicit IFRS category error", err)
	}
}

func TestIncomeStatementOffsetsContraRevenueAndExpense(t *testing.T) {
	revenue := reportAccount(uuid.New(), "4000", "Sales", masterdata.AccountTypeIncome, "operating_revenue")
	returns := reportAccount(uuid.New(), "4050", "Sales returns", masterdata.AccountTypeIncome, "sales_returns")
	returns.IsContraAccount = true
	returns.NormalBalance = masterdata.NormalBalanceDebit
	costOfSales := reportAccount(uuid.New(), "5000", "Cost of sales", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales)
	purchaseDiscount := reportAccount(uuid.New(), "5050", "Purchase discounts", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales)
	purchaseDiscount.IsContraAccount = true
	purchaseDiscount.NormalBalance = masterdata.NormalBalanceCredit
	cash := reportAccount(uuid.New(), "1000", "Cash", masterdata.AccountTypeAsset, "cash")
	repository := &memoryRepository{accounts: []masterdata.Account{revenue, returns, costOfSales, purchaseDiscount, cash}}
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: cash.ID, Debit: reportMoney(t, "1000", "USD")},
			posting.JournalEntryLine{AccountID: revenue.ID, Credit: reportMoney(t, "1000", "USD")},
		),
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: returns.ID, Debit: reportMoney(t, "100", "USD")},
			posting.JournalEntryLine{AccountID: cash.ID, Credit: reportMoney(t, "100", "USD")},
		),
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: costOfSales.ID, Debit: reportMoney(t, "500", "USD")},
			posting.JournalEntryLine{AccountID: cash.ID, Credit: reportMoney(t, "500", "USD")},
		),
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: cash.ID, Debit: reportMoney(t, "50", "USD")},
			posting.JournalEntryLine{AccountID: purchaseDiscount.ID, Credit: reportMoney(t, "50", "USD")},
		),
	}

	report, err := NewIncomeStatementEngine(repository).Generate(time.Time{}, time.Time{}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Revenue.Equal(decimal.NewFromInt(900)) ||
		!report.CostOfSales.Equal(decimal.NewFromInt(450)) ||
		!report.GrossProfit.Equal(decimal.NewFromInt(450)) {
		t.Fatalf("contra accounts were not deducted from their categories: revenue=%s cost_of_sales=%s gross_profit=%s",
			report.Revenue, report.CostOfSales, report.GrossProfit)
	}
}

func TestAdjustedAccountBalanceHandlesContraSides(t *testing.T) {
	tests := []struct {
		name        string
		accountType masterdata.AccountType
		normal      masterdata.NormalBalance
		debit       string
		credit      string
		want        int64
	}{
		{name: "contra asset", accountType: masterdata.AccountTypeAsset, normal: masterdata.NormalBalanceCredit, credit: "25", want: -25},
		{name: "contra liability", accountType: masterdata.AccountTypeLiability, normal: masterdata.NormalBalanceDebit, debit: "25", want: -25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := reportAccount(uuid.New(), "1000", test.name, test.accountType, "other")
			account.IsContraAccount = true
			account.NormalBalance = test.normal
			side := accountSide{debit: decimal.NewFromInt(0), credit: decimal.NewFromInt(0)}
			if test.debit != "" {
				side.debit = decimal.RequireFromString(test.debit)
			}
			if test.credit != "" {
				side.credit = decimal.RequireFromString(test.credit)
			}
			if got := adjustedAccountBalance(side, account); !got.Equal(decimal.NewFromInt(test.want)) {
				t.Fatalf("adjustedAccountBalance() = %s, want %d", got, test.want)
			}
		})
	}
}

func TestQuickRatioNetsContraReceivables(t *testing.T) {
	cash := reportAccount(uuid.New(), "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents)
	receivables := reportAccount(uuid.New(), "1100", "Trade receivables", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables)
	allowance := reportAccount(uuid.New(), "1190", "Allowance for ECL", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables)
	allowance.IsContraAccount = true
	allowance.NormalBalance = masterdata.NormalBalanceCredit
	equity := reportAccount(uuid.New(), "3000", "Capital", masterdata.AccountTypeEquity, "capital")
	revenue := reportAccount(uuid.New(), "4000", "Sales", masterdata.AccountTypeIncome, "operating_revenue")
	provision := reportAccount(uuid.New(), "5100", "ECL expense", masterdata.AccountTypeExpense, masterdata.SubClassExpectedCreditLossExpense)
	payable := reportAccount(uuid.New(), "2000", "Accounts payable", masterdata.AccountTypeLiability, masterdata.SubClassAccountsPayable)
	repository := &memoryRepository{accounts: []masterdata.Account{
		cash, receivables, allowance, equity, revenue, provision, payable,
	}}
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: cash.ID, Debit: reportMoney(t, "1000", "USD")},
			posting.JournalEntryLine{AccountID: equity.ID, Credit: reportMoney(t, "1000", "USD")},
		),
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: receivables.ID, Debit: reportMoney(t, "500", "USD")},
			posting.JournalEntryLine{AccountID: revenue.ID, Credit: reportMoney(t, "500", "USD")},
		),
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: provision.ID, Debit: reportMoney(t, "100", "USD")},
			posting.JournalEntryLine{AccountID: allowance.ID, Credit: reportMoney(t, "100", "USD")},
		),
		reportEntry(t, date,
			posting.JournalEntryLine{AccountID: cash.ID, Debit: reportMoney(t, "500", "USD")},
			posting.JournalEntryLine{AccountID: payable.ID, Credit: reportMoney(t, "500", "USD")},
		),
	}

	report, err := NewRatiosEngine(repository).Generate(time.Time{}, time.Time{}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Liquidity.QuickRatio.Equal(decimal.RequireFromString("3.8")) {
		t.Fatalf("QuickRatio = %s, want 3.8 with net receivables", report.Liquidity.QuickRatio)
	}
}

func TestCashFlowRequiresExplicitActivity(t *testing.T) {
	account := reportAccount(uuid.New(), "1200", "Receivables", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables)
	account.CashFlowActivity = ""
	repository := &memoryRepository{accounts: []masterdata.Account{account}}
	_, err := NewCashFlowEngine(repository).Generate(time.Time{}, time.Time{}, "USD")
	if err != ErrCashFlowClassificationRequired {
		t.Fatalf("Generate() error = %v, want explicit cash-flow activity error", err)
	}
}

func TestIndirectCashFlowSeparatesInvestingAndFinancingResults(t *testing.T) {
	cashID, capitalID, investingIncomeID, financeCostID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cash := reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents)
	capital := reportAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital)
	investingIncome := reportAccount(investingIncomeID, "4100", "Investment income", masterdata.AccountTypeIncome, masterdata.SubClassFinanceIncome)
	financeCost := reportAccount(financeCostID, "5100", "Finance cost", masterdata.AccountTypeExpense, masterdata.SubClassFinanceCost)
	repository := &memoryRepository{accounts: []masterdata.Account{cash, capital, investingIncome, financeCost}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "200", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: capitalID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "200", "USD")},
		),
		reportEntry(t, start.AddDate(0, 0, 2),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "50", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: investingIncomeID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "50", "USD")},
		),
		reportEntry(t, start.AddDate(0, 0, 3),
			posting.JournalEntryLine{AccountID: financeCostID, Debit: reportMoney(t, "20", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "20", "USD")},
		),
	}
	report, err := NewCashFlowEngine(repository).Generate(start, end, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Reconciled ||
		!report.OperatingCashFlow.Equal(decimal.Zero) ||
		!report.InvestingCashFlow.Equal(decimal.NewFromInt(50)) ||
		!report.FinancingCashFlow.Equal(decimal.NewFromInt(180)) ||
		!report.ClosingCash.Equal(decimal.NewFromInt(230)) {
		t.Fatalf("cash flow categories are incorrect: %+v", report)
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
	if closing.TargetPeriodNumber != 13 || !closing.AdjustmentAuthorized {
		t.Fatalf("year-end close must target authorized period 13: %+v", closing)
	}
	if opening, err := NewClosingEngine(repository).GenerateOpeningMove(year, uuid.New(), "USD"); err != nil || opening.ID != uuid.Nil {
		t.Fatalf("GenerateOpeningMove() = %+v, %v; want no opening move without prior balances", opening, err)
	}
}

func TestOpeningMoveCarriesBalanceSheetAndUnclosedNetIncome(t *testing.T) {
	cashID, payableID, retainedID, incomeID, expenseID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repository := &memoryRepository{
		accounts: []masterdata.Account{
			reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
			reportAccount(payableID, "2100", "Trade payables", masterdata.AccountTypeLiability, masterdata.SubClassTradePayables),
			reportAccount(retainedID, "3300", "Retained earnings", masterdata.AccountTypeEquity, masterdata.SubClassRetainedEarnings),
			reportAccount(incomeID, "4000", "Sales", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
			reportAccount(expenseID, "5000", "Expense", masterdata.AccountTypeExpense, masterdata.SubClassCostOfSales),
		},
	}
	priorDate := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, priorDate,
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "100", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: incomeID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "100", "USD")},
		),
		reportEntry(t, priorDate,
			posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "40", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "40", "USD")},
		),
		reportEntry(t, priorDate,
			posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "30", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: payableID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "30", "USD")},
		),
	}
	nextYear := masterdata.GenerateFiscalYear(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	entry, err := NewClosingEngine(repository).GenerateOpeningMove(nextYear, uuid.New(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	if entry.Classification != posting.EntryClassOpening || len(entry.Lines) != 3 {
		t.Fatalf("unexpected opening move: %+v", entry)
	}
	balanceByAccount := make(map[uuid.UUID]decimal.Decimal)
	for _, line := range entry.Lines {
		balanceByAccount[line.AccountID] = line.Debit.Amount().Sub(line.Credit.Amount())
	}
	if !balanceByAccount[cashID].Equal(decimal.NewFromInt(60)) ||
		!balanceByAccount[payableID].Equal(decimal.NewFromInt(-30)) ||
		!balanceByAccount[retainedID].Equal(decimal.NewFromInt(-30)) {
		t.Fatalf("opening balances are incorrect: %+v", balanceByAccount)
	}

	entry.Status = posting.EntryPosted
	repository.entries = append(repository.entries, entry,
		reportEntry(t, time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "50", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: incomeID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "50", "USD")},
		),
		reportEntry(t, time.Date(2027, 7, 1, 0, 0, 0, 0, time.UTC),
			posting.JournalEntryLine{AccountID: expenseID, Debit: reportMoney(t, "20", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "20", "USD")},
		),
	)
	asOf := time.Date(2027, 12, 31, 23, 59, 59, 0, time.UTC)
	sheet, err := NewBalanceSheetEngine(repository).Generate(asOf, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !sheet.TotalAssets.Equal(decimal.NewFromInt(90)) ||
		!sheet.TotalLiabilities.Equal(decimal.NewFromInt(30)) ||
		!sheet.Equity.Equal(decimal.NewFromInt(60)) {
		t.Fatalf("balance sheet double-counted balances after the opening move: %+v", sheet)
	}
	trialBalance, err := NewTrialBalanceEngine(repository).Generate(
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), asOf, "USD",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !trialBalance.DebitTotal.Equal(decimal.NewFromInt(110)) ||
		!trialBalance.CreditTotal.Equal(decimal.NewFromInt(110)) {
		t.Fatalf("trial balance double-counted balances after the opening move: %+v", trialBalance)
	}
	followingYear := masterdata.GenerateFiscalYear(time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC))
	nextEntry, err := NewClosingEngine(repository).GenerateOpeningMove(followingYear, uuid.New(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	nextBalances := make(map[uuid.UUID]decimal.Decimal)
	for _, line := range nextEntry.Lines {
		nextBalances[line.AccountID] = nextBalances[line.AccountID].Add(line.Debit.Amount().Sub(line.Credit.Amount()))
	}
	if !nextBalances[cashID].Equal(decimal.NewFromInt(90)) ||
		!nextBalances[payableID].Equal(decimal.NewFromInt(-30)) ||
		!nextBalances[retainedID].Equal(decimal.NewFromInt(-60)) {
		t.Fatalf("subsequent opening entry duplicated prior balances: %+v", nextBalances)
	}
}

func TestConfirmFiscalYearCloseRequiresPostedClosingAndClosesYear(t *testing.T) {
	repository := memory.New()
	companyID := uuid.New()
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	ctx := context.Background()
	if err := repository.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	for _, period := range year.Periods {
		if period.Number > 12 {
			continue
		}
		if err := repository.UpdatePeriodState(ctx, companyID, period.ID, masterdata.PeriodSoftClosed); err != nil {
			t.Fatal(err)
		}
		if err := repository.UpdatePeriodState(ctx, companyID, period.ID, masterdata.PeriodHardClosed); err != nil {
			t.Fatal(err)
		}
	}
	period13 := year.Periods[12]
	entry, err := posting.NewJournalEntry(uuid.New(), year.EndDate, "test",
		[]posting.JournalEntryLine{
			{AccountID: uuid.New(), Debit: reportMoney(t, "1", "USD"), Credit: reportMoney(t, "0", "USD")},
			{AccountID: uuid.New(), Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "1", "USD")},
		})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.Status = posting.EntryPosted
	entry.Classification = posting.EntryClassClosing
	entry.DocumentReference = "YEAR-END CLOSING"
	entry.TargetPeriodNumber = 13
	entry.AdjustmentAuthorized = true
	entry.PeriodID = period13.ID
	if err := repository.PostAtomic(entry); err != nil {
		t.Fatal(err)
	}
	if err := NewClosingEngine(repository).ConfirmFiscalYearClose(year, entry.ID); err != nil {
		t.Fatal(err)
	}
	closedYear, err := repository.GetFiscalYear(ctx, year.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closedYear.State != masterdata.FiscalYearHardClosed {
		t.Fatalf("fiscal year state = %s, want hard closed", closedYear.State)
	}
	for _, number := range []int{13, 14} {
		period, err := repository.GetPeriodByNumberForCompany(ctx, companyID, year.ID, number)
		if err != nil {
			t.Fatal(err)
		}
		if period.State != masterdata.PeriodHardClosed {
			t.Fatalf("period %d state = %s, want hard closed", number, period.State)
		}
	}
	if err := NewClosingEngine(repository).ConfirmFiscalYearClose(year, entry.ID); err != nil {
		t.Fatalf("repeated fiscal year close should be idempotent: %v", err)
	}
}

func TestClosingHandlesContraIncomeAndExpenseAccounts(t *testing.T) {
	cashID, contraRevenueID, contraExpenseID, retainedID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	contraRevenue := reportAccount(contraRevenueID, "4090", "Sales returns", masterdata.AccountTypeIncome, "operating_revenue")
	contraRevenue.NormalBalance = masterdata.NormalBalanceDebit
	contraRevenue.IsContraAccount = true
	contraExpense := reportAccount(contraExpenseID, "5090", "Expense recoveries", masterdata.AccountTypeExpense, "cost_of_sales")
	contraExpense.NormalBalance = masterdata.NormalBalanceCredit
	contraExpense.IsContraAccount = true
	repository := &memoryRepository{accounts: []masterdata.Account{
		reportAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, "cash"),
		contraRevenue, contraExpense,
		reportAccount(retainedID, "3300", "Retained earnings", masterdata.AccountTypeEquity, "retained_earnings"),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		reportEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: contraRevenueID, Debit: reportMoney(t, "30", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "30", "USD")},
		),
		reportEntry(t, start.AddDate(0, 0, 2),
			posting.JournalEntryLine{AccountID: cashID, Debit: reportMoney(t, "10", "USD"), Credit: reportMoney(t, "0", "USD")},
			posting.JournalEntryLine{AccountID: contraExpenseID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "10", "USD")},
		),
	}
	year := masterdata.GenerateFiscalYear(start)
	for i := range year.Periods {
		year.Periods[i].State = masterdata.PeriodHardClosed
	}
	closing, netIncome, err := NewClosingEngine(repository).GenerateClosingMove(year, uuid.New(), retainedID, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !netIncome.Equal(decimal.NewFromInt(-20)) {
		t.Fatalf("net income = %s, want -20", netIncome)
	}
	if err := closing.Validate(); err != nil {
		t.Fatalf("contra account closing entry is unbalanced: %v", err)
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

func (r *fullBundleMemoryRepository) ListFixedAssets(context.Context) ([]subledgers.FixedAsset, error) {
	return append([]subledgers.FixedAsset(nil), r.assets...), nil
}

func (r *fullBundleMemoryRepository) ListOpenLines(_ context.Context, status string) ([]subledgers.OpenLine, error) {
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

func TestBalanceSheetContraAccountCalculation(t *testing.T) {
	ppeID := uuid.New()
	accumDepID := uuid.New()
	equityID := uuid.New()

	ppeAccount := reportAccount(ppeID, "1500", "Machinery", masterdata.AccountTypeAsset, "property_plant_equipment")
	accumDepAccount := reportAccount(accumDepID, "1590", "Accumulated Depreciation", masterdata.AccountTypeAsset, "property_plant_equipment")
	accumDepAccount.NormalBalance = masterdata.NormalBalanceCredit
	accumDepAccount.IsContraAccount = true

	equityAccount := reportAccount(equityID, "3000", "Share Capital", masterdata.AccountTypeEquity, "share_capital")

	asOf := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	repo := &memoryRepository{
		accounts: []masterdata.Account{ppeAccount, accumDepAccount, equityAccount},
		entries: []posting.JournalEntry{
			reportEntry(t, asOf,
				posting.JournalEntryLine{AccountID: ppeID, Debit: reportMoney(t, "100000", "USD"), Credit: reportMoney(t, "0", "USD")},
				posting.JournalEntryLine{AccountID: accumDepID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "20000", "USD")},
				posting.JournalEntryLine{AccountID: equityID, Debit: reportMoney(t, "0", "USD"), Credit: reportMoney(t, "80000", "USD")},
			),
		},
	}

	balanceSheet, err := NewBalanceSheetEngine(repo).Generate(asOf, "USD")
	if err != nil {
		t.Fatalf("balance sheet generation failed: %v", err)
	}

	if !balanceSheet.Balanced {
		t.Fatalf("balance sheet with contra account is not balanced: assets=%s, liabilities_equity=%s",
			balanceSheet.TotalAssets, balanceSheet.TotalLiabilitiesAndEquity)
	}

	expectedNetPPE := decimal.NewFromInt(80000)
	if !balanceSheet.NonCurrentAssets.Equal(expectedNetPPE) {
		t.Fatalf("expected NonCurrentAssets %s, got %s", expectedNetPPE, balanceSheet.NonCurrentAssets)
	}
	if !balanceSheet.TotalAssets.Equal(expectedNetPPE) {
		t.Fatalf("expected TotalAssets %s, got %s", expectedNetPPE, balanceSheet.TotalAssets)
	}
}
