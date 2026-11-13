package ifrs15

import (
	"context"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/shopspring/decimal"
)

func TestModuleRegistrationAndDependencyErrors(t *testing.T) {
	module := &Module{}

	if err := module.ValidateDependencies(nil); err == nil {
		t.Fatal("ValidateDependencies(nil) returned nil, want dependency error")
	}
	if err := module.Initialize(context.Background(), nil); err == nil {
		t.Fatal("Initialize with nil core store returned nil, want dependency error")
	}
	if err := module.RegisterRules(nil); err == nil {
		t.Fatal("RegisterRules(nil) returned nil, want registration error")
	}

	router := events.NewRouter()
	if err := module.RegisterRules(router); err != nil {
		t.Fatalf("RegisterRules returned error: %v", err)
	}
	rules := router.GetRules("IFRS15_REVENUE_RECOGNITION")
	if len(rules) != 1 {
		t.Fatalf("registered %d rules, want 1", len(rules))
	}
	want := events.PostingRule{
		RuleID:              "ifrs15-revenue",
		EventType:           "IFRS15_REVENUE_RECOGNITION",
		DebitAccountCode:    "1250",
		CreditAccountCode:   "4200",
		DescriptionTemplate: "IFRS 15 Performance Obligation Satisfied",
	}
	if rules[0] != want {
		t.Fatalf("registered rule = %+v, want %+v", rules[0], want)
	}
}

func TestRevenueRecognitionPostsBalancedEntryFromMasterData(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	masterData := driver.MasterDataStore()

	companyID := types.NewCompanyID()
	if err := masterData.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "ACME", LegalName: "Acme Example Ltd",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}

	assetID := types.NewAccountID()
	revenueID := types.NewAccountID()
	for _, account := range []masterdata.Account{
		{ID: assetID, CompanyID: companyID, Code: "1250", Name: "Contract Asset", Type: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
		{ID: revenueID, CompanyID: companyID, Code: "4200", Name: "Revenue", Type: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, Currency: "USD", IsActive: true},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			t.Fatalf("create account %s: %v", account.Code, err)
		}
		approved, err := masterData.GetAccount(ctx, companyID, account.ID)
		if err != nil {
			t.Fatalf("read approved account %s: %v", account.Code, err)
		}
		if approved.Code != account.Code || !approved.IsActive {
			t.Fatalf("master-data account = %+v, want active account code %s", approved, account.Code)
		}
	}

	fiscalYearID := types.NewFiscalYearID()
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, masterdata.FiscalYear{
		ID: fiscalYearID, CompanyID: companyID, Name: "FY2026",
		StartDate: start, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: masterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatalf("create fiscal year: %v", err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, masterdata.FiscalPeriod{
		ID: types.NewFiscalPeriodID(), CompanyID: companyID, FiscalYearID: fiscalYearID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(driver),
		sdk.WithStandard(&Module{}),
	).Build(ctx)
	if err != nil {
		t.Fatalf("build engine with IFRS 15 module: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	}()

	amount, err := types.NewMoneyFromString("250.75", "USD")
	if err != nil {
		t.Fatalf("create event amount: %v", err)
	}
	entry, err := engine.Events().ProcessEvent(ctx, events.AccountingEvent{
		EventID: "revenue-001", EventType: "IFRS15_REVENUE_RECOGNITION",
		CompanyID: companyID, EffectiveDate: time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		Amount: amount,
	})
	if err != nil {
		t.Fatalf("process IFRS 15 revenue event: %v", err)
	}
	if entry.Status != ledger.EntryPosted {
		t.Fatalf("entry status = %q, want %q", entry.Status, ledger.EntryPosted)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("entry has %d lines, want 2", len(entry.Lines))
	}
	if entry.Lines[0].AccountID != assetID || entry.Lines[1].AccountID != revenueID {
		t.Fatalf("line accounts = (%s, %s), want contract asset %s and revenue %s",
			entry.Lines[0].AccountID, entry.Lines[1].AccountID, assetID, revenueID)
	}

	zero, err := types.Zero("USD")
	if err != nil {
		t.Fatalf("create zero amount: %v", err)
	}
	if !entry.Lines[0].TransactionAmount.Equal(amount) ||
		!entry.Lines[0].FunctionalDebit.Equal(amount) ||
		!entry.Lines[0].FunctionalCredit.Equal(zero) {
		t.Errorf("debit line money = transaction %s, debit %s, credit %s; want %s, %s, %s",
			entry.Lines[0].TransactionAmount, entry.Lines[0].FunctionalDebit, entry.Lines[0].FunctionalCredit,
			amount, amount, zero)
	}
	if !entry.Lines[1].TransactionAmount.Equal(amount) ||
		!entry.Lines[1].FunctionalDebit.Equal(zero) ||
		!entry.Lines[1].FunctionalCredit.Equal(amount) {
		t.Errorf("credit line money = transaction %s, debit %s, credit %s; want %s, %s, %s",
			entry.Lines[1].TransactionAmount, entry.Lines[1].FunctionalDebit, entry.Lines[1].FunctionalCredit,
			amount, zero, amount)
	}

	var debitTotal, creditTotal decimal.Decimal
	for _, line := range entry.Lines {
		if line.FunctionalDebit.Currency() != "USD" || line.FunctionalCredit.Currency() != "USD" {
			t.Errorf("functional amounts use currencies debit=%q credit=%q, want USD",
				line.FunctionalDebit.Currency(), line.FunctionalCredit.Currency())
		}
		debitTotal = debitTotal.Add(line.FunctionalDebit.Amount())
		creditTotal = creditTotal.Add(line.FunctionalCredit.Amount())
	}
	if !debitTotal.Equal(amount.Amount()) || !creditTotal.Equal(amount.Amount()) || !debitTotal.Equal(creditTotal) {
		t.Errorf("functional totals debit=%s credit=%s, want balanced totals of %s",
			debitTotal, creditTotal, amount.Amount())
	}
}
