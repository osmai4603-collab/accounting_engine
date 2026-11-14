package sdk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type mockCoreStore struct {
	legacy.CoreLedgerStore
	postCalls int
}

type legacyOnlyCoreStore struct {
	*mockCoreStore
	providerCalls int
}

func (s *legacyOnlyCoreStore) NewLedgerApplication() (legacy.LedgerApplication, error) {
	s.providerCalls++
	return legacyOnlyLedgerApplication{}, nil
}

type legacyOnlyLedgerApplication struct{}

func (legacyOnlyLedgerApplication) PostEntry(context.Context, ledger.JournalEntry) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func (legacyOnlyLedgerApplication) GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

func (legacyOnlyLedgerApplication) ReverseEntry(context.Context, types.CompanyID, types.JournalEntryID, string) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
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
	name            string
	err             error
	validateCalls   int
	initializeCalls int
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

func (m *mockStandard) ValidateDependencies(ports legacy.CoreLedgerStore) error {
	m.validateCalls++
	return m.err
}

func (m *mockStandard) Initialize(ctx context.Context, ports legacy.CoreLedgerStore) error {
	m.initializeCalls++
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

func TestBuilderRejectsLegacyApplicationThatBypassesUseCases(t *testing.T) {
	store := &legacyOnlyCoreStore{mockCoreStore: &mockCoreStore{}}
	_, err := NewBuilder(WithCoreStorage(store)).Build(context.Background())
	if err == nil || !strings.Contains(err.Error(), "application layer") {
		t.Fatalf("Build error = %v, want unsupported application-layer error", err)
	}
	if store.providerCalls != 0 {
		t.Fatalf("legacy application provider was called %d times", store.providerCalls)
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

func (m *dependentStandard) Name() string                                            { return m.name }
func (m *dependentStandard) Version() string                                         { return "1.0.0" }
func (m *dependentStandard) Dependencies() []string                                  { return m.deps }
func (m *dependentStandard) ValidateDependencies(ports legacy.CoreLedgerStore) error { return nil }
func (m *dependentStandard) Initialize(ctx context.Context, ports legacy.CoreLedgerStore) error {
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

func TestBuilder_PortsOnlyPostingJourney(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	masterData := driver.MasterDataStore()

	companyID := types.CompanyID(uuid.MustParse("10000000-0000-4000-8000-0000000000a1"))
	cashAccountID := types.AccountID(uuid.MustParse("20000000-0000-4000-8000-0000000000a1"))
	revenueAccountID := types.AccountID(uuid.MustParse("20000000-0000-4000-8000-0000000000a2"))
	yearID := types.FiscalYearID(uuid.MustParse("30000000-0000-4000-8000-0000000000a1"))
	periodID := types.FiscalPeriodID(uuid.MustParse("40000000-0000-4000-8000-0000000000a1"))
	journalID := publicmasterdata.NewJournalID()

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "PORTS", LegalName: "Ports Only Ltd",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "PORTS", Name: "Ports journal",
	}); err != nil {
		t.Fatalf("create journal: %v", err)
	}
	for _, account := range []publicmasterdata.Account{
		{
			ID: cashAccountID, CompanyID: companyID, Code: "1000", Name: "Cash",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		{
			ID: revenueAccountID, CompanyID: companyID, Code: "4000", Name: "Revenue",
			Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			t.Fatalf("create account %s: %v", account.Code, err)
		}
	}
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: start, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatalf("create fiscal year: %v", err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}

	engine, err := NewBuilder(WithStoragePorts(driver.Ports())).Build(ctx)
	if err != nil {
		t.Fatalf("build engine from ports: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	}()

	amount, err := types.NewMoneyFromString("42.00", "USD")
	if err != nil {
		t.Fatalf("create amount: %v", err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		t.Fatalf("create zero amount: %v", err)
	}
	entry := ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: companyID, JournalID: types.JournalID(journalID),
		FiscalPeriodID: periodID, Status: ledger.EntryDraft,
		Classification: ledger.EntryClassRegular,
		EffectiveDate:  time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		PostingDate:    time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		Description:    "Ports-only posting", SourceModule: "builder-test",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
		CreatedBy: "builder-test",
		Lines: []ledger.EntryLine{
			{
				ID: types.NewJournalEntryLineID(), AccountID: cashAccountID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.NewJournalEntryLineID(), AccountID: revenueAccountID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}
	posted, err := engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		t.Fatalf("post entry via ports-only engine: %v", err)
	}
	readBack, err := engine.Ledger().GetEntry(ctx, companyID, posted.ID)
	if err != nil {
		t.Fatalf("read entry via ports-only engine: %v", err)
	}
	if readBack.Status != ledger.EntryPosted || len(readBack.Lines) != 2 {
		t.Fatalf("unexpected read-back: status=%q lines=%d", readBack.Status, len(readBack.Lines))
	}
}

func TestBuilder_RejectsBothStorageSources(t *testing.T) {
	driver := memory.New()
	_, err := NewBuilder(
		WithCoreStorage(driver),
		WithStoragePorts(driver.Ports()),
	).Build(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("Build error = %v, want explicit storage source conflict", err)
	}
}

func TestBuilder_PortsPreflightReportsEveryMissingPort(t *testing.T) {
	_, err := NewBuilder(WithStoragePorts(storage.Ports{})).Build(context.Background())
	if err == nil {
		t.Fatal("expected preflight failure for empty ports")
	}
	for _, capability := range []string{
		"draft store", "entry reader", "posting writer", "sequence allocator", "master data reader",
	} {
		if !strings.Contains(err.Error(), capability) {
			t.Errorf("Build error does not mention %q: %v", capability, err)
		}
	}
}

func TestBuilder_PreflightRunsBeforeModuleInitialize(t *testing.T) {
	mod := &mockStandard{name: "IFRS15"}
	_, err := NewBuilder(
		WithStoragePorts(storage.Ports{}),
		WithStandard(mod),
	).Build(context.Background())
	if err == nil {
		t.Fatal("expected preflight failure")
	}
	if mod.validateCalls != 0 {
		t.Fatalf("ValidateDependencies called %d times before preflight completed", mod.validateCalls)
	}
	if mod.initializeCalls != 0 {
		t.Fatalf("Initialize called %d times before preflight completed", mod.initializeCalls)
	}
}

func TestBuilder_PortsModeRejectsStandardsExplicitly(t *testing.T) {
	driver := memory.New()
	mod := &mockStandard{name: "IFRS15"}
	_, err := NewBuilder(
		WithStoragePorts(driver.Ports()),
		WithStandard(mod),
	).Build(context.Background())
	if err == nil || !strings.Contains(err.Error(), "require WithCoreStorage") {
		t.Fatalf("Build error = %v, want explicit standards/ports conflict", err)
	}
	if mod.initializeCalls != 0 {
		t.Fatalf("Initialize called %d times despite preflight failure", mod.initializeCalls)
	}
}

func TestBuilder_OptionalCapabilitiesFailExplicitly(t *testing.T) {
	driver := memory.New()
	engine, err := NewBuilder(WithStoragePorts(driver.Ports())).Build(context.Background())
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	if _, err := engine.Reporting().GetTrialBalance(context.Background(), types.NewCompanyID(), time.Now(), "USD"); !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("Reporting error = %v, want %v", err, types.ErrCapabilityNotImplemented)
	}
	if err := engine.Governance().RecordAuditLog(context.Background(), "event", nil); !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("Governance error = %v, want %v", err, types.ErrCapabilityNotImplemented)
	}
}

func TestBuilder_StandardsDiagnosticsAndCapabilities(t *testing.T) {
	driver := memory.New()
	mod := &mockStandard{
		name: "test-module",
	}
	engine, err := NewBuilder(
		WithCoreStorage(driver),
		WithStandard(mod),
	).Build(context.Background())
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	defer engine.Close()

	standardsSvc := engine.Standards()
	if standardsSvc == nil {
		t.Fatal("expected non-nil Standards service")
	}

	diag := standardsSvc.Diagnostics()
	if len(diag.ActiveModules) != 1 {
		t.Fatalf("expected 1 active module in diagnostics, got %d", len(diag.ActiveModules))
	}
	if diag.ActiveModules[0].Name != "test-module" {
		t.Fatalf("expected active module name test-module, got %s", diag.ActiveModules[0].Name)
	}
}
