package ifrs9

import (
	"context"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
)

type testCoreLedgerStore struct{}

var _ legacy.CoreLedgerStore = testCoreLedgerStore{}

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
	if err := module.RegisterRules(nil); err != nil {
		t.Fatalf("RegisterRules(nil): %v", err)
	}

	router := events.NewRouter()
	if err := module.RegisterRules(router); err != nil {
		t.Fatalf("RegisterRules(router): %v", err)
	}
	for _, eventType := range []string{"IFRS9_ECL_PROVISION", "IFRS9_LEASE_PAYMENT"} {
		if got := router.GetRules(eventType); len(got) != 0 {
			t.Errorf("unexpected hard-coded posting rules for %s: %+v", eventType, got)
		}
	}

	descriptor := module.Descriptor()
	if err := descriptor.Profile.Validate(); err != nil {
		t.Fatalf("invalid standard profile: %v", err)
	}
	for _, workflow := range descriptor.Workflows {
		if err := workflow.Validate(*descriptor.Profile); err != nil {
			t.Errorf("invalid workflow %q: %v", workflow.ID, err)
		}
	}
}
