package standards

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrDuplicateModule is returned when registering two modules with the same ID or name.
	ErrDuplicateModule = errors.New("standards: duplicate module registration")

	// ErrMissingDependency is returned when a required module dependency is not registered.
	ErrMissingDependency = errors.New("standards: missing module dependency")

	// ErrCircularDependency is returned when a cycle is detected in module dependencies.
	ErrCircularDependency = errors.New("standards: circular dependency detected")

	// ErrVersionConflict is returned when a required version constraint is not met.
	ErrVersionConflict = errors.New("standards: module version conflict")
)

// Registry manages standard modules, capability resolution, dependency ordering,
// and runtime module diagnostics.
type Registry struct {
	modules             map[ModuleID]StandardModule
	descriptors         map[ModuleID]ModuleDescriptor
	capabilities        map[Capability]ModuleID
	registeredOrder     []ModuleID
	resolvedOrder       []ModuleID
	resolved            bool
	disabledReasons     map[string]string
}

// NewRegistry creates a new, empty module registry.
func NewRegistry() *Registry {
	return &Registry{
		modules:         make(map[ModuleID]StandardModule),
		descriptors:     make(map[ModuleID]ModuleDescriptor),
		capabilities:    make(map[Capability]ModuleID),
		disabledReasons: make(map[string]string),
	}
}

// Register adds a module to the registry. If the module implements DescribedModule,
// its descriptor is used; otherwise, a default descriptor is synthesized from its
// Name(), Version(), and Dependencies().
func (r *Registry) Register(mod StandardModule) error {
	if mod == nil {
		return errors.New("standards: cannot register nil module")
	}

	var desc ModuleDescriptor
	if dm, ok := mod.(DescribedModule); ok {
		desc = dm.Descriptor()
	} else {
		// Synthesize descriptor from legacy StandardModule interface
		id := canonicalModuleID(mod.Name())
		var reqs []Requirement
		for _, dep := range mod.Dependencies() {
			reqs = append(reqs, Requirement{
				ID: canonicalModuleID(dep),
			})
		}
		desc = ModuleDescriptor{
			ID:       id,
			Name:     mod.Name(),
			Version:  mod.Version(),
			Requires: reqs,
		}
	}

	if desc.ID == "" {
		desc.ID = canonicalModuleID(desc.Name)
	}

	if _, exists := r.modules[desc.ID]; exists {
		return fmt.Errorf("%w: module ID %q (name: %q)", ErrDuplicateModule, desc.ID, desc.Name)
	}

	r.modules[desc.ID] = mod
	r.descriptors[desc.ID] = desc
	r.registeredOrder = append(r.registeredOrder, desc.ID)

	for _, cap := range desc.Provides {
		if cap != "" {
			r.capabilities[cap] = desc.ID
		}
	}

	r.resolved = false
	return nil
}

// Resolve analyzes dependencies, validates version requirements, checks for cycles,
// and computes the deterministic topological initialization order.
func (r *Registry) Resolve() ([]StandardModule, error) {
	// 1. Check for missing dependencies
	for _, id := range r.registeredOrder {
		desc := r.descriptors[id]
		for _, req := range desc.Requires {
			targetMod, exists := r.modules[req.ID]
			if !exists {
				if req.Optional {
					r.disabledReasons[fmt.Sprintf("module:%s:optional_dep:%s", id, req.ID)] = "optional dependency not loaded"
					continue
				}
				return nil, fmt.Errorf("%w: module %q requires %q which is not registered", ErrMissingDependency, id, req.ID)
			}
			// Version constraint check if specified
			if req.MinVersion != "" {
				targetDesc := r.descriptors[req.ID]
				if compareSemVer(targetDesc.Version, req.MinVersion) < 0 {
					return nil, fmt.Errorf("%w: module %q requires %q >= %s, found %s", ErrVersionConflict, id, req.ID, req.MinVersion, targetDesc.Version)
				}
			}
			_ = targetMod
		}
	}

	// 2. Cycle Detection using DFS graph coloring (0: unvisited, 1: visiting, 2: visited)
	color := make(map[ModuleID]int)
	var cyclePath []string

	var dfs func(curr ModuleID, path []ModuleID) bool
	dfs = func(curr ModuleID, path []ModuleID) bool {
		color[curr] = 1 // visiting
		path = append(path, curr)

		desc := r.descriptors[curr]
		for _, req := range desc.Requires {
			depID := req.ID
			if _, exists := r.modules[depID]; !exists {
				continue
			}
			if color[depID] == 1 {
				// Cycle found: reconstruct path
				cycleStart := 0
				for i, p := range path {
					if p == depID {
						cycleStart = i
						break
					}
				}
				for _, p := range path[cycleStart:] {
					cyclePath = append(cyclePath, string(p))
				}
				cyclePath = append(cyclePath, string(depID))
				return true
			}
			if color[depID] == 0 {
				if dfs(depID, path) {
					return true
				}
			}
		}

		color[curr] = 2 // visited
		return false
	}

	for _, id := range r.registeredOrder {
		if color[id] == 0 {
			if dfs(id, nil) {
				return nil, fmt.Errorf("%w: %s", ErrCircularDependency, strings.Join(cyclePath, " -> "))
			}
		}
	}

	// 3. Topological sort using Kahn's algorithm with deterministic tie-breaking
	inDegree := make(map[ModuleID]int)
	adj := make(map[ModuleID][]ModuleID)

	for _, id := range r.registeredOrder {
		inDegree[id] = 0
		adj[id] = nil
	}

	for _, id := range r.registeredOrder {
		desc := r.descriptors[id]
		for _, req := range desc.Requires {
			depID := req.ID
			if _, exists := r.modules[depID]; exists {
				// depID must come before id
				adj[depID] = append(adj[depID], id)
				inDegree[id]++
			}
		}
	}

	var queue []ModuleID
	for _, id := range r.registeredOrder {
		if inDegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	sortModuleIDs(queue)

	var ordered []ModuleID
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		ordered = append(ordered, curr)

		for _, neighbor := range adj[curr] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
		sortModuleIDs(queue)
	}

	if len(ordered) != len(r.registeredOrder) {
		return nil, fmt.Errorf("%w: unresolved dependencies remained during topological sort", ErrCircularDependency)
	}

	r.resolvedOrder = ordered
	r.resolved = true

	result := make([]StandardModule, len(ordered))
	for i, id := range ordered {
		result[i] = r.modules[id]
	}
	return result, nil
}

// GetModule returns the module registered under the given ID.
func (r *Registry) GetModule(id ModuleID) (StandardModule, bool) {
	mod, ok := r.modules[id]
	return mod, ok
}

// GetDescriptor returns the descriptor registered under the given ID.
func (r *Registry) GetDescriptor(id ModuleID) (ModuleDescriptor, bool) {
	desc, ok := r.descriptors[id]
	return desc, ok
}

// HasCapability reports whether a capability is actively provided by a registered module.
func (r *Registry) HasCapability(cap Capability) bool {
	_, ok := r.capabilities[cap]
	return ok
}

// GetCapabilityProvider returns the module ID providing the requested capability.
func (r *Registry) GetCapabilityProvider(cap Capability) (ModuleID, bool) {
	id, ok := r.capabilities[cap]
	return id, ok
}

// ActiveCapabilities lists all capabilities provided by registered modules in sorted order.
func (r *Registry) ActiveCapabilities() []Capability {
	var caps []Capability
	for cap := range r.capabilities {
		caps = append(caps, cap)
	}
	sort.Slice(caps, func(i, j int) bool {
		return string(caps[i]) < string(caps[j])
	})
	return caps
}

// Diagnostics returns an inspectable snapshot of all active modules, capabilities,
// dependency resolution ordering, and disabled capability reasons.
func (r *Registry) Diagnostics() EngineDiagnostics {
	order := r.resolvedOrder
	if !r.resolved {
		order = r.registeredOrder
	}

	activeMods := make([]ModuleDescriptor, 0, len(order))
	for _, id := range order {
		activeMods = append(activeMods, r.descriptors[id])
	}

	providers := make(map[Capability]ModuleID, len(r.capabilities))
	for k, v := range r.capabilities {
		providers[k] = v
	}

	reasons := make(map[string]string, len(r.disabledReasons))
	for k, v := range r.disabledReasons {
		reasons[k] = v
	}

	return EngineDiagnostics{
		ActiveModules:       activeMods,
		ActiveCapabilities:  r.ActiveCapabilities(),
		CapabilityProviders: providers,
		DependencyOrder:     order,
		DisabledReasons:     reasons,
	}
}

// Helper utilities:

func canonicalModuleID(name string) ModuleID {
	cleaned := strings.ToLower(strings.TrimSpace(name))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, "_", "")
	return ModuleID(cleaned)
}

func sortModuleIDs(ids []ModuleID) {
	sort.Slice(ids, func(i, j int) bool {
		return string(ids[i]) < string(ids[j])
	})
}

// compareSemVer performs basic comparison of "X.Y.Z" versions.
// Returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
func compareSemVer(v1, v2 string) int {
	v1 = strings.TrimPrefix(v1, "v")
	v2 = strings.TrimPrefix(v2, "v")
	p1 := strings.Split(v1, ".")
	p2 := strings.Split(v2, ".")

	maxLen := len(p1)
	if len(p2) > maxLen {
		maxLen = len(p2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(p1) {
			_, _ = fmt.Sscanf(p1[i], "%d", &n1)
		}
		if i < len(p2) {
			_, _ = fmt.Sscanf(p2[i], "%d", &n2)
		}
		if n1 < n2 {
			return -1
		}
		if n1 > n2 {
			return 1
		}
	}
	return 0
}
