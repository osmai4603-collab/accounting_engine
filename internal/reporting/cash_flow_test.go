package reporting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

type cashFlowTestRepository struct {
	Repository
	accounts []masterdata.Account
	entries  []posting.JournalEntry
}

func (r *cashFlowTestRepository) ListAccounts(context.Context) ([]masterdata.Account, error) {
	return r.accounts, nil
}

func (r *cashFlowTestRepository) ListPostedEntries(_ context.Context, start, end time.Time) ([]posting.JournalEntry, error) {
	entries := make([]posting.JournalEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		date := entryEffectiveDate(entry)
		if !start.IsZero() && date.Before(start) || !end.IsZero() && date.After(end) {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func cashFlowTestMoney(t *testing.T, value string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(value, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func cashFlowTestAccount(id uuid.UUID, code, name string, accountType masterdata.AccountType, subClass string) masterdata.Account {
	account := masterdata.Account{
		ID: id, Code: code, Name: name, AccountType: accountType,
		NormalBalance: masterdata.DefaultNormalBalance(accountType), SubClassification: subClass,
		IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
	}
	switch accountType {
	case masterdata.AccountTypeAsset:
		if subClass == masterdata.SubClassCashAndEquivalents {
			account.CashFlowActivity = masterdata.CashFlowCashEquivalent
			account.IsCashEquivalent = true
		} else if subClass == masterdata.SubClassPropertyPlantEquipment {
			account.CashFlowActivity = masterdata.CashFlowInvesting
		} else {
			account.CashFlowActivity = masterdata.CashFlowOperating
		}
	case masterdata.AccountTypeLiability:
		account.CashFlowActivity = masterdata.CashFlowFinancing
	case masterdata.AccountTypeEquity:
		account.CashFlowActivity = masterdata.CashFlowFinancing
	case masterdata.AccountTypeIncome:
		account.IFRSCategory = masterdata.IFRSCategoryOperating
		account.CashFlowActivity = masterdata.CashFlowOperating
		if subClass == masterdata.SubClassFinanceIncome {
			account.IFRSCategory = masterdata.IFRSCategoryInvesting
		}
	case masterdata.AccountTypeExpense:
		account.IFRSCategory = masterdata.IFRSCategoryOperating
		account.CashFlowActivity = masterdata.CashFlowOperating
		if subClass == masterdata.SubClassFinanceCost {
			account.IFRSCategory = masterdata.IFRSCategoryFinancing
		}
	}
	return account
}

func cashFlowTestEntry(t *testing.T, date time.Time, lines ...posting.JournalEntryLine) posting.JournalEntry {
	t.Helper()
	for i := range lines {
		lines[i].ID = uuid.New()
	}
	entry, err := posting.NewJournalEntry(uuid.New(), date, "test", lines)
	if err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	return entry
}

func TestDirectCashFlowUsesExplicitCashLineClassifications(t *testing.T) {
	cashID, capitalID, revenueID, assetID, loanID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repository := &cashFlowTestRepository{accounts: []masterdata.Account{
		cashFlowTestAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		cashFlowTestAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		cashFlowTestAccount(revenueID, "4000", "Sales", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue),
		cashFlowTestAccount(assetID, "1500", "Equipment", masterdata.AccountTypeAsset, masterdata.SubClassPropertyPlantEquipment),
		cashFlowTestAccount(loanID, "2200", "Borrowings", masterdata.AccountTypeLiability, masterdata.SubClassLongTermBorrowings),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	opening := cashFlowTestEntry(t, start.Add(-time.Hour),
		posting.JournalEntryLine{AccountID: cashID, Debit: cashFlowTestMoney(t, "500")},
		posting.JournalEntryLine{AccountID: capitalID, Credit: cashFlowTestMoney(t, "500")},
	)
	receipt := cashFlowTestEntry(t, start,
		posting.JournalEntryLine{AccountID: cashID, Debit: cashFlowTestMoney(t, "100")},
		posting.JournalEntryLine{AccountID: revenueID, Credit: cashFlowTestMoney(t, "100")},
	)
	vendorPayment := cashFlowTestEntry(t, start.AddDate(0, 1, 0),
		posting.JournalEntryLine{AccountID: cashID, Credit: cashFlowTestMoney(t, "30")},
		posting.JournalEntryLine{AccountID: assetID, Debit: cashFlowTestMoney(t, "30")},
	)
	investingPayment := cashFlowTestEntry(t, start.AddDate(0, 2, 0),
		posting.JournalEntryLine{AccountID: cashID, Credit: cashFlowTestMoney(t, "50")},
		posting.JournalEntryLine{AccountID: assetID, Debit: cashFlowTestMoney(t, "50")},
	)
	borrowingReceipt := cashFlowTestEntry(t, start.AddDate(0, 3, 0),
		posting.JournalEntryLine{AccountID: cashID, Debit: cashFlowTestMoney(t, "100")},
		posting.JournalEntryLine{AccountID: loanID, Credit: cashFlowTestMoney(t, "100")},
	)
	repository.entries = []posting.JournalEntry{opening, receipt, vendorPayment, investingPayment, borrowingReceipt}

	engine := NewCashFlowEngine(repository)
	_, err := engine.GenerateDirect(start, end, "USD")
	if !errors.Is(err, ErrCashFlowTransactionClassificationRequired) {
		t.Fatalf("GenerateDirect error = %v, want explicit transaction classification", err)
	}
	classifications := map[uuid.UUID]CashFlowTransactionClassification{
		receipt.Lines[0].ID:          {Activity: CashFlowActivityOperating, Code: "CUSTOMER_RECEIPTS", Label: "Customer receipts"},
		vendorPayment.Lines[0].ID:    {Activity: CashFlowActivityOperating, Code: "SUPPLIER_PAYMENTS", Label: "Supplier payments"},
		investingPayment.Lines[0].ID: {Activity: CashFlowActivityInvesting, Code: "PPE_PURCHASES", Label: "Purchase of equipment"},
		borrowingReceipt.Lines[0].ID: {Activity: CashFlowActivityFinancing, Code: "BORROWINGS", Label: "Borrowings received"},
	}
	report, err := engine.GenerateWithRequest(CashFlowRequest{
		StartDate: start, EndDate: end, Currency: "USD",
		Method: CashFlowMethodDirect, Profile: CashFlowProfileIAS7,
		Transactions: classifications,
		NonCashTransactions: []NonCashTransaction{{
			Code: "LEASE", Description: "Lease liability recognized on commencement",
			Amount: decimal.NewFromInt(70), Currency: "USD",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Method != CashFlowMethodDirect || !report.Reconciled ||
		!report.OpeningCash.Equal(decimal.NewFromInt(500)) ||
		!report.ClosingCash.Equal(decimal.NewFromInt(620)) ||
		!report.OperatingCashFlow.Equal(decimal.NewFromInt(70)) ||
		!report.InvestingCashFlow.Equal(decimal.NewFromInt(-50)) ||
		!report.FinancingCashFlow.Equal(decimal.NewFromInt(100)) ||
		!report.NetChangeInCash.Equal(decimal.NewFromInt(120)) ||
		len(report.NonCashTransactions) != 1 || report.OperatingActivities.Total.String() != "70" {
		t.Fatalf("direct cash-flow statement is incorrect: %+v", report)
	}
}

func TestCashFlowProfilesChooseDifferentIndirectStartingPoint(t *testing.T) {
	cashID, capitalID, investmentIncomeID, financeCostID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repository := &cashFlowTestRepository{accounts: []masterdata.Account{
		cashFlowTestAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents),
		cashFlowTestAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital),
		cashFlowTestAccount(investmentIncomeID, "4100", "Investment income", masterdata.AccountTypeIncome, masterdata.SubClassFinanceIncome),
		cashFlowTestAccount(financeCostID, "5100", "Finance cost", masterdata.AccountTypeExpense, masterdata.SubClassFinanceCost),
	}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	repository.entries = []posting.JournalEntry{
		cashFlowTestEntry(t, start.AddDate(0, 0, 1),
			posting.JournalEntryLine{AccountID: cashID, Debit: cashFlowTestMoney(t, "200")},
			posting.JournalEntryLine{AccountID: capitalID, Credit: cashFlowTestMoney(t, "200")},
		),
		cashFlowTestEntry(t, start.AddDate(0, 0, 2),
			posting.JournalEntryLine{AccountID: cashID, Debit: cashFlowTestMoney(t, "50")},
			posting.JournalEntryLine{AccountID: investmentIncomeID, Credit: cashFlowTestMoney(t, "50")},
		),
		cashFlowTestEntry(t, start.AddDate(0, 0, 3),
			posting.JournalEntryLine{AccountID: financeCostID, Debit: cashFlowTestMoney(t, "20")},
			posting.JournalEntryLine{AccountID: cashID, Credit: cashFlowTestMoney(t, "20")},
		),
	}
	engine := NewCashFlowEngine(repository)
	ias7, err := engine.GenerateWithRequest(CashFlowRequest{
		StartDate: start, EndDate: end, Currency: "USD",
		Method: CashFlowMethodIndirect, Profile: CashFlowProfileIAS7,
	})
	if err != nil {
		t.Fatalf("IAS 7 report = %+v, error = %v", ias7, err)
	}
	if !ias7.StartingProfit.Equal(decimal.NewFromInt(30)) || !ias7.Reconciled {
		t.Fatalf("IAS 7 profile did not start from net profit: %+v", ias7)
	}
	ifrs18, err := engine.GenerateWithRequest(CashFlowRequest{
		StartDate: start, EndDate: end, Currency: "USD",
		Method: CashFlowMethodIndirect, Profile: CashFlowProfileIFRS18,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ifrs18.StartingProfit.IsZero() || !ifrs18.Reconciled ||
		!ifrs18.InvestingCashFlow.Equal(decimal.NewFromInt(50)) ||
		!ifrs18.FinancingCashFlow.Equal(decimal.NewFromInt(180)) {
		t.Fatalf("IFRS 18 profile did not start from operating profit: %+v", ifrs18)
	}
}

func TestCashFlowCashPolicyCanIncludeDesignatedOverdraft(t *testing.T) {
	cashID, overdraftID, capitalID := uuid.New(), uuid.New(), uuid.New()
	cash := cashFlowTestAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents)
	overdraft := cashFlowTestAccount(overdraftID, "2100", "Demand overdraft", masterdata.AccountTypeLiability, masterdata.SubClassShortTermBorrowings)
	capital := cashFlowTestAccount(capitalID, "3000", "Capital", masterdata.AccountTypeEquity, masterdata.SubClassShareCapital)
	repository := &cashFlowTestRepository{accounts: []masterdata.Account{cash, overdraft, capital}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	repository.entries = []posting.JournalEntry{cashFlowTestEntry(t, start.Add(-time.Hour),
		posting.JournalEntryLine{AccountID: cashID, Debit: cashFlowTestMoney(t, "200")},
		posting.JournalEntryLine{AccountID: capitalID, Credit: cashFlowTestMoney(t, "180")},
		posting.JournalEntryLine{AccountID: overdraftID, Credit: cashFlowTestMoney(t, "20")},
	)}
	report, err := NewCashFlowEngine(repository).GenerateWithRequest(CashFlowRequest{
		StartDate: start, EndDate: end, Currency: "USD", Method: CashFlowMethodDirect,
		Policy: CashFlowPolicy{CashEquivalentAccountIDs: []uuid.UUID{cashID, overdraftID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OpeningCash.Equal(decimal.NewFromInt(180)) ||
		!report.ClosingCash.Equal(decimal.NewFromInt(180)) || !report.Reconciled {
		t.Fatalf("designated cash overdraft was not netted with cash: %+v", report)
	}
}

func TestCashFlowRequestValidatesNonCashDisclosuresAndDefaultsProfile(t *testing.T) {
	request, err := normalizeCashFlowRequest(CashFlowRequest{
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		Currency:  " usd ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Method != CashFlowMethodIndirect || request.Profile != CashFlowProfileIAS7 || request.Currency != "USD" {
		t.Fatalf("cash-flow defaults were not normalized: %+v", request)
	}

	for _, disclosure := range []NonCashTransaction{
		{Code: "LEASE", Description: "Lease liability", Amount: decimal.Zero, Currency: "USD"},
		{Code: "LEASE", Description: "Lease liability", Amount: decimal.NewFromInt(1), Currency: "EUR"},
		{Description: "Lease liability", Amount: decimal.NewFromInt(1), Currency: "USD"},
	} {
		_, err := normalizeCashFlowRequest(CashFlowRequest{
			StartDate: request.StartDate, EndDate: request.EndDate, Currency: "USD",
			NonCashTransactions: []NonCashTransaction{disclosure},
		})
		if !errors.Is(err, ErrInvalidCashFlowRequest) {
			t.Errorf("invalid non-cash disclosure %+v returned error %v", disclosure, err)
		}
	}
}

func TestCashFlowTranslationDetectionOnlyUsesCashEquivalentAccounts(t *testing.T) {
	cashID, revenueID, receivableID := uuid.New(), uuid.New(), uuid.New()
	cash := cashFlowTestAccount(cashID, "1000", "Cash", masterdata.AccountTypeAsset, masterdata.SubClassCashAndEquivalents)
	revenue := cashFlowTestAccount(revenueID, "4000", "Sales", masterdata.AccountTypeIncome, masterdata.SubClassOperatingRevenue)
	receivable := cashFlowTestAccount(receivableID, "1200", "Receivables", masterdata.AccountTypeAsset, masterdata.SubClassTradeReceivables)
	foreignAmount, err := financialtypes.NewMoneyFromString("100", "SAR")
	if err != nil {
		t.Fatal(err)
	}
	repository := &cashFlowTestRepository{
		accounts: []masterdata.Account{cash, revenue, receivable},
		entries: []posting.JournalEntry{cashFlowTestEntry(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			posting.JournalEntryLine{AccountID: receivableID, Debit: foreignAmount},
			posting.JournalEntryLine{AccountID: revenueID, Credit: foreignAmount},
		)},
	}
	cashAccounts := map[uuid.UUID]masterdata.Account{cashID: cash}
	translated, err := cashAccountsRequireTranslation(repository, cashAccounts, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if translated {
		t.Fatal("foreign-currency non-cash activity was treated as a cash translation effect")
	}

	repository.entries = append(repository.entries, cashFlowTestEntry(t, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		posting.JournalEntryLine{AccountID: cashID, Debit: foreignAmount},
		posting.JournalEntryLine{AccountID: revenueID, Credit: foreignAmount},
	))
	translated, err = cashAccountsRequireTranslation(repository, cashAccounts, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !translated {
		t.Fatal("foreign-currency cash balance was not identified for translation")
	}
}
