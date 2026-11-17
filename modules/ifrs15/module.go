package ifrs15

import (
	"context"
	"fmt"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
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
			Scope:         "Limited: host-assessed contract inputs, fixed transaction prices, relative standalone selling-price allocation, and recognition from externally measured service-delivery evidence through a configured application posting capability.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS15-5STEP-01", Summary: "Validate host-supplied five-step inputs for a supported fixed-price, distinct-obligation contract.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/workflow.go:NewAssessedContract", TestEvidence: "internal/ifrs15/workflow_test.go:TestAssessedContractValidatesFiveStepInputs"},
				{ID: "IFRS15-ALLOC-01", Summary: "Allocate transaction price using relative standalone selling prices.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/revenue.go:NewContract", TestEvidence: "internal/ifrs15/revenue_test.go:TestRelativeSSPWeightedAllocation"},
				{ID: "IFRS15-SCHEDULE-01", Summary: "Build daily or monthly recognition schedules for point-in-time and over-time obligations.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/revenue.go:Contract.Schedule", TestEvidence: "internal/ifrs15/revenue_test.go:TestDailyAndMonthlyRecognitionSchedules"},
				{ID: "IFRS15-POST-01", Summary: "Post externally measured satisfaction events through a configured application capability and real journal mapping before confirming in-memory contract state.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/workflow.go:RevenueWorkflow.Recognize", TestEvidence: "internal/ifrs15/workflow_test.go:TestRecognitionCommitsOnlyAfterPostingAndRetriesIdempotently"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS15-VARIABLE-CONSIDERATION", Summary: "Variable consideration constraint and subsequent updates.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS15-FINANCING", Summary: "Significant financing component identification and measurement.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS15-CONTRACT-MODIFICATION", Summary: "End-to-end accounting for contract modifications.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS15-DURABLE-WORKFLOW", Summary: "Durable contract workflow state, transactional outbox, and recovery across process restarts.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Variable consideration, significant financing components, contract modifications, and principal-versus-agent assessments.",
				"RevenueWorkflow state is in-memory only; the host must provide durable contract state and an idempotent application-backed posting capability for production use.",
				"Service delivery, control transfer, acceptance, and progress measurement are supplied and evidenced by the host; the engine does not determine them.",
			},
		},
		Workflows: []standards.WorkflowDescriptor{
			{
				ID: "revenue-five-step-input-validation", Name: "Five-step input validation",
				Summary:        "Validate host-assessed inputs for the explicitly supported fixed-price revenue scope and allocate transaction price.",
				Inputs:         []string{"host assessment of contract approval, enforceable rights, payment terms, commercial substance, and collectibility", "fixed transaction price without variable consideration or significant financing", "host assessment of distinct performance obligations and standalone selling prices"},
				Steps:          []string{"validate contract-identification and collectibility assertions", "reject unsupported transaction-price features", "validate distinct obligation inputs", "allocate the fixed transaction price by relative standalone selling prices"},
				Outputs:        []string{"validated contract", "allocated transaction prices"},
				RequirementIDs: []string{"IFRS15-5STEP-01", "IFRS15-ALLOC-01"},
			},
			{
				ID: "revenue-allocation", Name: "Revenue allocation",
				Summary:        "Validate a contract and allocate its transaction price across performance obligations.",
				Inputs:         []string{"contract/customer identifiers", "transaction price and currency", "performance obligations and standalone selling prices"},
				Steps:          []string{"validate contract and obligations", "allocate transaction price proportionally", "check allocation totals against rounded transaction price"},
				Outputs:        []string{"validated contract", "allocated transaction prices"},
				RequirementIDs: []string{"IFRS15-ALLOC-01"},
			},
			{
				ID: "revenue-satisfaction-posting", Name: "Revenue satisfaction posting",
				Summary:                "Recognize a host-measured satisfaction event only through a configured application posting capability and mapped ledger accounts.",
				Inputs:                 []string{"validated contract", "host-provided satisfaction event, amount, date, measurement basis, and evidence reference", "company, journal, revenue, contract-asset, and contract-liability mappings", "idempotent application-layer posting capability"},
				Steps:                  []string{"validate external evidence and obligation allocation", "construct a balanced journal draft from the real mapping", "post using the stable event idempotency key", "commit in-memory contract state only after posting succeeds"},
				Outputs:                []string{"confirmed contract state after successful posting", "posted recognition journal entry"},
				RequirementIDs:         []string{"IFRS15-POST-01"},
				RequiredCapabilities:   []standards.Capability{standards.CapLedgerPosting},
				ApplicationContract:    standards.ApplicationLedgerService,
				RequiresJournalMapping: true,
				ProducesJournalEntries: true,
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

func (m *Module) ValidateDependencies(ports standards.ModulePorts) error {
	if err := standards.ValidateStandardModulePorts(ports); err != nil {
		return fmt.Errorf("IFRS 15: %w", err)
	}
	return nil
}

func (m *Module) Initialize(ctx context.Context, ports standards.ModulePorts) error {
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
