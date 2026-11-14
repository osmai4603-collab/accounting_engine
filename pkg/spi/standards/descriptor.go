package standards

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

	CapRevenueRecognition Capability = "revenue_recognition"
	CapDeferredTax         Capability = "deferred_tax"
	CapLeaseAccounting     Capability = "lease_accounting"
	CapFinancialInstruments Capability = "financial_instruments"
)

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
}

// DescribedModule is an optional interface implemented by modules that declare
// rich metadata via ModuleDescriptor.
type DescribedModule interface {
	StandardModule
	Descriptor() ModuleDescriptor
}

