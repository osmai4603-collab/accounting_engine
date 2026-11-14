package standards

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
)

type dummyModule struct {
	name         string
	version      string
	dependencies []string
	descriptor   *ModuleDescriptor
}

func (d *dummyModule) Name() string                  { return d.name }
func (d *dummyModule) Version() string               { return d.version }
func (d *dummyModule) Dependencies() []string        { return d.dependencies }
func (d *dummyModule) ValidateDependencies(legacy.CoreLedgerStore) error { return nil }
func (d *dummyModule) Initialize(context.Context, legacy.CoreLedgerStore) error { return nil }
func (d *dummyModule) RegisterRules(events.RuleRegistrar) error { return nil }
func (d *dummyModule) RunScheduledJobs(context.Context, ScheduledJobRunner) error { return nil }
func (d *dummyModule) Close(context.Context) error { return nil }

func (d *dummyModule) Descriptor() ModuleDescriptor {
	if d.descriptor != nil {
		return *d.descriptor
	}
	return ModuleDescriptor{
		ID:      canonicalModuleID(d.name),
		Name:    d.name,
		Version: d.version,
	}
}

func TestRegistry_TopologicalOrdering(t *testing.T) {
	// A requires B, B requires C.
	// Expected resolution order: C, B, A
	modA := &dummyModule{
		name: "Module A",
		descriptor: &ModuleDescriptor{
			ID:       "mod-a",
			Name:     "Module A",
			Version:  "1.0.0",
			Requires: []Requirement{{ID: "mod-b"}},
			Provides: []Capability{"cap-a"},
		},
	}
	modB := &dummyModule{
		name: "Module B",
		descriptor: &ModuleDescriptor{
			ID:       "mod-b",
			Name:     "Module B",
			Version:  "1.0.0",
			Requires: []Requirement{{ID: "mod-c"}},
			Provides: []Capability{"cap-b"},
		},
	}
	modC := &dummyModule{
		name: "Module C",
		descriptor: &ModuleDescriptor{
			ID:       "mod-c",
			Name:     "Module C",
			Version:  "1.0.0",
			Provides: []Capability{"cap-c"},
		},
	}

	reg := NewRegistry()
	// Register in random order: A, C, B
	if err := reg.Register(modA); err != nil {
		t.Fatalf("failed to register modA: %v", err)
	}
	if err := reg.Register(modC); err != nil {
		t.Fatalf("failed to register modC: %v", err)
	}
	if err := reg.Register(modB); err != nil {
		t.Fatalf("failed to register modB: %v", err)
	}

	ordered, err := reg.Resolve()
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}

	if len(ordered) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(ordered))
	}
	if ordered[0].Name() != "Module C" || ordered[1].Name() != "Module B" || ordered[2].Name() != "Module A" {
		t.Fatalf("expected order [C, B, A], got [%s, %s, %s]", ordered[0].Name(), ordered[1].Name(), ordered[2].Name())
	}

	// Verify capabilities
	if !reg.HasCapability("cap-a") || !reg.HasCapability("cap-b") || !reg.HasCapability("cap-c") {
		t.Fatal("expected all capabilities to be present")
	}
	provider, ok := reg.GetCapabilityProvider("cap-b")
	if !ok || provider != "mod-b" {
		t.Fatalf("expected provider mod-b for cap-b, got %v (ok=%v)", provider, ok)
	}

	// Verify diagnostics
	diag := reg.Diagnostics()
	if len(diag.ActiveCapabilities) != 3 {
		t.Fatalf("expected 3 active capabilities in diagnostics, got %d", len(diag.ActiveCapabilities))
	}
	if len(diag.DependencyOrder) != 3 {
		t.Fatalf("expected 3 modules in dependency order, got %d", len(diag.DependencyOrder))
	}
}

func TestRegistry_DetectsMissingDependency(t *testing.T) {
	modA := &dummyModule{
		name: "Module A",
		descriptor: &ModuleDescriptor{
			ID:       "mod-a",
			Name:     "Module A",
			Version:  "1.0.0",
			Requires: []Requirement{{ID: "mod-missing"}},
		},
	}

	reg := NewRegistry()
	if err := reg.Register(modA); err != nil {
		t.Fatalf("failed to register modA: %v", err)
	}

	_, err := reg.Resolve()
	if err == nil {
		t.Fatal("expected error on missing dependency, got nil")
	}
	if !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("expected ErrMissingDependency, got %v", err)
	}
	if !strings.Contains(err.Error(), "mod-missing") {
		t.Fatalf("expected error message to mention missing module: %v", err)
	}
}

func TestRegistry_DetectsDuplicateModule(t *testing.T) {
	mod1 := &dummyModule{name: "Duplicate Mod"}
	mod2 := &dummyModule{name: "Duplicate Mod"}

	reg := NewRegistry()
	if err := reg.Register(mod1); err != nil {
		t.Fatalf("failed to register first module: %v", err)
	}

	err := reg.Register(mod2)
	if err == nil {
		t.Fatal("expected duplicate registration error, got nil")
	}
	if !errors.Is(err, ErrDuplicateModule) {
		t.Fatalf("expected ErrDuplicateModule, got %v", err)
	}
}

func TestRegistry_DetectsCircularDependency(t *testing.T) {
	// A -> B -> C -> A
	modA := &dummyModule{
		name: "A",
		descriptor: &ModuleDescriptor{
			ID:       "mod-a",
			Requires: []Requirement{{ID: "mod-b"}},
		},
	}
	modB := &dummyModule{
		name: "B",
		descriptor: &ModuleDescriptor{
			ID:       "mod-b",
			Requires: []Requirement{{ID: "mod-c"}},
		},
	}
	modC := &dummyModule{
		name: "C",
		descriptor: &ModuleDescriptor{
			ID:       "mod-c",
			Requires: []Requirement{{ID: "mod-a"}},
		},
	}

	reg := NewRegistry()
	_ = reg.Register(modA)
	_ = reg.Register(modB)
	_ = reg.Register(modC)

	_, err := reg.Resolve()
	if err == nil {
		t.Fatal("expected circular dependency error, got nil")
	}
	if !errors.Is(err, ErrCircularDependency) {
		t.Fatalf("expected ErrCircularDependency, got %v", err)
	}
	if !strings.Contains(err.Error(), "mod-a") || !strings.Contains(err.Error(), "mod-b") || !strings.Contains(err.Error(), "mod-c") {
		t.Fatalf("expected cycle path in error message, got: %v", err)
	}
}

func TestRegistry_DetectsVersionConflict(t *testing.T) {
	modA := &dummyModule{
		name: "A",
		descriptor: &ModuleDescriptor{
			ID: "mod-a",
			Requires: []Requirement{
				{ID: "mod-b", MinVersion: "2.0.0"},
			},
		},
	}
	modB := &dummyModule{
		name: "B",
		descriptor: &ModuleDescriptor{
			ID:      "mod-b",
			Version: "1.5.0",
		},
	}

	reg := NewRegistry()
	_ = reg.Register(modA)
	_ = reg.Register(modB)

	_, err := reg.Resolve()
	if err == nil {
		t.Fatal("expected version conflict error, got nil")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
}

// Module without DescribedModule interface
type plainModule struct {
	name         string
	version      string
	dependencies []string
}

func (p *plainModule) Name() string                                     { return p.name }
func (p *plainModule) Version() string                                  { return p.version }
func (p *plainModule) Dependencies() []string                           { return p.dependencies }
func (p *plainModule) ValidateDependencies(legacy.CoreLedgerStore) error { return nil }
func (p *plainModule) Initialize(context.Context, legacy.CoreLedgerStore) error {
	return nil
}
func (p *plainModule) RegisterRules(events.RuleRegistrar) error              { return nil }
func (p *plainModule) RunScheduledJobs(context.Context, ScheduledJobRunner) error {
	return nil
}
func (p *plainModule) Close(context.Context) error { return nil }

func TestRegistry_LegacyModuleBackwardCompatibility(t *testing.T) {
	legacyA := &plainModule{
		name:         "Module Alpha",
		version:      "1.0.0",
		dependencies: []string{"Module Beta"},
	}
	legacyB := &plainModule{
		name:    "Module Beta",
		version: "1.0.0",
	}

	reg := NewRegistry()
	if err := reg.Register(legacyA); err != nil {
		t.Fatalf("failed to register legacyA: %v", err)
	}
	if err := reg.Register(legacyB); err != nil {
		t.Fatalf("failed to register legacyB: %v", err)
	}

	ordered, err := reg.Resolve()
	if err != nil {
		t.Fatalf("unexpected resolve error for legacy modules: %v", err)
	}

	if len(ordered) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(ordered))
	}
	if ordered[0].Name() != "Module Beta" || ordered[1].Name() != "Module Alpha" {
		t.Fatalf("expected [Beta, Alpha], got [%s, %s]", ordered[0].Name(), ordered[1].Name())
	}
}

func TestCompareSemVer(t *testing.T) {
	cases := []struct {
		v1   string
		v2   string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"v1.2.3", "1.2.3", 0},
		{"1.2", "1.2.0", 0},
		{"1.2.0", "1.2.1", -1},
	}
	for _, tc := range cases {
		got := compareSemVer(tc.v1, tc.v2)
		if got != tc.want {
			t.Errorf("compareSemVer(%q, %q) = %d, want %d", tc.v1, tc.v2, got, tc.want)
		}
	}
}
