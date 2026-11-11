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
