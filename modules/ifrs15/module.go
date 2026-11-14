package ifrs15

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
		ID:          "ifrs15",
		Name:        "IFRS 15",
		Version:     "1.0.0",
		Provides:    []standards.Capability{standards.CapRevenueRecognition},
		Description: "Limited-scope revenue measurement workflows; not a claim of IFRS 15 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IFRS 15",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: relative standalone selling-price allocation and point-in-time/over-time recognition schedules.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS15-ALLOC-01", Summary: "Allocate transaction price using relative standalone selling prices.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/revenue.go:NewContract", TestEvidence: "internal/ifrs15/revenue_test.go:TestRelativeSSPWeightedAllocation"},
				{ID: "IFRS15-SCHEDULE-01", Summary: "Build daily or monthly recognition schedules for point-in-time and over-time obligations.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/revenue.go:Contract.Schedule", TestEvidence: "internal/ifrs15/revenue_test.go:TestDailyAndMonthlyRecognitionSchedules"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS15-VARIABLE-CONSIDERATION", Summary: "Variable consideration constraint and subsequent updates.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS15-CONTRACT-MODIFICATION", Summary: "End-to-end accounting for contract modifications.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS15-POSTING-WORKFLOW", Summary: "Persistent revenue workflow with configured journal mapping and application-layer posting.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Variable consideration, significant financing components, contract modifications, and principal-versus-agent assessments.",
				"Production workflow persistence, journal mapping, and posting through the application service.",
			},
		},
		Workflows: []standards.WorkflowDescriptor{
			{
				ID: "revenue-allocation", Name: "Revenue allocation",
				Summary:        "Validate a contract and allocate its transaction price across performance obligations.",
				Inputs:         []string{"contract/customer identifiers", "transaction price and currency", "performance obligations and standalone selling prices"},
				Steps:          []string{"validate contract and obligations", "allocate transaction price proportionally", "check allocation totals against rounded transaction price"},
				Outputs:        []string{"validated contract", "allocated transaction prices"},
				RequirementIDs: []string{"IFRS15-ALLOC-01"},
			},
			{
				ID: "revenue-recognition-schedule", Name: "Revenue recognition schedule",
				Summary:        "Calculate dated recognition amounts for a supported performance obligation.",
				Inputs:         []string{"validated contract", "performance obligation", "schedule frequency"},
				Steps:          []string{"validate recognition pattern and dates", "calculate dated recognition amounts", "return schedule without posting"},
				Outputs:        []string{"recognition schedule"},
				RequirementIDs: []string{"IFRS15-SCHEDULE-01"},
			},
		},
	}
}

func (m *Module) Name() string {
	return "IFRS 15"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports legacy.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 15")
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
