package standards

import (
	"fmt"
	"strings"
	"time"
)

// ModuleID is a unique, machine-readable identifier for a module
// (e.g., "ifrs15", "ias12", "ifrs16", "ifrs9").
type ModuleID string

// Capability is a unique identifier for a business or accounting feature
// provided by one or more modules (e.g., "revenue_recognition", "deferred_tax").
type Capability string

const (
	CapLedgerPosting       Capability = "ledger_posting"
	CapMasterData          Capability = "masterdata_management"
	CapReporting           Capability = "reporting"
	CapGovernance          Capability = "governance"
	CapAuditTrail          Capability = "audit_trail"
	CapApproval            Capability = "approval_fingerprint"
	CapCurrencyTranslation Capability = "currency_translation"
	CapScheduledJobs       Capability = "scheduled_jobs"

	CapReconciliation Capability = "reconciliation"
	CapConsolidation  Capability = "consolidation"

	CapRevenueRecognition   Capability = "revenue_recognition"
	CapDeferredTax          Capability = "deferred_tax"
	CapLeaseAccounting      Capability = "lease_accounting"
	CapFinancialInstruments Capability = "financial_instruments"
)

type RequirementStatus string

const (
	RequirementLimited        RequirementStatus = "limited"
	RequirementPartial        RequirementStatus = "partial"
	RequirementNotImplemented RequirementStatus = "not_implemented"
)

type ApplicationContract string

const (
	ApplicationLedgerService ApplicationContract = "pkg/spi/application.Service"
)

// StandardRequirement ties a bounded accounting requirement to its code and
// test evidence, or explicitly marks it as not implemented.
type StandardRequirement struct {
	ID             string            `json:"id"`
	Summary        string            `json:"summary"`
	Status         RequirementStatus `json:"status"`
	Implementation string            `json:"implementation,omitempty"`
	TestEvidence   string            `json:"test_evidence,omitempty"`
}

// StandardProfile makes the standards and jurisdictional scope of a module
// explicit. "Not configured" values are declarations that activation requires
// host configuration, not defaults.
type StandardProfile struct {
	Standard                  string                `json:"standard"`
	Edition                   string                `json:"edition"`
	Amendments                []string              `json:"amendments"`
	EffectiveDate             string                `json:"effective_date"`
	Jurisdiction              string                `json:"jurisdiction"`
	Scope                     string                `json:"scope"`
	SupportedRequirements     []StandardRequirement `json:"supported_requirements"`
	UnimplementedRequirements []StandardRequirement `json:"unimplemented_requirements,omitempty"`
	Exclusions                []string              `json:"exclusions"`
}

func (p StandardProfile) Validate() error {
	if strings.TrimSpace(p.Standard) == "" ||
		strings.TrimSpace(p.Edition) == "" ||
		strings.TrimSpace(p.EffectiveDate) == "" ||
		strings.TrimSpace(p.Jurisdiction) == "" ||
		strings.TrimSpace(p.Scope) == "" ||
		len(p.Amendments) == 0 ||
		len(p.SupportedRequirements) == 0 ||
		len(p.Exclusions) == 0 {
		return fmt.Errorf("standards: profile must declare standard, edition, amendments, effective date, jurisdiction, scope, requirements, and exclusions")
	}

	seen := make(map[string]struct{}, len(p.SupportedRequirements)+len(p.UnimplementedRequirements))
	for _, amendment := range p.Amendments {
		if strings.TrimSpace(amendment) == "" {
			return fmt.Errorf("standards: profile amendments cannot be empty")
		}
	}
	for _, exclusion := range p.Exclusions {
		if strings.TrimSpace(exclusion) == "" {
			return fmt.Errorf("standards: profile exclusions cannot be empty")
		}
	}
	for _, requirement := range p.SupportedRequirements {
		if strings.TrimSpace(requirement.ID) == "" || strings.TrimSpace(requirement.Summary) == "" {
			return fmt.Errorf("standards: profile requirement ID and summary are required")
		}
		if _, exists := seen[requirement.ID]; exists {
			return fmt.Errorf("standards: duplicate profile requirement %q", requirement.ID)
		}
		seen[requirement.ID] = struct{}{}

		switch requirement.Status {
		case RequirementLimited, RequirementPartial:
			if strings.TrimSpace(requirement.Implementation) == "" || strings.TrimSpace(requirement.TestEvidence) == "" {
				return fmt.Errorf("standards: requirement %q needs implementation and test evidence", requirement.ID)
			}
		case RequirementNotImplemented:
			return fmt.Errorf("standards: unimplemented requirement %q must be listed separately", requirement.ID)
		default:
			return fmt.Errorf("standards: requirement %q has unsupported status %q", requirement.ID, requirement.Status)
		}
	}
	for _, requirement := range p.UnimplementedRequirements {
		if strings.TrimSpace(requirement.ID) == "" || strings.TrimSpace(requirement.Summary) == "" {
			return fmt.Errorf("standards: unimplemented requirement ID and summary are required")
		}
		if _, exists := seen[requirement.ID]; exists {
			return fmt.Errorf("standards: duplicate profile requirement %q", requirement.ID)
		}
		seen[requirement.ID] = struct{}{}
		if requirement.Status != RequirementNotImplemented ||
			requirement.Implementation != "" || requirement.TestEvidence != "" {
			return fmt.Errorf("standards: unimplemented requirement %q must have not_implemented status and no implementation evidence", requirement.ID)
		}
	}
	return nil
}

// IsConfigured reports whether the host has pinned an edition, amendments,
// effective date, and jurisdiction instead of leaving explicit placeholders.
func (p StandardProfile) IsConfigured() bool {
	if p.Validate() != nil {
		return false
	}
	if _, err := time.Parse("2006-01-02", p.EffectiveDate); err != nil {
		return false
	}
	values := []string{p.Edition, p.EffectiveDate, p.Jurisdiction}
	values = append(values, p.Amendments...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), "not configured") {
			return false
		}
	}
	return true
}

// WorkflowDescriptor describes a bounded use case without asserting that it
// posts accounting entries. Posting must be an explicit workflow backed by
// application capabilities and a real journal mapping.
type WorkflowDescriptor struct {
	ID                     string              `json:"id"`
	Name                   string              `json:"name"`
	Summary                string              `json:"summary"`
	Inputs                 []string            `json:"inputs"`
	Steps                  []string            `json:"steps"`
	Outputs                []string            `json:"outputs"`
	RequirementIDs         []string            `json:"requirement_ids"`
	RequiredCapabilities   []Capability        `json:"required_capabilities,omitempty"`
	ApplicationContract    ApplicationContract `json:"application_contract,omitempty"`
	RequiresJournalMapping bool                `json:"requires_journal_mapping"`
	ProducesJournalEntries bool                `json:"produces_journal_entries"`
}

func (w WorkflowDescriptor) Validate(profile StandardProfile) error {
	if strings.TrimSpace(w.ID) == "" || strings.TrimSpace(w.Name) == "" ||
		strings.TrimSpace(w.Summary) == "" || len(w.Inputs) == 0 ||
		len(w.Steps) == 0 || len(w.Outputs) == 0 || len(w.RequirementIDs) == 0 {
		return fmt.Errorf("standards: workflow must declare ID, name, summary, inputs, steps, outputs, and requirements")
	}
	for _, values := range [][]string{w.Inputs, w.Steps, w.Outputs, w.RequirementIDs} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("standards: workflow %q contains an empty input, step, output, or requirement", w.ID)
			}
		}
	}
	if w.ProducesJournalEntries &&
		(w.ApplicationContract != ApplicationLedgerService ||
			!containsCapability(w.RequiredCapabilities, CapLedgerPosting) ||
			!w.RequiresJournalMapping) {
		return fmt.Errorf("standards: posting workflow %q requires the application contract, ledger-posting capability, and configured journal mapping", w.ID)
	}

	requirements := make(map[string]struct{}, len(profile.SupportedRequirements))
	for _, requirement := range profile.SupportedRequirements {
		requirements[requirement.ID] = struct{}{}
	}
	for _, requirementID := range w.RequirementIDs {
		if _, ok := requirements[requirementID]; !ok {
			return fmt.Errorf("standards: workflow %q references unknown requirement %q", w.ID, requirementID)
		}
	}
	return nil
}

func containsCapability(capabilities []Capability, wanted Capability) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

// Requirement describes a dependency of a module on another module.
type Requirement struct {
	// ID is the target module's machine-readable identifier.
	ID ModuleID `json:"id"`
	// MinVersion is the minimum acceptable SemVer version string (e.g. "1.0.0").
	MinVersion string `json:"min_version,omitempty"`
	// Optional indicates whether the dependency is optional.
	Optional bool `json:"optional,omitempty"`
}

// ModuleDescriptor provides metadata for module discovery, version negotiation,
// dependency resolution, and capability exposure.
type ModuleDescriptor struct {
	// ID is the unique machine-readable identifier.
	ID ModuleID `json:"id"`
	// Name is the human-readable display name.
	Name string `json:"name"`
	// Version is the SemVer version of the module.
	Version string `json:"version"`
	// Requires lists the modules required by this module.
	Requires []Requirement `json:"requires,omitempty"`
	// Provides lists the capabilities exported by this module.
	Provides []Capability `json:"provides,omitempty"`
	// Compatibility specifies engine compatibility constraints (e.g. ">=1.0.0").
	Compatibility string `json:"compatibility,omitempty"`
	// Description summarizes the purpose and scope of the module.
	Description string `json:"description,omitempty"`
	// Profile is the standards edition, jurisdiction, and supported-scope declaration.
	Profile *StandardProfile `json:"profile,omitempty"`
	// Workflows lists bounded use cases exposed by the module.
	Workflows []WorkflowDescriptor `json:"workflows,omitempty"`
}

// DescribedModule is an optional interface implemented by modules that declare
// rich metadata via ModuleDescriptor.
type DescribedModule interface {
	StandardModule
	Descriptor() ModuleDescriptor
}
