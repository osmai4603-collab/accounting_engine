package sdk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

type rollbackTrackerModule struct {
	name                string
	deps                []string
	initErr             error
	initialized         bool
	closed              bool
	closeErr            error
	initializedSequence *[]string
	closedSequence      *[]string
}

func (m *rollbackTrackerModule) Name() string                                            { return m.name }
func (m *rollbackTrackerModule) Version() string                                         { return "1.0.0" }
func (m *rollbackTrackerModule) Dependencies() []string                                  { return m.deps }
func (m *rollbackTrackerModule) ValidateDependencies(ports legacy.CoreLedgerStore) error { return nil }
func (m *rollbackTrackerModule) Initialize(ctx context.Context, ports legacy.CoreLedgerStore) error {
	if m.initErr != nil {
		return m.initErr
	}
	m.initialized = true
	if m.initializedSequence != nil {
		*m.initializedSequence = append(*m.initializedSequence, m.name)
	}
	return nil
}
func (m *rollbackTrackerModule) RegisterRules(registrar events.RuleRegistrar) error { return nil }
func (m *rollbackTrackerModule) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}
func (m *rollbackTrackerModule) Close(ctx context.Context) error {
	m.closed = true
	if m.closedSequence != nil {
		*m.closedSequence = append(*m.closedSequence, m.name)
	}
	return m.closeErr
}

func TestP5_02_Preflight_ReportingAndGovernanceDisabledReasons(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	// Build engine without reporting or governance stores configured
	engine, err := NewBuilder(WithCoreStorage(store)).Build(ctx)
	if err != nil {
		t.Fatalf("failed to build engine: %v", err)
	}
	defer engine.Close()

	diag := engine.Standards().Diagnostics()

	// Verify Reporting capability disabled reason
	reason, ok := diag.DisabledReasons["capability:reporting"]
	if !ok {
		t.Fatalf("expected disabled reason for reporting capability")
	}
	if !strings.Contains(reason, "reporting data sources missing") {
		t.Fatalf("unexpected reporting disabled reason: %s", reason)
	}

	// Verify Governance capability disabled reason
	reason, ok = diag.DisabledReasons["capability:governance"]
	if !ok {
		t.Fatalf("expected disabled reason for governance capability")
	}
	if !strings.Contains(reason, "governance required stores missing") {
		t.Fatalf("unexpected governance disabled reason: %s", reason)
	}

	// Calling reporting service when store is disabled returns ErrCapabilityNotImplemented
	_, err = engine.Reporting().GetTrialBalance(ctx, types.CompanyID{}, time.Now(), "USD")
	if !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("expected ErrCapabilityNotImplemented for disabled reporting service, got %v", err)
	}

	// Calling governance service when store is disabled returns ErrCapabilityNotImplemented
	err = engine.Governance().RecordAuditLog(ctx, "evt-1", nil)
	if !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("expected ErrCapabilityNotImplemented for disabled governance service, got %v", err)
	}
}

type mockGovStore struct{}

func (m *mockGovStore) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	return nil
}

func (m *mockGovStore) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	return "fp", nil
}

func TestP5_02_Preflight_ActiveCapabilities(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	governanceStore := &mockGovStore{}

	engine, err := NewBuilder(
		WithCoreStorage(store),
		WithGovernance(governanceStore),
	).Build(ctx)
	if err != nil {
		t.Fatalf("failed to build engine: %v", err)
	}
	defer engine.Close()

	if !engine.Standards().HasCapability(standards.CapGovernance) {
		t.Fatalf("expected CapGovernance to be active")
	}
	if !engine.Standards().HasCapability(standards.CapAuditTrail) {
		t.Fatalf("expected CapAuditTrail to be active")
	}
}

func TestP5_03_Lifecycle_TeardownRollbackOnInitializationFailure(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	var initSeq []string
	var closeSeq []string

	modA := &rollbackTrackerModule{
		name:                "ModuleA",
		initializedSequence: &initSeq,
		closedSequence:      &closeSeq,
	}
	modB := &rollbackTrackerModule{
		name:                "ModuleB",
		deps:                []string{"ModuleA"},
		initErr:             errors.New("ModuleB initialization failed"),
		initializedSequence: &initSeq,
		closedSequence:      &closeSeq,
	}

	_, err := NewBuilder(
		WithCoreStorage(store),
		WithStandard(modA),
		WithStandard(modB),
	).Build(ctx)

	if err == nil {
		t.Fatalf("expected build to fail when ModuleB initialization fails")
	}

	// ModuleA should have been initialized
	if !modA.initialized {
		t.Fatalf("expected ModuleA to be initialized")
	}
	// ModuleB should NOT be initialized (it failed)
	if modB.initialized {
		t.Fatalf("ModuleB should not be initialized")
	}

	// ROLLBACK: ModuleA must have been closed during rollback!
	if !modA.closed {
		t.Fatalf("expected ModuleA to be closed during rollback teardown")
	}

	if len(closeSeq) != 1 || closeSeq[0] != "ModuleA" {
		t.Fatalf("unexpected close sequence during rollback: %v", closeSeq)
	}
}

func TestP5_03_Lifecycle_IdempotentCloseAndContextCancellation(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	mod := &rollbackTrackerModule{
		name: "ModuleA",
	}

	engine, err := NewBuilder(
		WithCoreStorage(store),
		WithStandard(mod),
	).Build(ctx)
	if err != nil {
		t.Fatalf("failed to build engine: %v", err)
	}

	// First Close should succeed
	err1 := engine.Close()
	if err1 != nil {
		t.Fatalf("first Close failed: %v", err1)
	}
	if !mod.closed {
		t.Fatalf("module was not closed")
	}

	// Second Close should be idempotent and return the exact same result
	err2 := engine.Close()
	if err2 != err1 {
		t.Fatalf("expected idempotent Close to return second call = %v, got %v", err1, err2)
	}

	// Test CloseWithContext with cancelled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err3 := engine.CloseWithContext(canceledCtx)
	// Must return the cached close result
	if err3 != err1 {
		t.Fatalf("idempotent CloseWithContext returned unexpected error: %v", err3)
	}
}
