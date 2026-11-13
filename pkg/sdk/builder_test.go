package sdk

import (
	"context"
	"errors"
	"testing"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/spi/standards"
	"accounting_engine/pkg/spi/storage"
	"accounting_engine/storage/memory"
)

type mockCoreStore struct {
	storage.CoreLedgerStore
	postCalls int
}

type mockPostingCoreStore struct {
	*mockCoreStore
	posting.BulkRepository
	masterdata.ContextStorageRepository
}

func (m *mockCoreStore) PostEntry(ctx context.Context, entry ledger.JournalEntry) error {
	m.postCalls++
	return nil
}

func (m *mockCoreStore) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func newMockPostingCoreStore() *mockPostingCoreStore {
	return &mockPostingCoreStore{mockCoreStore: &mockCoreStore{}}
}

type mockStandard struct {
	standards.StandardModule
	name string
	err  error
}

func (m *mockStandard) Name() string {
	return m.name
}

func (m *mockStandard) Version() string {
	return "1.0.0"
}

func (m *mockStandard) Dependencies() []string {
	return nil
}

func (m *mockStandard) ValidateDependencies(ports storage.CoreLedgerStore) error {
	return m.err
}

func (m *mockStandard) Initialize(ctx context.Context, ports storage.CoreLedgerStore) error {
	return m.err
}

func (m *mockStandard) RegisterRules(registrar events.RuleRegistrar) error {
	return nil
}

func (m *mockStandard) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}

func (m *mockStandard) Close(ctx context.Context) error {
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

func TestBuilder_RejectsStoreWithoutPostingCapabilities(t *testing.T) {
	_, err := NewBuilder(WithCoreStorage(&mockCoreStore{})).Build(context.Background())
	if !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("Build error = %v, want %v", err, types.ErrCapabilityNotImplemented)
	}
}

func TestLedgerServiceDoesNotFallbackToDirectStorage(t *testing.T) {
	store := &mockCoreStore{}
	service := &defaultLedgerService{}

	if _, err := service.PostEntry(context.Background(), ledger.JournalEntry{}); !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("PostEntry error = %v, want %v", err, types.ErrCapabilityNotImplemented)
	}
	if _, err := service.ReverseEntry(context.Background(), types.CompanyID{}, types.JournalEntryID{}, "reason"); !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("ReverseEntry error = %v, want %v", err, types.ErrCapabilityNotImplemented)
	}
	if store.postCalls != 0 {
		t.Fatalf("direct storage posting was called %d times", store.postCalls)
	}
}

func TestBuilder_Success(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
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
	store := memory.New()
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
	if err := engine.Close(); err != nil {
		t.Fatalf("failed to close engine: %v", err)
	}
}

type dependentStandard struct {
	standards.StandardModule
	name string
	deps []string
}

func (m *dependentStandard) Name() string                                             { return m.name }
func (m *dependentStandard) Version() string                                          { return "1.0.0" }
func (m *dependentStandard) Dependencies() []string                                   { return m.deps }
func (m *dependentStandard) ValidateDependencies(ports storage.CoreLedgerStore) error { return nil }
func (m *dependentStandard) Initialize(ctx context.Context, ports storage.CoreLedgerStore) error {
	return nil
}
func (m *dependentStandard) RegisterRules(registrar events.RuleRegistrar) error { return nil }
func (m *dependentStandard) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}
func (m *dependentStandard) Close(ctx context.Context) error { return nil }

func TestBuilder_ModuleDependencies(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	// Module A requires Module B, but B is not provided.
	modA := &dependentStandard{name: "ModuleA", deps: []string{"ModuleB"}}
	_, err := NewBuilder(
		WithCoreStorage(store),
		WithStandard(modA),
	).Build(ctx)
	if err == nil {
		t.Fatalf("expected error due to missing dependency, got nil")
	}
}
