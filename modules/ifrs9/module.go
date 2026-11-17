package ifrs9

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
			Scope:         "Limited: simplified provision-matrix ECL for supplied trade receivables and reproducible, scenario-weighted general-approach ECL for supplied exposures; host-supplied risk assessments and source snapshots are required.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS9-ECL-SIMPLIFIED-01", Summary: "Calculate a simplified ECL allowance for receivables using supplied aging-bucket rates and existing allowance.", Status: standards.RequirementLimited, Implementation: "internal/ifrs9/ecl.go:Calculate", TestEvidence: "internal/ifrs9/ecl_test.go:TestCalculateProvisionMatrixAndAllowanceAdjustment"},
				{ID: "IFRS9-ECL-GENERAL-VERSIONED-01", Summary: "Calculate a reproducible scenario-weighted Stage 1/2/3 ECL allowance from explicitly supplied, versioned assumptions and exposure inputs.", Status: standards.RequirementLimited, Implementation: "internal/ifrs9/versioned.go:EvaluateVersionedECL", TestEvidence: "internal/ifrs9/versioned_test.go:TestEvaluateVersionedECLGolden"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS9-CLASSIFICATION-MEASUREMENT", Summary: "End-to-end classification and measurement workflows for financial instruments.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS9-GENERAL-ECL-WORKFLOW", Summary: "Complete end-to-end general expected-credit-loss workflow with independently validated stage and risk estimates, portfolio sourcing, and controls.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS9-POSTING-WORKFLOW", Summary: "Persistent ECL workflow with configured journal mapping and application-layer posting.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Business-model and SPPI classification, financial-asset measurement categories, derecognition, hedge accounting, and complete general ECL model coverage.",
				"Independent SICR/default assessment, validated portfolio sourcing, persistence, production journal mapping, and posting through the application service.",
				"Any assertion of IFRS 9 compliance or professional validation of the models, assumptions, thresholds, or source data.",
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
			{
				ID: "versioned-general-ecl-measurement", Name: "Versioned general ECL measurement",
				Summary:        "Calculate a deterministic, scenario-weighted Stage 1/2/3 allowance from caller-supplied versioned inputs without persistence or posting.",
				Inputs:         []string{"evaluation date", "model version and named assumptions", "source snapshot reference and hash", "exposures with prior stage and stage evidence", "scenario weights and PD estimates", "LGD, EAD, remaining life, EIR, and existing allowance"},
				Steps:          []string{"validate provenance and explicit scenario assumptions", "assess bounded stage transitions", "calculate scenario-weighted ECL and EIR discount", "fingerprint the complete measurement input and compare required allowance with the supplied existing allowance"},
				Outputs:        []string{"per-exposure ECL and stage transition", "required allowance", "allowance change", "reproducible input fingerprint"},
				RequirementIDs: []string{"IFRS9-ECL-GENERAL-VERSIONED-01"},
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

func (m *Module) ValidateDependencies(ports standards.ModulePorts) error {
	if err := standards.ValidateStandardModulePorts(ports); err != nil {
		return fmt.Errorf("IFRS 9: %w", err)
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
