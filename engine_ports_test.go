package accounting_engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
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
