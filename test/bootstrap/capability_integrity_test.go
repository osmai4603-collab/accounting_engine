package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

func buildError(t *testing.T, opts ...sdk.Option) error {
	t.Helper()
	engine, err := sdk.NewBuilder(opts...).Build(context.Background())
	if engine != nil {
		_ = engine.Close()
	}
	return err
}

func requireErrorContaining(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Build succeeded, want error containing %v", fragments)
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("Build error %q does not contain %q", err.Error(), fragment)
		}
	}
}

func TestBootstrap_ReportingRequiredWithoutStoreFailsFast(t *testing.T) {
	ports := memory.New().Ports()
	ports.Balance = nil
	err := buildError(t,
		sdk.WithStoragePorts(ports),
		sdk.WithRequiredCapabilities(sdk.BootstrapReporting),
	)
	requireErrorContaining(t, err, "reporting", "WithReporting")
}

func TestBootstrap_GovernanceRequiredWithoutStoreFailsFast(t *testing.T) {
	ports := memory.New().Ports()
	ports.Audit = nil
	err := buildError(t,
		sdk.WithStoragePorts(ports),
		sdk.WithRequiredCapabilities(sdk.BootstrapGovernance),
	)
	requireErrorContaining(t, err, "governance", "WithGovernance")
}

func TestBootstrap_StandardsRequiredWithoutModuleFailsFast(t *testing.T) {
	err := buildError(t,
		sdk.WithStoragePorts(memory.New().Ports()),
		sdk.WithRequiredCapabilities(sdk.BootstrapStandards),
	)
	requireErrorContaining(t, err, "standards", "WithStandard")
}

func TestBootstrap_EventIngestionRequiredWithoutEventStoresFailsFast(t *testing.T) {
	ports := memory.New().Ports()
	ports.Events.Idempotency = nil
	ports.Events.Mappings = nil
	err := buildError(t,
		sdk.WithStoragePorts(ports),
		sdk.WithRequiredCapabilities(sdk.BootstrapEventIngestion),
	)
	requireErrorContaining(t, err, "event_ingestion", "Events.Idempotency", "Events.Mappings")
}

func TestBootstrap_SubledgersUnavailableThroughPortsFailsFast(t *testing.T) {
	err := buildError(t,
		sdk.WithStoragePorts(memory.New().Ports()),
		sdk.WithRequiredCapabilities(sdk.BootstrapSubledgers),
	)
	requireErrorContaining(t, err, "subledgers", "WithStoragePorts")
}

func TestBootstrap_PostingPolicyRequiredWithoutEnforcementFailsFast(t *testing.T) {
	err := buildError(t,
		sdk.WithCoreStorage(memory.New()),
		sdk.WithRequiredCapabilities(sdk.BootstrapPostingPolicy),
	)
	requireErrorContaining(t, err, "posting_policy", "RequirePolicy")
}

func TestBootstrap_UnknownCapabilityFailsFast(t *testing.T) {
	err := buildError(t,
		sdk.WithStoragePorts(memory.New().Ports()),
		sdk.WithRequiredCapabilities("not-a-capability"),
	)
	requireErrorContaining(t, err, "unknown required capability")
}

func TestBootstrap_SatisfiedCapabilitiesBuild(t *testing.T) {
	driver := memory.New()
	ctx := context.Background()
	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(driver),
		sdk.WithRequiredCapabilities(
			sdk.BootstrapReporting,
			sdk.BootstrapGovernance,
			sdk.BootstrapStandards,
			sdk.BootstrapEventIngestion,
			sdk.BootstrapSubledgers,
		),
		sdk.WithReporting(driver),
		sdk.WithGovernance(driver),
		sdk.WithStandard(&noopModule{}),
	).Build(ctx)
	if err != nil {
		t.Fatalf("Build with satisfied capabilities: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBootstrap_StandardsSatisfiedThroughPorts(t *testing.T) {
	ctx := context.Background()
	engine, err := sdk.NewBuilder(
		sdk.WithStoragePorts(memory.New().Ports()),
		sdk.WithRequiredCapabilities(
			sdk.BootstrapReporting,
			sdk.BootstrapGovernance,
			sdk.BootstrapStandards,
			sdk.BootstrapEventIngestion,
		),
		sdk.WithStandard(&noopModule{}),
	).Build(ctx)
	if err != nil {
		t.Fatalf("Build with ports-satisfied capabilities: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// noopModule implements the public standards.StandardModule contract without
// touching storage, so the test isolates bootstrap dependency validation.
type noopModule struct{}

func (m *noopModule) Name() string                                           { return "noop" }
func (m *noopModule) Version() string                                        { return "1.0.0" }
func (m *noopModule) Dependencies() []string                                 { return nil }
func (m *noopModule) ValidateDependencies(ports standards.ModulePorts) error { return nil }
func (m *noopModule) Initialize(ctx context.Context, ports standards.ModulePorts) error {
	return nil
}
func (m *noopModule) RegisterRules(registrar events.RuleRegistrar) error { return nil }
func (m *noopModule) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}
func (m *noopModule) Close(ctx context.Context) error { return nil }
