package sdk

import (
	"context"
	"testing"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/spi/standards"
	"accounting_engine/pkg/spi/storage"
)

type mockCoreStore struct {
	storage.CoreLedgerStore
}

func (m *mockCoreStore) PostEntry(ctx context.Context, entry ledger.JournalEntry) error {
	return nil
}

func (m *mockCoreStore) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

type mockStandard struct {
	standards.StandardModule
	name string
	err  error
}

func (m *mockStandard) Name() string {
	return m.name
}

func (m *mockStandard) ValidateDependencies(ports storage.CoreLedgerStore) error {
	return m.err
}

func (m *mockStandard) RegisterRules(router interface{}) error {
	return nil
}

func (m *mockStandard) RunScheduledJobs(ctx context.Context, runner interface{}) error {
	return nil
}

func TestBuilder_FailFast(t *testing.T) {
	ctx := context.Background()
	engine, err := NewBuilder().Build(ctx)
	if err == nil {
		t.Fatalf("expected error when building without core storage, got nil")
	}
	if engine != nil {
		t.Fatalf("expected nil engine on failure")
	}
}

func TestBuilder_Success(t *testing.T) {
	ctx := context.Background()
	store := &mockCoreStore{}
	engine, err := NewBuilder(WithCoreStorage(store)).Build(ctx)
	if err != nil {
		t.Fatalf("failed to build engine: %v", err)
	}
	if engine == nil {
		t.Fatalf("expected non-nil engine")
	}
	if engine.Ledger() == nil {
		t.Fatalf("expected ledger service")
	}
}

func TestBuilder_StandardValidation(t *testing.T) {
	ctx := context.Background()
	store := &mockCoreStore{}
	mod := &mockStandard{name: "IFRS15", err: nil}
	engine, err := NewBuilder(
		WithCoreStorage(store),
		WithStandard(mod),
	).Build(ctx)
	if err != nil {
		t.Fatalf("unexpected error with valid standard module: %v", err)
	}
	if engine == nil {
		t.Fatalf("expected engine instance")
	}
}
