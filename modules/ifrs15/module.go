package ifrs15

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	internalifrs15 "github.com/osmai4603-collab/accounting_engine/internal/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

type Module struct {
	mu    sync.RWMutex
	ports standards.ModulePorts
	store internalifrs15.ContractStore
}

type Option func(*Module)

// WithContractStore configures the module with a persistent or custom ContractStore.
func WithContractStore(store internalifrs15.ContractStore) Option {
	return func(m *Module) {
		m.store = store
	}
}

// NewModule creates an IFRS 15 StandardModule instance.
func NewModule(opts ...Option) *Module {
	m := &Module{
		store: internalifrs15.NewInMemoryContractStore(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

var (
	_ standards.StandardModule     = (*Module)(nil)
	_ standards.DescribedModule    = (*Module)(nil)
	_ standards.DisclosureProvider = (*Module)(nil)
)

func (m *Module) Descriptor() standards.ModuleDescriptor {
	return standards.ModuleDescriptor{
		ID:          "ifrs15",
		Name:        "IFRS 15",
		Version:     "1.1.0",
		Provides:    []standards.Capability{standards.CapRevenueRecognition},
		Description: "Expanded IFRS 15 revenue measurement, variable consideration, financing component, contract modifications, durable contract lifecycle, and financial disclosures.",
		Profile: &standards.StandardProfile{
			Standard:      "IFRS 15",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Expanded: host-assessed contract inputs, relative standalone selling-price allocation, variable consideration (expected value and most likely amount) with constraining under IFRS 15.56-58, significant financing component adjustment and amortization under IFRS 15.60-65, three-way contract modifications under IFRS 15.18-21, durable contract state persistence across process restarts via ContractStore, application-backed posting, and structured disclosures under IFRS 15.113-128.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS15-5STEP-01", Summary: "Validate host-supplied five-step inputs for a supported fixed-price, distinct-obligation contract.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/workflow.go:NewAssessedContract", TestEvidence: "internal/ifrs15/workflow_test.go:TestAssessedContractValidatesFiveStepInputs"},
				{ID: "IFRS15-ALLOC-01", Summary: "Allocate transaction price using relative standalone selling prices.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/revenue.go:NewContract", TestEvidence: "internal/ifrs15/revenue_test.go:TestRelativeSSPWeightedAllocation"},
				{ID: "IFRS15-SCHEDULE-01", Summary: "Build daily or monthly recognition schedules for point-in-time and over-time obligations.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/revenue.go:Contract.Schedule", TestEvidence: "internal/ifrs15/revenue_test.go:TestDailyAndMonthlyRecognitionSchedules"},
				{ID: "IFRS15-POST-01", Summary: "Post externally measured satisfaction events through a configured application capability and real journal mapping before confirming contract state.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/workflow.go:RevenueWorkflow.Recognize", TestEvidence: "internal/ifrs15/workflow_test.go:TestRecognitionCommitsOnlyAfterPostingAndRetriesIdempotently"},
				{ID: "IFRS15-VARIABLE-CONSIDERATION", Summary: "Estimate and constrain variable consideration under IFRS 15.56-58 using Expected Value or Most Likely Amount with subsequent reassessment.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/variable.go:EstimateVariableConsideration", TestEvidence: "internal/ifrs15/variable_test.go:TestVariableConsideration_Constraining"},
				{ID: "IFRS15-FINANCING", Summary: "Significant financing component identification, practical expedient, and compound interest schedule under IFRS 15.60-65.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/financing.go:CalculateFinancingComponent", TestEvidence: "internal/ifrs15/financing_test.go:TestFinancingComponent_CustomerFinancing_AdvancePayment"},
				{ID: "IFRS15-CONTRACT-MODIFICATION", Summary: "Three-way contract modification handling under IFRS 15.18-21 (separate contract, prospective replacement, cumulative catch-up).", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/modification.go:ApplyProspectiveModification", TestEvidence: "internal/ifrs15/modification_test.go:TestContractModification_TypeB_Prospective"},
				{ID: "IFRS15-DURABLE-WORKFLOW", Summary: "Durable contract workflow state, lifecycle statuses (Draft, Active, Modified, Satisfied), and recovery across process restarts via ContractStore.", Status: standards.RequirementLimited, Implementation: "internal/ifrs15/store.go:InMemoryContractStore", TestEvidence: "internal/ifrs15/store_test.go:TestDurableWorkflowRecoveryAcrossRestart"},
				{ID: "IFRS15-DISCLOSURE", Summary: "Structured financial statement disclosures under IFRS 15.113-128 (revenue disaggregation, contract balances, remaining performance obligations).", Status: standards.RequirementLimited, Implementation: "modules/ifrs15/module.go:Module.GetDisclosures", TestEvidence: "modules/ifrs15/module_test.go:TestModuleDisclosureProvider"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS15-PRINCIPAL-VS-AGENT", Summary: "Principal versus agent evaluation (IFRS 15.B34-B38) is evaluated by the host system.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS15-IP-LICENSING-COMPLEX", Summary: "Complex intellectual property licenses and sales-based/usage-based royalties (IFRS 15.B52-B63).", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Principal-versus-agent evaluations (IFRS 15.B34-B38) and complex intellectual property licensing (IFRS 15.B52-B63) are excluded from engine determination.",
				"Service delivery, control transfer, acceptance, and progress measurement are supplied and evidenced by the host; the engine does not determine them.",
			},
		},
		Workflows: []standards.WorkflowDescriptor{
			{
				ID: "revenue-five-step-input-validation", Name: "Five-step input validation",
				Summary:        "Validate host-assessed inputs for supported revenue contracts and allocate transaction price.",
				Inputs:         []string{"host assessment of contract approval, enforceable rights, payment terms, commercial substance, and collectibility", "transaction price (fixed or variable with constraint)", "distinct performance obligations and standalone selling prices"},
				Steps:          []string{"validate contract-identification and collectibility assertions", "evaluate variable consideration and financing components if present", "validate distinct obligation inputs", "allocate transaction price by relative standalone selling prices"},
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
				Steps:                  []string{"validate external evidence and obligation allocation", "construct a balanced journal draft from the real mapping", "post using the stable event idempotency key", "commit durable contract state only after posting succeeds"},
				Outputs:                []string{"confirmed contract state after successful posting", "posted recognition journal entry"},
				RequirementIDs:         []string{"IFRS15-POST-01", "IFRS15-DURABLE-WORKFLOW"},
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
			{
				ID: "revenue-variable-consideration", Name: "Variable consideration estimation and constraining",
				Summary:        "Estimate variable consideration via Expected Value or Most Likely Amount and apply reversal constraint.",
				Inputs:         []string{"estimation method", "outcomes and probabilities", "constraint limit and rationale", "as of date"},
				Steps:          []string{"calculate unconstrained estimate", "apply constraining limit if reversal risk exists", "produce final transaction price component"},
				Outputs:        []string{"variable consideration estimate", "final transaction price amount"},
				RequirementIDs: []string{"IFRS15-VARIABLE-CONSIDERATION"},
			},
			{
				ID: "revenue-financing-component", Name: "Significant financing component measurement",
				Summary:        "Evaluate time value of money, practical expedient, and periodic compound interest.",
				Inputs:         []string{"contract nominal amount", "annual discount rate", "transfer date and payment date", "financing type"},
				Steps:          []string{"check 1-year practical expedient", "calculate present value or future value", "generate compound amortization schedule"},
				Outputs:        []string{"adjusted transaction price", "financing schedule", "periodic interest amounts"},
				RequirementIDs: []string{"IFRS15-FINANCING"},
			},
			{
				ID: "revenue-contract-modification", Name: "Contract modification accounting",
				Summary:        "Process contract modifications under separate contract, prospective, or cumulative catch-up models.",
				Inputs:         []string{"existing contract", "modification type, additional consideration, and additional obligations", "progress measure for catch-up"},
				Steps:          []string{"verify modification type conditions", "adjust or reallocate transaction price", "generate catch-up journal draft if cumulative catch-up"},
				Outputs:        []string{"updated contract with Modified status", "catch-up journal entry (if applicable)"},
				RequirementIDs: []string{"IFRS15-CONTRACT-MODIFICATION"},
			},
			{
				ID: "revenue-disclosure", Name: "IFRS 15 structured disclosures",
				Summary:        "Generate structured note disclosures for revenue disaggregation, contract balances, and remaining performance obligations.",
				Inputs:         []string{"company ID", "reporting period", "functional currency"},
				Steps:          []string{"query durable contracts for period", "aggregate Point-in-Time and Over-Time revenues", "aggregate contract assets and liabilities", "compile remaining performance obligations"},
				Outputs:        []string{"disaggregation disclosure", "contract balances disclosure", "remaining obligations disclosure"},
				RequirementIDs: []string{"IFRS15-DISCLOSURE"},
			},
		},
	}
}

func (m *Module) Name() string {
	return "IFRS 15"
}

func (m *Module) Version() string {
	return "1.1.0"
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
	if err := m.ValidateDependencies(ports); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ports = ports
	if m.store == nil {
		m.store = internalifrs15.NewInMemoryContractStore()
	}
	return nil
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

func (m *Module) ContractStore() internalifrs15.ContractStore {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store
}

func (m *Module) SetContractStore(store internalifrs15.ContractStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
}

// GetDisclosures implements standards.DisclosureProvider for IFRS 15.
// Produces structured disclosures for:
// 1. IFRS15-REV-DISAGG: Disaggregation of revenue (Point in Time vs Over Time).
// 2. IFRS15-CONTRACT-BALANCES: Contract assets and contract liabilities.
// 3. IFRS15-REMAINING-OBLIGATIONS: Transaction price allocated to remaining performance obligations (IFRS 15.120).
func (m *Module) GetDisclosures(
	ctx context.Context,
	query reporting.DisclosureQuery,
) ([]reporting.StructuredDisclosure, error) {
	companyUUID := uuid.UUID(query.CompanyID)
	if companyUUID == uuid.Nil {
		return nil, fmt.Errorf("reporting: invalid company ID")
	}
	if query.StandardReference != "" && query.StandardReference != "IFRS 15" {
		return nil, nil
	}

	currency := strings.ToUpper(strings.TrimSpace(query.Currency))
	if currency == "" {
		currency = "USD"
	}

	store := m.ContractStore()
	var contracts []internalifrs15.Contract
	if store != nil {
		var err error
		contracts, err = store.ListContracts(ctx, companyUUID)
		if err != nil {
			return nil, fmt.Errorf("IFRS 15 disclosures: list contracts: %w", err)
		}
	}

	pointInTimeTotal := decimal.Zero
	overTimeTotal := decimal.Zero
	totalContractAssets := decimal.Zero
	totalContractLiabilities := decimal.Zero
	totalBilled := decimal.Zero
	totalRecognized := decimal.Zero
	totalRemainingPrice := decimal.Zero
	remainingObligationsCount := 0
	remainingBreakdown := make([]map[string]any, 0)

	for _, c := range contracts {
		if query.Currency != "" && !strings.EqualFold(c.Currency, query.Currency) {
			continue
		}
		if currency == "USD" && c.Currency != "" {
			currency = c.Currency
		}

		totalBilled = totalBilled.Add(c.BilledAmount.Amount())
		totalRecognized = totalRecognized.Add(c.RecognizedAmount.Amount())

		asset, liability, err := c.ContractBalances()
		if err == nil {
			totalContractAssets = totalContractAssets.Add(asset.Amount())
			totalContractLiabilities = totalContractLiabilities.Add(liability.Amount())
		}

		for _, obl := range c.Obligations {
			allocated := c.AllocatedPrices[obl.ID].Amount()
			var recognized decimal.Decimal
			if c.RecognizedPrices != nil {
				recognized = c.RecognizedPrices[obl.ID].Amount()
			}
			if obl.RecognitionPattern == internalifrs15.PointInTime {
				pointInTimeTotal = pointInTimeTotal.Add(recognized)
			} else {
				overTimeTotal = overTimeTotal.Add(recognized)
			}

			if allocated.GreaterThan(recognized) {
				remAmount := allocated.Sub(recognized)
				totalRemainingPrice = totalRemainingPrice.Add(remAmount)
				remainingObligationsCount++
				remainingBreakdown = append(remainingBreakdown, map[string]any{
					"contract_id":         c.ID.String(),
					"obligation_id":       obl.ID.String(),
					"description":         obl.Description,
					"pattern":             string(obl.RecognitionPattern),
					"allocated_amount":    allocated.StringFixed(2),
					"recognized_amount":   recognized.StringFixed(2),
					"unsatisfied_amount":  remAmount.StringFixed(2),
				})
			}
		}
	}

	totalRevenue := pointInTimeTotal.Add(overTimeTotal)
	source := []reporting.DisclosureSource{
		{Kind: "subledger", Reference: "ifrs15_revenue_contracts"},
	}

	disclosures := []reporting.StructuredDisclosure{
		{
			CompanyID:         query.CompanyID,
			Code:              "IFRS15-REV-DISAGG",
			Title:             "Disaggregation of Revenue from Contracts with Customers",
			StandardReference: "IFRS 15",
			Period:            query.Period,
			Currency:          currency,
			Version:           "1.0.0",
			Source:            source,
			Data: map[string]any{
				"point_in_time_revenue": pointInTimeTotal.StringFixed(2),
				"over_time_revenue":     overTimeTotal.StringFixed(2),
				"total_revenue":         totalRevenue.StringFixed(2),
				"contracts_count":       len(contracts),
			},
			Narrative: "Revenue from contracts with customers disaggregated by pattern of transfer in accordance with IFRS 15.114-115.",
		},
		{
			CompanyID:         query.CompanyID,
			Code:              "IFRS15-CONTRACT-BALANCES",
			Title:             "Contract Assets and Contract Liabilities",
			StandardReference: "IFRS 15",
			Period:            query.Period,
			Currency:          currency,
			Version:           "1.0.0",
			Source:            source,
			Data: map[string]any{
				"total_contract_assets":      totalContractAssets.StringFixed(2),
				"total_contract_liabilities": totalContractLiabilities.StringFixed(2),
				"total_billed":               totalBilled.StringFixed(2),
				"total_recognized":           totalRecognized.StringFixed(2),
				"contracts_count":            len(contracts),
			},
			Narrative: "Balances of contract assets and contract liabilities resulting from contracts with customers in accordance with IFRS 15.116-118.",
		},
		{
			CompanyID:         query.CompanyID,
			Code:              "IFRS15-REMAINING-OBLIGATIONS",
			Title:             "Transaction Price Allocated to Remaining Performance Obligations",
			StandardReference: "IFRS 15",
			Period:            query.Period,
			Currency:          currency,
			Version:           "1.0.0",
			Source:            source,
			Data: map[string]any{
				"unsatisfied_amount": totalRemainingPrice.StringFixed(2),
				"obligations_count":  remainingObligationsCount,
				"breakdown":          remainingBreakdown,
			},
			Narrative: "Aggregate transaction price allocated to performance obligations that are unsatisfied (or partially unsatisfied) at reporting period end in accordance with IFRS 15.120.",
		},
	}

	return disclosures, nil
}
