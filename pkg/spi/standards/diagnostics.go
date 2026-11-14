package standards

// EngineDiagnostics provides an inspectable snapshot of the active modules,
// resolved capabilities, initialization order, and any disabled features or reasons.
type EngineDiagnostics struct {
	// ActiveModules lists all successfully resolved modules and their metadata.
	ActiveModules []ModuleDescriptor `json:"active_modules"`
	// ActiveCapabilities lists all capabilities actively provided by registered modules.
	ActiveCapabilities []Capability `json:"active_capabilities"`
	// CapabilityProviders maps each active capability to its providing module ID.
	CapabilityProviders map[Capability]ModuleID `json:"capability_providers"`
	// DependencyOrder lists module IDs in topological execution order.
	DependencyOrder []ModuleID `json:"dependency_order"`
	// DisabledReasons maps inactive capabilities or modules to the reason why they are unavailable.
	DisabledReasons map[string]string `json:"disabled_reasons,omitempty"`
}
