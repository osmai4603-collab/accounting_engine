package ifrs9

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
		ID:          "ifrs9",
		Name:        "IFRS 9",
		Version:     "1.0.0",
		Provides:    []standards.Capability{standards.CapFinancialInstruments},
		Description: "Limited-scope expected credit loss measurement; not a claim of IFRS 9 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IFRS 9",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: simplified provision-matrix ECL measurement for supplied trade-receivable exposures.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS9-ECL-SIMPLIFIED-01", Summary: "Calculate a simplified ECL allowance for receivables using supplied aging-bucket rates and existing allowance.", Status: standards.RequirementLimited, Implementation: "internal/ifrs9/ecl.go:Calculate", TestEvidence: "internal/ifrs9/ecl_test.go:TestCalculateProvisionMatrixAndAllowanceAdjustment"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS9-CLASSIFICATION-MEASUREMENT", Summary: "End-to-end classification and measurement workflows for financial instruments.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS9-GENERAL-ECL-WORKFLOW", Summary: "Complete general expected-credit-loss workflow across the financial-asset population.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS9-POSTING-WORKFLOW", Summary: "Persistent ECL workflow with configured journal mapping and application-layer posting.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Financial-asset classification and measurement, derecognition, hedge accounting, and complete general ECL model coverage.",
				"Production journal mapping, persistence, and posting through the application service.",
			},
		},
		Workflows: []standards.WorkflowDescriptor{
			{
				ID: "receivables-ecl-measurement", Name: "Receivables ECL measurement",
				Summary:        "Measure a simplified receivables allowance from host-supplied exposures, matrix rates, and existing allowance.",
				Inputs:         []string{"measurement date", "receivable exposures and aging", "provision matrix rates", "existing allowance"},
				Steps:          []string{"validate configuration and measurement date", "classify exposures by aging bucket", "calculate required allowance and adjustment"},
				Outputs:        []string{"per-receivable ECL results", "required allowance", "allowance adjustment"},
				RequirementIDs: []string{"IFRS9-ECL-SIMPLIFIED-01"},
			},
		},
	}
}

func (m *Module) Name() string {
	return "IFRS 9"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports legacy.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IFRS 9")
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
