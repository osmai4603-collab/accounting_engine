package ias12

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
		ID:          "ias12",
		Name:        "IAS 12",
		Version:     "1.0.0",
		Provides:    []standards.Capability{standards.CapDeferredTax},
		Description: "Limited-scope deferred-tax measurement; not a claim of IAS 12 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IAS 12",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: deferred-tax measurement for supplied temporary differences using enacted rates and explicit DTA probability evidence.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IAS12-DEFERRED-MEASUREMENT-01", Summary: "Measure deferred tax on temporary differences using enacted rates and the supplied probability assessment for deductible differences.", Status: standards.RequirementLimited, Implementation: "internal/ias12/ias12.go:Measure", TestEvidence: "internal/ias12/ias12_test.go:TestMeasureDTAAndDTLSignsAndRates"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IAS12-CURRENT-TAX-WORKFLOW", Summary: "End-to-end current-tax measurement workflow.", Status: standards.RequirementNotImplemented},
				{ID: "IAS12-PRESENTATION-DISCLOSURE", Summary: "Tax presentation and disclosure workflows.", Status: standards.RequirementNotImplemented},
				{ID: "IAS12-POSTING-WORKFLOW", Summary: "Persistent tax workflow with configured journal mapping and application-layer posting.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Complete current-tax, uncertain-tax, offsetting, presentation, and disclosure workflows.",
				"Production journal mapping, persistence, and posting through the application service.",
			},
		},
		Workflows: []standards.WorkflowDescriptor{
			{
				ID: "deferred-tax-measurement", Name: "Deferred-tax measurement",
				Summary:        "Measure deferred-tax assets and liabilities from supplied carrying amounts, tax bases, enacted rates, and probability evidence.",
				Inputs:         []string{"measurement period", "tax balances and tax bases", "enacted tax rates", "future taxable-profit probability evidence"},
				Steps:          []string{"validate balances, currencies, and enacted rates", "classify temporary differences", "measure DTA and DTL"},
				Outputs:        []string{"per-balance temporary differences", "deferred-tax assets and liabilities"},
				RequirementIDs: []string{"IAS12-DEFERRED-MEASUREMENT-01"},
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

func (m *Module) ValidateDependencies(ports legacy.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IAS 12")
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
