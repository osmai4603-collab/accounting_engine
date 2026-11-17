package ias12

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
		ID:          "ias12",
		Name:        "IAS 12",
		Version:     "1.0.0",
		Provides:    []standards.Capability{standards.CapDeferredTax},
		Description: "Bounded current-tax estimate and deferred-tax measurement; not a claim of IAS 12 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IAS 12",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: supplied taxable-profit estimate and supplied carrying-amount/tax-base differences using a versioned rate profile, effective dates, source references, and bounded DTA evidence. Local tax computation and full IAS 12 workflows are excluded.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IAS12-DEFERRED-MEASUREMENT-01", Summary: "Measure supplied temporary differences at enacted profile rates selected by expected reversal date; bound recognized deductible differences by supplied probable-profit evidence.", Status: standards.RequirementLimited, Implementation: "internal/ias12/profile.go:MeasureWithProfile", TestEvidence: "internal/ias12/profile_test.go"},
				{ID: "IAS12-CURRENT-TAX-ESTIMATE-01", Summary: "Estimate current tax from a supplied nonnegative taxable-profit base and the profile rate effective at period end.", Status: standards.RequirementLimited, Implementation: "internal/ias12/profile.go:MeasureCurrentTax", TestEvidence: "internal/ias12/profile_test.go"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IAS12-CURRENT-TAX-WORKFLOW", Summary: "Full current-tax determination including jurisdictional adjustments, losses, credits, uncertain positions, and filing.", Status: standards.RequirementNotImplemented},
				{ID: "IAS12-PRESENTATION-DISCLOSURE", Summary: "Tax presentation and disclosure workflows.", Status: standards.RequirementNotImplemented},
				{ID: "IAS12-POSTING-WORKFLOW", Summary: "Application-integrated tax workflow with host-verified mapping, authorization, persistence, and posting.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Local tax law, tax-return preparation, loss/credit utilization, uncertain tax positions, offsetting, presentation, and disclosure.",
				"Production mapping provisioning, application integration, authorization, persistence, and posting.",
				"Independent professional tax or accounting review has not been performed as part of this implementation.",
			},
		},
		Workflows: []standards.WorkflowDescriptor{
			{
				ID: "deferred-tax-measurement", Name: "Deferred-tax measurement",
				Summary:        "Measure temporary differences from supplied carrying amounts and tax bases using a versioned profile rate effective for the expected reversal date.",
				Inputs:         []string{"measurement period", "carrying amounts and tax bases with source references", "company/jurisdiction tax profile and enacted rates", "expected reversal dates", "probable-profit evidence for deductible differences"},
				Steps:          []string{"validate profile, source provenance, and effective dates", "classify taxable and deductible temporary differences", "limit DTA recognition to supported deductible amounts", "measure DTA and DTL"},
				Outputs:        []string{"per-balance temporary differences with rate and source provenance", "deferred-tax assets and liabilities"},
				RequirementIDs: []string{"IAS12-DEFERRED-MEASUREMENT-01"},
			},
			{
				ID: "current-tax-estimate", Name: "Current-tax estimate",
				Summary:        "Calculate a bounded estimate from an externally supplied taxable-profit amount and the profile rate effective at period end.",
				Inputs:         []string{"measurement period", "taxable-profit amount and source reference", "company/jurisdiction tax profile"},
				Steps:          []string{"validate profile and source provenance", "select enacted rate effective at period end", "calculate a simple tax estimate"},
				Outputs:        []string{"taxable-profit base, selected rate, and estimated current tax expense"},
				RequirementIDs: []string{"IAS12-CURRENT-TAX-ESTIMATE-01"},
			},
		},
	}
}

func (m *Module) Name() string {
	return "IAS 12"
}

func (m *Module) Version() string {
	return "1.0.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports standards.ModulePorts) error {
	if err := standards.ValidateStandardModulePorts(ports); err != nil {
		return fmt.Errorf("IAS 12: %w", err)
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
