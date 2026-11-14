package ifrs16

import (
	"context"
	"errors"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

type Module struct{}

var (
	_ standards.StandardModule  = (*Module)(nil)
	_ standards.DescribedModule = (*Module)(nil)
)

func (m *Module) Descriptor() standards.ModuleDescriptor {
	return standards.ModuleDescriptor{
		ID:          "ifrs16",
		Name:        "IFRS 16",
		Version:     "1.0.0",
		Provides:    []standards.Capability{standards.CapLeaseAccounting},
		Description: "Limited-scope lease measurement workflows; not a claim of IFRS 16 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IFRS 16",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: lessee initial measurement and dated liability/ROU amortization schedules for supplied lease terms.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS16-INITIAL-01", Summary: "Measure initial lease liability and right-of-use asset from supplied payment terms and discount rate.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/lease.go:NewLeaseContract", TestEvidence: "internal/ifrs16/lease_test.go"},
				{ID: "IFRS16-SCHEDULE-01", Summary: "Calculate dated interest, principal, liability, and ROU depreciation schedules.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/lease.go:LeaseContract.AmortizationSchedule", TestEvidence: "internal/ifrs16/lease_test.go"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS16-LESSOR-ACCOUNTING", Summary: "Lessor accounting workflows.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS16-POSTING-WORKFLOW", Summary: "Persistent lease workflow with configured journal mapping and application-layer posting.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Lessor accounting, lease identification, completeness of the lease population, and host contract lifecycle.",
				"Production journal mapping, persistence, and posting through the application service.",
			},
		},
		Workflows: []standards.WorkflowDescriptor{
			{
				ID: "lease-initial-measurement", Name: "Lease initial measurement",
				Summary:        "Validate supplied lessee lease terms and calculate initial liability and ROU asset.",
				Inputs:         []string{"commencement date", "payment schedule", "discount rate", "direct costs, prepayments, and incentives", "useful life"},
				Steps:          []string{"validate payment terms and currencies", "calculate present value of payments", "calculate initial ROU asset"},
				Outputs:        []string{"initial lease liability", "initial ROU asset"},
				RequirementIDs: []string{"IFRS16-INITIAL-01"},
			},
			{
				ID: "lease-amortization-schedule", Name: "Lease amortization schedule",
				Summary:        "Calculate the liability roll-forward and ROU depreciation schedule without posting.",
				Inputs:         []string{"validated lease contract"},
				Steps:          []string{"calculate period interest and principal", "roll forward the liability", "calculate ROU depreciation"},
				Outputs:        []string{"dated liability and ROU schedule"},
				RequirementIDs: []string{"IFRS16-SCHEDULE-01"},
			},
		},
	}
}

func (m *Module) Name() string {
	return "IFRS 16"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports legacy.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 16")
	}
	return nil
}

func (m *Module) Initialize(ctx context.Context, ports legacy.CoreLedgerStore) error {
	return m.ValidateDependencies(ports)
}

func (m *Module) RegisterRules(registrar events.RuleRegistrar) error {
	// Posting rules require a configured workflow and journal mapping.
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner standards.ScheduledJobRunner) error {
	return nil
}

func (m *Module) Close(ctx context.Context) error {
	return nil
}
