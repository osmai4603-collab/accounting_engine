package ifrs9

import (
	"context"
	"testing"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/spi/storage"
)

type testCoreLedgerStore struct{}

var _ storage.CoreLedgerStore = testCoreLedgerStore{}

func (testCoreLedgerStore) SaveEntry(context.Context, ledger.JournalEntry) error {
	return nil
}

func (testCoreLedgerStore) GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func (testCoreLedgerStore) PostEntry(context.Context, ledger.JournalEntry) error {
	return nil
}

func (testCoreLedgerStore) GetAccount(context.Context, types.CompanyID, types.AccountID) (ledger.Account, error) {
	return ledger.Account{}, nil
}

func (testCoreLedgerStore) GetFiscalPeriod(context.Context, types.CompanyID, types.FiscalPeriodID) (ledger.FiscalPeriod, error) {
	return ledger.FiscalPeriod{}, nil
}

func TestModuleDependencies(t *testing.T) {
	module := &Module{}

	if got := module.Dependencies(); len(got) != 0 {
		t.Fatalf("Dependencies() = %v, want no module dependencies", got)
	}
	if err := module.ValidateDependencies(nil); err == nil || err.Error() != "core ledger store cannot be nil for IFRS 9" {
		t.Fatalf("ValidateDependencies(nil) error = %v, want IFRS 9 core ledger store error", err)
	}
	if err := module.Initialize(context.Background(), nil); err == nil || err.Error() != "core ledger store cannot be nil for IFRS 9" {
		t.Fatalf("Initialize with nil store error = %v, want IFRS 9 core ledger store error", err)
	}
	if err := module.ValidateDependencies(testCoreLedgerStore{}); err != nil {
		t.Fatalf("ValidateDependencies with core ledger store: %v", err)
	}
	if err := module.Initialize(context.Background(), testCoreLedgerStore{}); err != nil {
		t.Fatalf("Initialize with core ledger store: %v", err)
	}
}

func TestRegisterRules(t *testing.T) {
	module := &Module{}
	if err := module.RegisterRules(nil); err == nil || err.Error() != "rule registrar is required" {
		t.Fatalf("RegisterRules(nil) error = %v, want rule registrar error", err)
	}

	router := events.NewRouter()
	if err := module.RegisterRules(router); err != nil {
		t.Fatalf("RegisterRules(router): %v", err)
	}

	got := router.GetRules("IFRS9_ECL_PROVISION")
	want := []events.PostingRule{{
		RuleID:              "ifrs9-ecl",
		EventType:           "IFRS9_ECL_PROVISION",
		DebitAccountCode:    "5300",
		CreditAccountCode:   "1290",
		DescriptionTemplate: "IFRS 9 Expected Credit Loss Provision",
	}}
	if len(got) != len(want) {
		t.Fatalf("registered rule count = %d, want %d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Errorf("registered IFRS 9 rule = %+v, want %+v", got[0], want[0])
	}
	if got := router.GetRules("IFRS9_LEASE_PAYMENT"); len(got) != 0 {
		t.Errorf("unexpected rules registered for unrelated event: %+v", got)
	}
}
