package ias12

import (
	"context"
	"fmt"
	"sync"

	ias12engine "github.com/osmai4603-collab/accounting_engine/internal/ias12"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

type Module struct {
	mu          sync.RWMutex
	ports       standards.ModulePorts
	disclosures standards.DisclosureProvider
}

var (
	_ standards.StandardModule     = (*Module)(nil)
	_ standards.DescribedModule    = (*Module)(nil)
	_ standards.DisclosureProvider = (*Module)(nil)
)

// NewModule wraps a host-configured IAS 12 disclosure provider.
func NewModule(disclosures standards.DisclosureProvider) *Module {
	return &Module{disclosures: disclosures}
}

func (m *Module) Descriptor() standards.ModuleDescriptor {
	return standards.ModuleDescriptor{
		ID:          "ias12",
		Name:        "IAS 12",
		Version:     "1.0.0",
		Provides:    []standards.Capability{standards.CapDeferredTax},
		Description: "Bounded deferred-tax, current-tax estimate, IFRIC 23 uncertainty measurement, and income-tax reconciliation disclosures; not a claim of full IAS 12 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IAS 12",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: supplied taxable-profit estimate and temporary differences measured with versioned tax profiles and bounded DTA evidence; host-assessed IFRIC 23 positions measured with probable-acceptance, most-likely-amount, or expected-value inputs and explicit reassessment evidence; and structured tax-rate reconciliation disclosures from host-supplied reports. Local tax-law analysis, tax-return preparation/filing, and full IAS 12 workflows are excluded.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IAS12-DEFERRED-MEASUREMENT-01", Summary: "Measure supplied temporary differences at enacted profile rates selected by expected reversal date; bound recognized deductible differences by supplied probable-profit evidence.", Status: standards.RequirementLimited, Implementation: "internal/ias12/profile.go:MeasureWithProfile", TestEvidence: "internal/ias12/profile_test.go"},
				{ID: "IAS12-CURRENT-TAX-ESTIMATE-01", Summary: "Estimate current tax from a supplied nonnegative taxable-profit base and the profile rate effective at period end.", Status: standards.RequirementLimited, Implementation: "internal/ias12/profile.go:MeasureCurrentTax", TestEvidence: "internal/ias12/profile_test.go"},
				{ID: "IFRIC23-UNCERTAIN-TAX-TREATMENTS-01", Summary: "Apply probable-acceptance, most-likely-amount, or expected-value measurement to host-assessed uncertain tax treatments, with explicit unit-of-account, examination, provenance, and reassessment evidence.", Status: standards.RequirementLimited, Implementation: "internal/ias12/uncertain.go:MeasureUncertainTaxTreatment", TestEvidence: "internal/ias12/uncertain_test.go"},
				{ID: "IAS12-TAX-RATE-RECONCILIATION-01", Summary: "Build a structured, versioned tax-rate reconciliation disclosure from a validated IAS 12 reconciliation report.", Status: standards.RequirementLimited, Implementation: "internal/ias12/disclosures.go:BuildTaxReconciliationDisclosure", TestEvidence: "internal/ias12/disclosures_test.go"},
				{ID: "IAS12-POSTING-WORKFLOW", Summary: "Post deferred tax and current tax drafts through the public application service with real account mappings and idempotency.", Status: standards.RequirementLimited, Implementation: "modules/ias12/module.go:PostDeferredTax", TestEvidence: "modules/ias12/module_test.go:TestIAS12PostingWorkflow"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IAS12-CURRENT-TAX-WORKFLOW", Summary: "Full current-tax determination including jurisdictional adjustments, losses, credits, tax-return preparation, and filing.", Status: standards.RequirementNotImplemented},
				{ID: "IAS12-PRESENTATION-DISCLOSURE", Summary: "Full tax presentation and disclosure workflows beyond the bounded tax-rate reconciliation.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Local tax law interpretation, tax-return preparation, loss/credit utilization, tax filing, offsetting, and full tax presentation and disclosure.",
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
			{
				ID: "uncertain-tax-treatment", Name: "Uncertain tax treatment measurement",
				Summary:        "Measure a host-assessed uncertain tax treatment under IFRIC 23 using probable acceptance or the supported measurement method and retain reassessment evidence.",
				Inputs:         []string{"unit-of-account assessment and rationale", "tax authority full-knowledge examination assumption", "acceptance assessment and evidence", "reported tax amount", "possible outcomes and probabilities when acceptance is not probable", "facts and statute-of-limitations references"},
				Steps:          []string{"validate the unit of account and examination assumption", "accept the reported amount unchanged when acceptance is probable", "otherwise apply the selected most-likely-amount or expected-value method", "identify reassessment triggers and preserve the prior assessment"},
				Outputs:        []string{"recognized tax amount and signed adjustment", "liability or tax-asset increase", "assessment and reassessment provenance"},
				RequirementIDs: []string{"IFRIC23-UNCERTAIN-TAX-TREATMENTS-01"},
			},
			{
				ID: "tax-rate-reconciliation", Name: "Tax-rate reconciliation disclosure",
				Summary:        "Generate a structured, scoped IAS 12 tax-rate reconciliation disclosure from a measured reconciliation report.",
				Inputs:         []string{"company and reporting period", "IAS 12 reconciliation report", "source references"},
				Steps:          []string{"validate report scope and currencies", "serialize expected tax, permanent differences, current and deferred tax, and effective tax rate", "return the versioned disclosure for the reporting registry"},
				Outputs:        []string{"structured IAS 12 tax-rate reconciliation disclosure"},
				RequirementIDs: []string{"IAS12-TAX-RATE-RECONCILIATION-01"},
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
	if err := m.ValidateDependencies(ports); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ports = ports
	return nil
}

func (m *Module) PostDeferredTax(
	ctx context.Context,
	input ias12engine.DeferredTaxEntryInput,
	entityRef string,
	idempotencyKey string,
) (publicapplication.EntryResult, error) {
	m.mu.RLock()
	ports := m.ports
	m.mu.RUnlock()
	if ports == nil || ports.Application() == nil {
		return publicapplication.EntryResult{}, fmt.Errorf("IAS 12: module ports and application service are required")
	}

	entry, err := ias12engine.GenerateDeferredTaxEntry(input)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	lines, err := convertPostingLines(entry.Lines)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	if entityRef == "" {
		entityRef = input.CompanyID.String() + "-deferred-tax"
	}

	req := standards.StandardPostingRequest{
		StandardRef:    "IAS-12-DEFERRED-TAX",
		EntityID:       entityRef,
		IdempotencyKey: idempotencyKey,
		CompanyID:      types.CompanyID(input.CompanyID),
		JournalID:      types.JournalID(input.JournalID),
		EffectiveDate:  entry.EntryDate,
		PostingDate:    entry.EntryDate,
		Description:    entry.Reference,
		Lines:          lines,
		Classification: ledger.EntryClassRegular,
	}

	return ports.Posting().PostStandardEntry(ctx, req)
}

func (m *Module) PostCurrentTax(
	ctx context.Context,
	input ias12engine.CurrentTaxEntryInput,
	entityRef string,
	idempotencyKey string,
) (publicapplication.EntryResult, error) {
	m.mu.RLock()
	ports := m.ports
	m.mu.RUnlock()
	if ports == nil || ports.Application() == nil {
		return publicapplication.EntryResult{}, fmt.Errorf("IAS 12: module ports and application service are required")
	}

	entry, err := ias12engine.GenerateCurrentTaxEntry(input)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	lines, err := convertPostingLines(entry.Lines)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	if entityRef == "" {
		entityRef = input.CompanyID.String() + "-current-tax"
	}

	req := standards.StandardPostingRequest{
		StandardRef:    "IAS-12-CURRENT-TAX",
		EntityID:       entityRef,
		IdempotencyKey: idempotencyKey,
		CompanyID:      types.CompanyID(input.CompanyID),
		JournalID:      types.JournalID(input.JournalID),
		EffectiveDate:  entry.EntryDate,
		PostingDate:    entry.EntryDate,
		Description:    entry.Reference,
		Lines:          lines,
		Classification: ledger.EntryClassRegular,
	}

	return ports.Posting().PostStandardEntry(ctx, req)
}

func convertPostingLines(postingLines []posting.JournalEntryLine) ([]ledger.EntryLine, error) {
	lines := make([]ledger.EntryLine, 0, len(postingLines))
	for i, l := range postingLines {
		debitAmt := l.Debit
		creditAmt := l.Credit
		lineCurrency := debitAmt.Currency()
		if lineCurrency == "" {
			lineCurrency = creditAmt.Currency()
		}
		pubDebit, err := types.NewMoney(debitAmt.Amount(), lineCurrency)
		if err != nil {
			return nil, fmt.Errorf("line %d debit: %w", i, err)
		}
		pubCredit, err := types.NewMoney(creditAmt.Amount(), lineCurrency)
		if err != nil {
			return nil, fmt.Errorf("line %d credit: %w", i, err)
		}
		txAmt := pubDebit
		if txAmt.Amount().IsZero() {
			txAmt = pubCredit
		}
		lines = append(lines, ledger.EntryLine{
			ID:                  types.NewJournalEntryLineID(),
			AccountID:           types.AccountID(l.AccountID),
			TransactionAmount:   txAmt,
			TransactionCurrency: lineCurrency,
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     pubDebit,
			FunctionalCredit:    pubCredit,
		})
	}
	return lines, nil
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

func (m *Module) GetDisclosures(
	ctx context.Context,
	query publicreporting.DisclosureQuery,
) ([]publicreporting.StructuredDisclosure, error) {
	if m == nil {
		return nil, fmt.Errorf("IAS 12: module is nil")
	}
	provider := m.disclosures
	if provider == nil {
		var err error
		provider, err = ias12engine.NewTaxReconciliationDisclosureProvider()
		if err != nil {
			return nil, fmt.Errorf("IAS 12: initialize empty disclosure provider: %w", err)
		}
	}
	return provider.GetDisclosures(ctx, query)
}
