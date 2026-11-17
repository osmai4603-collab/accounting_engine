package standardsparity

import (
	"context"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/modules/ias12"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs16"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

// TestStandardModules_SameContractOnBothStoragePaths is the P8-01 contract
// parity gate: every shipped standard module must accept the same public
// ModulePorts surface and build identically through WithStoragePorts and the
// deprecated WithCoreStorage path.
func TestStandardModules_SameContractOnBothStoragePaths(t *testing.T) {
	modules := []struct {
		name   string
		module func() standards.StandardModule
	}{
		{"IFRS 15", func() standards.StandardModule { return &ifrs15.Module{} }},
		{"IFRS 9", func() standards.StandardModule { return &ifrs9.Module{} }},
		{"IFRS 16", func() standards.StandardModule { return &ifrs16.Module{} }},
		{"IAS 12", func() standards.StandardModule { return &ias12.Module{} }},
	}

	paths := []struct {
		name string
		opt  func(*memory.Driver) sdk.Option
	}{
		{"ports", func(driver *memory.Driver) sdk.Option { return sdk.WithStoragePorts(driver.Ports()) }},
		{"legacy", func(driver *memory.Driver) sdk.Option { return sdk.WithCoreStorage(driver) }},
	}

	for _, tc := range modules {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// A module must reject a host that provides no ports on either path.
			if err := tc.module().ValidateDependencies(nil); err == nil {
				t.Fatal("ValidateDependencies(nil) succeeded, want explicit failure")
			}

			for _, path := range paths {
				path := path
				t.Run(path.name, func(t *testing.T) {
					driver := memory.New()
					module := tc.module()
					engine, err := sdk.NewBuilder(
						path.opt(driver),
						sdk.WithStandard(module),
					).Build(context.Background())
					if err != nil {
						t.Fatalf("build engine: %v", err)
					}
					defer func() {
						if closeErr := engine.Close(); closeErr != nil {
							t.Fatalf("close engine: %v", closeErr)
						}
					}()

					diag := engine.Standards().Diagnostics()
					if len(diag.ActiveModules) != 1 {
						t.Fatalf("active modules = %d, want 1", len(diag.ActiveModules))
					}
					if diag.ActiveModules[0].Name != tc.name {
						t.Fatalf("active module = %q, want %q", diag.ActiveModules[0].Name, tc.name)
					}
				})
			}
		})
	}
}

// TestModulePorts_ExposeStableCapabilitySubset pins the narrowing rule from
// P8-01: modules can read and submit through the application layer, but never
// receive storage mutation capabilities.
func TestModulePorts_ExposeStableCapabilitySubset(t *testing.T) {
	driver := memory.New()
	ports := driver.Ports()
	modulePorts := standards.NewModulePorts(ports.Entries, ports.Balance, ports.MasterData, nil)
	if modulePorts == nil {
		t.Fatal("NewModulePorts returned nil")
	}
	if modulePorts.Entries() == nil || modulePorts.MasterData() == nil {
		t.Fatalf("incomplete module ports: entries=%v masterdata=%v",
			modulePorts.Entries() != nil, modulePorts.MasterData() != nil)
	}
	if err := standards.ValidateStandardModulePorts(modulePorts); err == nil {
		t.Fatal("ValidateStandardModulePorts accepted missing application port")
	}
	if err := standards.ValidateStandardModulePorts(nil); err == nil {
		t.Fatal("ValidateStandardModulePorts(nil) succeeded")
	}
	if err := standards.ValidateStandardModulePorts(
		standards.NewModulePorts(ports.Entries, ports.Balance, ports.MasterData, stubApplication{}),
	); err != nil {
		t.Fatalf("validate complete module ports: %v", err)
	}
}

type stubApplication struct {
	publicapplication.Service
}
