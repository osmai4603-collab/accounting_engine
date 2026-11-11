package accounting_engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/consolidation"
	"accounting_engine/internal/governance"
	"accounting_engine/internal/ias12"
	"accounting_engine/internal/ifrs15"
	"accounting_engine/internal/ifrs16"
	"accounting_engine/internal/ifrs9"
	"accounting_engine/internal/ifrsautomation"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/policy"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/memory"
	"github.com/google/uuid"
)

func TestNewWithPortsAcceptsIndependentStoragePorts(t *testing.T) {
	ports := StoragePorts{
		Posting:    memory.New(),
		Reporting:  memory.New(),
		MasterData: memory.New(),
		Subledger:  memory.New(),
		Governance: memory.New(),
		Valuation:  memory.New(),
	}

	engine, err := NewWithPorts(ports, DefaultConfig())
	if err != nil {
		t.Fatalf("construct engine with domain-specific ports: %v", err)
	}
	if engine.MasterData == nil || engine.Posting == nil || engine.Subledgers == nil ||
		engine.Valuation == nil || engine.Reporting == nil || engine.Governance == nil {
		t.Fatal("engine did not initialize every accounting domain")
	}
	if engine.IFRS9 != nil || engine.IFRS15 != nil || engine.IFRS16 != nil ||
		engine.IAS12 != nil || engine.Consolidation != nil {
		t.Fatal("standards units should remain disabled unless explicitly configured")
	}
	if engine.Policies != nil {
		t.Fatal("policy units should remain disabled unless explicitly configured")
	}
}

func TestNewWithPortsInjectsConfiguredStandardsUnits(t *testing.T) {
	ifrs15Unit, err := ifrs15.NewUnit(ifrs15.UnitConfig{
		JournalID: uuid.New(), CashAccountID: uuid.New(),
		ReceivableAccountID: uuid.New(), ContractAssetAccountID: uuid.New(),
		ContractLiabilityAccount: uuid.New(), RevenueAccountID: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	standards := StandardsConfig{
		IFRS9:         ifrs9.NewGeneralUnit(),
		IFRS15:        ifrs15Unit,
		IFRS16:        ifrs16.NewUnit(),
		IAS12:         ias12.NewUnit(),
		Consolidation: consolidation.NewUnit(nil),
	}
	engine, err := NewWithPorts(StoragePorts{
		Posting:    memory.New(),
		Reporting:  memory.New(),
		MasterData: memory.New(),
		Subledger:  memory.New(),
		Governance: memory.New(),
		Valuation:  memory.New(),
	}, Config{Standards: standards})
	if err != nil {
		t.Fatalf("construct engine with standards units: %v", err)
	}
	if engine.IFRS9 != standards.IFRS9 || engine.IFRS15 != standards.IFRS15 ||
		engine.IFRS16 != standards.IFRS16 || engine.IAS12 != standards.IAS12 ||
		engine.Consolidation != standards.Consolidation {
		t.Fatal("engine did not retain the configured standards units")
	}
}

func TestNewWithPortsConfiguresPolicyUnitAndPresentationCurrency(t *testing.T) {
	driver := memory.New()
	engine, err := NewWithPorts(StoragePorts{
		Posting:    driver,
		Reporting:  driver,
		MasterData: driver,
		Subledger:  driver,
		Governance: driver,
		Valuation:  driver,
		Policy:     driver,
	}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if engine.Policies == nil {
		t.Fatal("policy storage port did not initialize policy unit")
	}
	companyID, policyID := uuid.New(), uuid.New()
	if err := engine.Policies.SaveCurrencyPolicy(context.Background(), policy.EffectivePolicyHeader{
		PolicyID: policyID, CompanyID: companyID, Version: 1,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedBy:     "creator", ApprovedBy: "approver",
	}, policy.CurrencyPolicy{
		FunctionalCurrency: "SAR", PresentationCurrency: "USD",
		RoundingMethod: policy.RoundingBankers, AllowedCurrencies: []string{"SAR", "USD"},
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.Reporting.ResolvePresentationCurrency(
		context.Background(), companyID, time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "USD" {
		t.Fatalf("default presentation currency = %q, want USD", resolved)
	}
	if _, err := engine.Reporting.ResolvePresentationCurrency(
		context.Background(), companyID, time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), "EUR",
	); !errors.Is(err, policy.ErrCurrencyNotAllowed) {
		t.Fatalf("unsupported explicit translation = %v, want ErrCurrencyNotAllowed", err)
	}
}

func TestStandardsEntriesAndMonthlyCloseUseCentralPosting(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	companyID, journalID, debitAccountID, creditAccountID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "STANDARDS", LegalName: "Standards Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []masterdata.Account{
		{CompanyID: companyID, ID: debitAccountID, Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
		{CompanyID: companyID, ID: creditAccountID, Code: "2000", Name: "Contract liability", AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, IsActive: true},
	} {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	engine, err := New(driver, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	amount, _ := financialtypes.NewMoneyFromString("10.00", "USD")
	zero, _ := financialtypes.Zero("USD")
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	entry, err := posting.NewJournalEntry(journalID, date, "ifrs15-advance", []posting.JournalEntryLine{
		{AccountID: debitAccountID, Debit: amount, Credit: zero},
		{AccountID: creditAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	posted, err := engine.PostStandardsEntryContext(ctx, companyID, entry, "standards-user")
	if err != nil {
		t.Fatalf("post standards entry: %v", err)
	}
	if posted.Status != posting.EntryPosted {
		t.Fatalf("standards entry status = %s, want posted", posted.Status)
	}

	monthlyEntry, err := posting.NewJournalEntry(journalID, date, "ifrs16-monthly-close", []posting.JournalEntryLine{
		{AccountID: debitAccountID, Debit: amount, Credit: zero},
		{AccountID: creditAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	monthlyEntry.CompanyID = companyID
	monthlyEntry.Reference = "ifrs16:lease:test:depreciation:1"
	job := ifrsautomation.JobFunc{
		ID: "ifrs16-monthly-close",
		Execute: func(context.Context, ifrsautomation.Period) ([]posting.JournalEntry, error) {
			return []posting.JournalEntry{monthlyEntry}, nil
		},
	}
	period, _ := ifrsautomation.Month(2026, time.January)
	first, err := engine.RunMonthlyStandardsClose(ctx, period, "standards-user", job)
	if err != nil {
		t.Fatalf("run monthly standards close: %v", err)
	}
	second, err := engine.RunMonthlyStandardsClose(ctx, period, "standards-user", job)
	if err != nil {
		t.Fatalf("repeat monthly standards close: %v", err)
	}
	if len(first.PostedEntryIDs) != 1 || len(second.AlreadyPosted) != 1 {
		t.Fatalf("monthly close results = first %+v, second %+v; want posted then idempotent skip", first, second)
	}
	if _, err := engine.RunMonthlyStandardsClose(ctx, period, "standards-user"); !errors.Is(err, ErrStandardsJobsRequired) {
		t.Fatalf("empty monthly close error = %v, want ErrStandardsJobsRequired", err)
	}
}

func TestNewWithPortsRequiresEveryPort(t *testing.T) {
	if _, err := NewWithPorts(StoragePorts{}, DefaultConfig()); !errors.Is(err, ErrStoragePortRequired) {
		t.Fatalf("NewWithPorts error = %v, want ErrStoragePortRequired", err)
	}
}

func TestStrictGovernanceConfigRejectsPostingWithoutApprovalPolicy(t *testing.T) {
	driver := memory.New()
	engine, err := New(driver, Config{StrictGovernance: true})
	if err != nil {
		t.Fatal(err)
	}
	one, err := financialtypes.NewMoneyFromString("1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	companyID, debitAccountID, creditAccountID := uuid.New(), uuid.New(), uuid.New()
	for i, accountID := range []uuid.UUID{debitAccountID, creditAccountID} {
		if err := driver.CreateAccount(context.Background(), masterdata.Account{
			CompanyID: companyID, ID: accountID, Code: string(rune('1' + i)), Name: "Posting account",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
		}); err != nil {
			t.Fatal(err)
		}
	}
	entry, err := posting.NewJournalEntry(uuid.New(), time.Now().UTC(), "maker", []posting.JournalEntryLine{
		{ID: uuid.New(), AccountID: debitAccountID, TransactionAmount: one, TransactionCurrency: "USD", ExchangeRate: one.Amount(), FunctionalDebit: one, FunctionalCredit: zero},
		{ID: uuid.New(), AccountID: creditAccountID, TransactionAmount: one, TransactionCurrency: "USD", ExchangeRate: one.Amount(), FunctionalDebit: zero, FunctionalCredit: one},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	if err := engine.Posting.PostEntryContext(context.Background(), &entry, "checker"); !errors.Is(err, governance.ErrUnconfiguredApprovalPolicy) {
		t.Fatalf("posting without configured policy = %v; want ErrUnconfiguredApprovalPolicy", err)
	}
}
