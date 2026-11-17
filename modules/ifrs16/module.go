package ifrs16

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
		ID:          "ifrs16",
		Name:        "IFRS 16",
		Version:     "1.1.0",
		Provides:    []standards.Capability{standards.CapLeaseAccounting},
		Description: "Bounded lessee and lessor lease measurement workflows, lessee exemptions, and disclosure payloads; not a claim of IFRS 16 compliance.",
		Profile: &standards.StandardProfile{
			Standard:      "IFRS 16",
			Edition:       "Not configured; host must select the applicable issued edition",
			Amendments:    []string{"Not configured; host must select applicable amendments"},
			EffectiveDate: "Not configured; host must supply the applicable effective date",
			Jurisdiction:  "Not configured; host must identify its reporting jurisdiction",
			Scope:         "Limited: lessee initial measurement, dated liability/ROU schedules, and non-separate modification remeasurement; host-classified lessor operating and finance lease measurement including straight-line income, net investment in lease, and implicit-rate finance income; short-term and low-value exemption expense schedules; and structured disclosure payload builders from host-supplied inputs. Lease classification, exemption elections, and contract identification remain host decisions.",
			SupportedRequirements: []standards.StandardRequirement{
				{ID: "IFRS16-INITIAL-01", Summary: "Measure initial lease liability and right-of-use asset from supplied payment terms and discount rate.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/lease.go:NewLeaseContract", TestEvidence: "internal/ifrs16/lease_test.go"},
				{ID: "IFRS16-SCHEDULE-01", Summary: "Calculate dated interest, principal, liability, and ROU depreciation schedules.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/lease.go:LeaseContract.AmortizationSchedule", TestEvidence: "internal/ifrs16/lease_test.go"},
				{ID: "IFRS16-MODIFICATION-01", Summary: "Remeasure supplied remaining cash flows for a non-separate modification using a supplied revised discount rate and supplied carrying amounts.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/lease.go:LeaseContract.RecalculateModification", TestEvidence: "internal/ifrs16/lease_test.go:TestModificationRecalculationAndEntry"},
				{ID: "IFRS16-LESSOR-OPERATING-01", Summary: "Recognize lessor operating lease income straight-line over the lease term with a deferred-income/accrued-rent contra while the retained leased asset continues to depreciate; initial direct costs join the depreciated asset base.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/lessor.go:OperatingLeaseContract.StraightLineSchedule", TestEvidence: "internal/ifrs16/lessor_test.go:TestOperatingLeaseStraightLineSchedule"},
				{ID: "IFRS16-LESSOR-FINANCE-01", Summary: "Derecognize the leased asset, measure the net investment in the lease (present value of receipts plus unguaranteed residual plus initial direct costs), and allocate finance income at the implicit rate until the closing balance equals the residual.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/lessor.go:FinanceLeaseContract.FinanceIncomeSchedule", TestEvidence: "internal/ifrs16/lessor_test.go:TestFinanceLeaseNetInvestmentAndFinanceIncome"},
				{ID: "IFRS16-EXEMPTION-SHORTTERM-01", Summary: "Apply the short-term exemption as an alternative accounting treatment: at most a twelve-month term without a purchase option, charging payments to profit or loss straight-line with an accrued/prepaid contra.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/exemptions.go:ExemptibleLease.ExpenseSchedule", TestEvidence: "internal/ifrs16/exemptions_test.go:TestShortTermExemptionEligibilityAndExpense"},
				{ID: "IFRS16-EXEMPTION-LOWVALUE-01", Summary: "Apply the low-value exemption as an alternative accounting treatment for underlying assets at or below a host-configurable individual-value threshold, charging payments to profit or loss straight-line.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/exemptions.go:NewExemptibleLease", TestEvidence: "internal/ifrs16/exemptions_test.go:TestLowValueExemptionThresholdAndExpense"},
				{ID: "IFRS16-DISCLOSURE-PAYLOAD-01", Summary: "Build structured IFRS 16 disclosure payloads for the P9-05 framework: lease expense and income from host-supplied balances, and the future lease payments maturity analysis derived from the lessee schedule.", Status: standards.RequirementLimited, Implementation: "internal/ifrs16/disclosures.go:BuildLeaseExpenseDisclosure", TestEvidence: "internal/ifrs16/disclosures_test.go"},
			},
			UnimplementedRequirements: []standards.StandardRequirement{
				{ID: "IFRS16-POSTING-WORKFLOW", Summary: "Persistent lease workflow with durable retry handling and application-layer posting.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS16-SALE-LEASEBACK", Summary: "Sale-and-leaseback transactions and subleases.", Status: standards.RequirementNotImplemented},
				{ID: "IFRS16-DISCLOSURE-PROVIDER", Summary: "Module-level DisclosureProvider retrieval from durable company lease state.", Status: standards.RequirementNotImplemented},
			},
			Exclusions: []string{
				"Lease identification, completeness of the lease population, lessor classification assessment (risk-and-reward judgments arrive as host-approved inputs), options and lease-term judgments, non-lease components, variable payments, and completeness/accuracy of supplied cash flows.",
				"Sale-and-leaseback transactions and subleases are excluded.",
				"Exemption elections are host accounting-policy choices for supplied asset classes; the engine validates mechanical eligibility (term length, purchase option, value threshold) only.",
				"Realization of the unguaranteed residual after lease end, index/rate reassessments, separate lease modifications, impairment, foreign-currency accounting, and full termination/transition accounting.",
				"Production persistence and posting through the application service; EntryBuilder, LessorEntryBuilder, and ExemptionEntryBuilder only resolve configured accounts and create deterministic drafts when a host capability is supplied.",
				"Module-level DisclosureProvider retrieval requires durable lease state; P11-03 supplies deterministic payload builders and retrieval is completed with the posting workflow (P11-05).",
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
			{
				ID: "lease-modification-remeasurement", Name: "Lease modification remeasurement",
				Summary:        "Remeasure a non-separate modification from host-supplied remaining payments, carrying amounts, and revised discount rate; not a legal modification assessment.",
				Inputs:         []string{"effective date", "remaining lease payments", "revised discount rate", "liability carrying amount", "ROU carrying amount"},
				Steps:          []string{"validate remaining payment dates and currency", "discount remaining payments on Actual/365", "adjust liability and ROU; recognize excess liability reduction as gain"},
				Outputs:        []string{"remeasured liability", "ROU adjustment", "modification gain where applicable"},
				RequirementIDs: []string{"IFRS16-MODIFICATION-01"},
			},
			{
				ID: "lessor-operating-lease-income", Name: "Lessor operating lease income",
				Summary:        "Calculate straight-line operating lease income, the deferred/accrued rent contra, and continued depreciation of the retained leased asset from host-approved classification.",
				Inputs:         []string{"commencement date", "lease receipts", "leased asset cost", "initial direct costs", "depreciation periods"},
				Steps:          []string{"validate receipts, currencies, and depreciation policy", "spread consideration straight-line over the lease term", "roll forward the asset and the deferred/accrued rent contra"},
				Outputs:        []string{"dated straight-line income schedule", "asset depreciation", "deferred-income or accrued-rent balance"},
				RequirementIDs: []string{"IFRS16-LESSOR-OPERATING-01"},
			},
			{
				ID: "lessor-finance-lease-measurement", Name: "Lessor finance lease measurement",
				Summary:        "Measure the net investment in the lease and allocate finance income at the implicit rate from a host-approved finance classification.",
				Inputs:         []string{"commencement date", "implicit rate", "lease receipts", "unguaranteed residual value", "asset carrying amount", "initial direct costs"},
				Steps:          []string{"validate receipts, residual, and carrying amounts", "discount receipts and residual at the implicit rate", "derecognize the asset and recognize the net investment", "allocate finance income over the term"},
				Outputs:        []string{"net investment in lease", "derecognition gain or loss", "dated finance income schedule closing at the residual value"},
				RequirementIDs: []string{"IFRS16-LESSOR-FINANCE-01"},
			},
			{
				ID: "lease-exemption-expense", Name: "Lessee exemption expense",
				Summary:        "Apply a host-elected short-term or low-value exemption and charge payments to profit or loss straight-line instead of recognizing a ROU asset and liability.",
				Inputs:         []string{"commencement date", "payments", "exemption type", "lease term months", "purchase option", "asset fair value and low-value threshold for low-value leases"},
				Steps:          []string{"validate mechanical eligibility for the elected exemption", "spread total payments straight-line over the lease term", "track accrued or prepaid difference against cash"},
				Outputs:        []string{"dated exemption expense schedule", "accrued lease expense or prepaid lease balance"},
				RequirementIDs: []string{"IFRS16-EXEMPTION-SHORTTERM-01", "IFRS16-EXEMPTION-LOWVALUE-01"},
			},
			{
				ID: "lease-disclosure-payloads", Name: "Lease disclosure payloads",
				Summary:        "Build IFRS 16 structured disclosure payloads for the P9-05 framework from host-supplied balances and the lessee schedule; not a company-data retrieval provider.",
				Inputs:         []string{"company, period, and currency", "host-supplied lease expense and income balances", "lease liability carrying amount and maturity buckets or a lessee contract"},
				Steps:          []string{"validate scope and currency consistency", "serialize supplied balances into the structured note", "derive undiscounted maturity bands from the lessee schedule where supplied"},
				Outputs:        []string{"lease expense and income disclosure payload", "future lease payments maturity disclosure payload"},
				RequirementIDs: []string{"IFRS16-DISCLOSURE-PAYLOAD-01"},
			},
		},
	}
}

func (m *Module) Name() string {
	return "IFRS 16"
}

func (m *Module) Version() string {
	return "1.1.0"
}

func (m *Module) Dependencies() []string {
	return nil
}

func (m *Module) ValidateDependencies(ports standards.ModulePorts) error {
	if err := standards.ValidateStandardModulePorts(ports); err != nil {
		return fmt.Errorf("IFRS 16: %w", err)
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
