package ifrs9

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	internalifrs9 "github.com/osmai4603-collab/accounting_engine/internal/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

type Module struct {
	mu    sync.RWMutex
	ports standards.ModulePorts
}

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
		Description: "Limited IFRS 9 host-classification, subsequent-measurement, and ECL workflows; not a claim of IFRS 9 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IFRS 9",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: host-approved financial-asset categories, effective-interest and fair-value subsequent measurement, simplified receivables ECL, and reproducible scenario-weighted general ECL for amortised-cost/FVOCI exposures. Host-supplied classifications, staging evidence, risk estimates, and source snapshots are required.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS9-ECL-SIMPLIFIED-01", Summary: "Calculate a simplified ECL allowance for receivables using supplied aging-bucket rates and existing allowance.", Status: standards.RequirementLimited, Implementation: "internal/ifrs9/ecl.go:Calculate", TestEvidence: "internal/ifrs9/ecl_test.go:TestCalculateProvisionMatrixAndAllowanceAdjustment"},
				{ID: "IFRS9-CLASSIFICATION-MEASUREMENT-01", Summary: "Apply host-approved amortised-cost, FVOCI, or FVTPL classifications to effective-interest and fair-value subsequent measurement.", Status: standards.RequirementLimited, Implementation: "internal/ifrs9/measurement.go:MeasureSubsequentPeriod", TestEvidence: "internal/ifrs9/measurement_test.go:TestMeasureSubsequentPeriodByCategory"},
				{ID: "IFRS9-ECL-GENERAL-VERSIONED-01", Summary: "Calculate reproducible, scenario-weighted Stage 1/2/3 ECL for classified amortised-cost and FVOCI exposures using computed EAD and category-specific allowance movements.", Status: standards.RequirementLimited, Implementation: "internal/ifrs9/versioned.go:EvaluateVersionedECL", TestEvidence: "internal/ifrs9/versioned_test.go:TestEvaluateVersionedECLGolden"},
				{ID: "IFRS9-ECL-POSTING-01", Summary: "Post ECL and subsequent-measurement journal drafts through the public application service using explicit account mappings and idempotency.", Status: standards.RequirementLimited, Implementation: "internal/ifrs9/application.go:PostCategorizedVersionedECLThroughApplication", TestEvidence: "internal/ifrs9/application_test.go:TestPostCategorizedVersionedECLThroughApplication"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS9-DERECOGNITION", Summary: "Complete financial-asset derecognition, modification, and write-off accounting workflows.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS9-HEDGE-ACCOUNTING", Summary: "Hedge accounting under IFRS 9 Chapter 6 is deferred to a separate future phase.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS9-INDEPENDENT-VALIDATION", Summary: "Independent professional validation of host models, estimates, source data, and accounting judgments.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"The host alone decides business-model and SPPI outcomes; this module validates references and applies the supplied final classification without independent judgment.",
				"Derecognition, modification, write-off, complete general ECL model coverage, persistent portfolio sourcing, and autonomous SICR/default assessment.",
				"Hedge accounting (IFRS 9 Chapter 6) is explicitly deferred to a separate future phase and is not included in this release.",
				"Independent professional validation of accounting models, assumptions, thresholds, mappings, and source data; no claim of full IFRS 9 compliance.",
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
				Summary:        "Calculate a deterministic, scenario-weighted Stage 1/2/3 allowance from host-classified AC/FVOCI exposures without persistence or posting.",
				Inputs:         []string{"evaluation date", "model version and named assumptions", "source snapshot reference and hash", "host classification and assessment references", "exposures with prior stage and stage evidence", "scenario weights and PD estimates", "LGD, ledger balance, undrawn commitments, remaining life, EIR, and per-exposure existing allowance"},
				Steps:          []string{"validate host classification, provenance, and explicit scenario assumptions", "compute EAD from ledger balance plus undrawn commitments", "assess bounded stage transitions", "calculate scenario-weighted ECL and EIR discount", "fingerprint all source, classification, risk, balance, and allowance inputs"},
				Outputs:        []string{"per-exposure ECL, EAD, and stage transition", "required and existing allowance by category", "allowance change by category", "reproducible input fingerprint"},
				RequirementIDs: []string{"IFRS9-ECL-GENERAL-VERSIONED-01"},
			},
			{
				ID: "financial-asset-subsequent-measurement", Name: "Financial asset subsequent measurement",
				Summary:                "Apply host-approved AC/FVOCI/FVTPL classification, effective-interest rules, and fair-value routing, then post the balanced draft through the application service.",
				Inputs:                 []string{"host-approved category and business-model/SPPI evidence references", "initial carrying amount and dated contractual cash flows or approved EIR", "opening gross carrying amount and allowance", "stage, measurement period, principal repayments, and fair value where required", "company, journal, and category-specific account mappings", "authorized application principal"},
				Steps:                  []string{"validate the host classification and measurement inputs", "derive EIR from dated contractual cash flows when supplied", "apply effective interest to AC/FVOCI and net carrying amount for Stage 3", "route FVOCI remeasurement to OCI or FVTPL remeasurement to P&L", "build and post a balanced idempotent journal through the public application service"},
				Outputs:                []string{"measured carrying amount and interest income", "fair-value movement by destination", "posted application journal"},
				RequirementIDs:         []string{"IFRS9-CLASSIFICATION-MEASUREMENT-01", "IFRS9-ECL-POSTING-01"},
				RequiredCapabilities:   []standards.Capability{standards.CapLedgerPosting},
				ApplicationContract:    standards.ApplicationLedgerService,
				RequiresJournalMapping: true,
				ProducesJournalEntries: true,
			},
			{
				ID: "versioned-general-ecl-posting", Name: "Versioned general ECL posting",
				Summary:                "Post AC and FVOCI allowance changes in one balanced journal through the public application service.",
				Inputs:                 []string{"completed versioned general ECL result", "AC impairment/allowance and FVOCI impairment/OCI-reserve mappings", "company and journal identifiers", "authorized application principal"},
				Steps:                  []string{"build category-specific impairment movements", "combine AC and FVOCI movements into one idempotent journal draft", "create and post the draft through the public application service"},
				Outputs:                []string{"posted category-specific ECL journal"},
				RequirementIDs:         []string{"IFRS9-ECL-GENERAL-VERSIONED-01", "IFRS9-ECL-POSTING-01"},
				RequiredCapabilities:   []standards.Capability{standards.CapLedgerPosting},
				ApplicationContract:    standards.ApplicationLedgerService,
				RequiresJournalMapping: true,
				ProducesJournalEntries: true,
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
	if err := m.ValidateDependencies(ports); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ports = ports
	return nil
}

func (m *Module) PostSubsequentMeasurement(
	ctx context.Context,
	result internalifrs9.MeasurementResult,
	accounts internalifrs9.MeasurementAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
	idempotencyKey string,
) (publicapplication.EntryResult, error) {
	m.mu.RLock()
	ports := m.ports
	m.mu.RUnlock()
	if ports == nil || ports.Application() == nil {
		return publicapplication.EntryResult{}, fmt.Errorf("IFRS 9: module ports and application service are required")
	}

	entry, err := internalifrs9.BuildMeasurementEntry(result, accounts, companyID, journalID, createdBy)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	lines, err := convertPostingLines(entry.Lines)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	req := standards.StandardPostingRequest{
		StandardRef:    "IFRS-9-SUBSEQUENT-MEASUREMENT",
		EntityID:       result.InstrumentID.String(),
		IdempotencyKey: idempotencyKey,
		CompanyID:      types.CompanyID(companyID),
		JournalID:      types.JournalID(journalID),
		EffectiveDate:  entry.EntryDate,
		PostingDate:    entry.EntryDate,
		Description:    entry.Reference,
		Lines:          lines,
		Classification: ledger.EntryClassRegular,
	}

	return ports.Posting().PostStandardEntry(ctx, req)
}

func (m *Module) PostVersionedECL(
	ctx context.Context,
	result internalifrs9.VersionedECLResult,
	accounts internalifrs9.ECLCategoryAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
	idempotencyKey string,
) (publicapplication.EntryResult, error) {
	m.mu.RLock()
	ports := m.ports
	m.mu.RUnlock()
	if ports == nil || ports.Application() == nil {
		return publicapplication.EntryResult{}, fmt.Errorf("IFRS 9: module ports and application service are required")
	}

	entry, err := internalifrs9.BuildVersionedECLApplicationEntry(result, accounts, companyID, journalID, createdBy)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	lines, err := convertPostingLines(entry.Lines)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}

	entityID := result.InputFingerprint
	if entityID == "" {
		entityID = entry.DocumentReference
	}

	req := standards.StandardPostingRequest{
		StandardRef:    "IFRS-9-ECL",
		EntityID:       entityID,
		IdempotencyKey: idempotencyKey,
		CompanyID:      types.CompanyID(companyID),
		JournalID:      types.JournalID(journalID),
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
