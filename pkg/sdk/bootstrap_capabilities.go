package sdk

import (
	"fmt"

	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	"github.com/osmai4603-collab/accounting_engine/internal/sdkapplication"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
)

// BootstrapCapability names an optional engine capability that a host can
// require at build time. Requiring a capability enforces:
//
//	Enabled(capability) => Available(capability)
//
// Build fails fast with an actionable error when the dependency is missing, so
// the engine never starts in a partially configured state.
type BootstrapCapability string

const (
	BootstrapReporting      BootstrapCapability = "reporting"
	BootstrapGovernance     BootstrapCapability = "governance"
	BootstrapStandards      BootstrapCapability = "standards"
	BootstrapEventIngestion BootstrapCapability = "event_ingestion"
	BootstrapSubledgers     BootstrapCapability = "subledgers"
	BootstrapPostingPolicy  BootstrapCapability = "posting_policy"
)

func (c BootstrapCapability) valid() bool {
	switch c {
	case BootstrapReporting, BootstrapGovernance, BootstrapStandards,
		BootstrapEventIngestion, BootstrapSubledgers, BootstrapPostingPolicy:
		return true
	default:
		return false
	}
}

// WithRequiredCapabilities declares capabilities that must be fully available
// before the engine is built. It is a fail-fast contract, not an activation
// switch: dependencies must still be supplied through their own options.
func WithRequiredCapabilities(capabilities ...BootstrapCapability) Option {
	return func(b *EngineBuilder) error {
		for _, capability := range capabilities {
			if !capability.valid() {
				return fmt.Errorf("bootstrap: unknown required capability %q", capability)
			}
			if b.hasRequiredCapability(capability) {
				continue
			}
			b.requiredCapabilities = append(b.requiredCapabilities, capability)
		}
		return nil
	}
}

func (b *EngineBuilder) hasRequiredCapability(capability BootstrapCapability) bool {
	for _, existing := range b.requiredCapabilities {
		if existing == capability {
			return true
		}
	}
	return false
}

// validateRequiredCapabilities checks every explicitly required capability
// against the configured dependency sources. It reports all missing
// dependencies at once with the option or port needed to fix each one.
func (b *EngineBuilder) validateRequiredCapabilities() []error {
	var errs []error
	for _, capability := range b.requiredCapabilities {
		var err error
		switch capability {
		case BootstrapReporting:
			err = b.validateReportingAvailability()
		case BootstrapGovernance:
			err = b.validateGovernanceAvailability()
		case BootstrapStandards:
			err = b.validateStandardsAvailability()
		case BootstrapEventIngestion:
			err = b.validateEventIngestionAvailability()
		case BootstrapSubledgers:
			err = b.validateSubledgersAvailability()
		case BootstrapPostingPolicy:
			err = b.validatePostingPolicyAvailability()
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (b *EngineBuilder) validateReportingAvailability() error {
	if b.reportingStore != nil {
		return nil
	}
	if b.portsConfigured && b.ports.Balance != nil && b.ports.MasterData != nil && b.ports.Entries != nil {
		return nil
	}
	return fmt.Errorf(
		"bootstrap: capability %q is required but unavailable: configure WithReporting(store) or supply Balance, MasterData and Entries ports",
		BootstrapReporting,
	)
}

func (b *EngineBuilder) validateGovernanceAvailability() error {
	if b.governanceStore != nil {
		return nil
	}
	if b.portsConfigured && b.ports.Audit != nil {
		return nil
	}
	return fmt.Errorf(
		"bootstrap: capability %q is required but unavailable: configure WithGovernance(store) or supply the Audit port",
		BootstrapGovernance,
	)
}

func (b *EngineBuilder) validateStandardsAvailability() error {
	if len(b.standards) == 0 {
		return fmt.Errorf(
			"bootstrap: capability %q is required but no standard module is configured: call WithStandard(module)",
			BootstrapStandards,
		)
	}
	if b.portsConfigured {
		var missing []string
		if b.ports.Entries == nil {
			missing = append(missing, "Entries")
		}
		if b.ports.MasterData == nil {
			missing = append(missing, "MasterData")
		}
		if len(missing) > 0 {
			return fmt.Errorf(
				"bootstrap: capability %q requires complete module ports; missing: %v",
				BootstrapStandards, missing,
			)
		}
	}
	return nil
}

func (b *EngineBuilder) validateEventIngestionAvailability() error {
	if b.portsConfigured {
		var missing []string
		if b.ports.Events.Idempotency == nil {
			missing = append(missing, "Events.Idempotency")
		}
		if b.ports.Events.Mappings == nil {
			missing = append(missing, "Events.Mappings")
		}
		if len(missing) > 0 {
			return fmt.Errorf(
				"bootstrap: capability %q requires durable event stores; missing: %v",
				BootstrapEventIngestion, missing,
			)
		}
		return nil
	}
	if b.coreStore == nil {
		return fmt.Errorf("bootstrap: capability %q requires a storage source", BootstrapEventIngestion)
	}
	if _, ok := b.coreStore.(PublicEventIdempotencyStore); !ok {
		return fmt.Errorf(
			"bootstrap: capability %q requires an event idempotency store",
			BootstrapEventIngestion,
		)
	}
	if _, ok := b.coreStore.(PublicEventMappingStore); !ok {
		return fmt.Errorf(
			"bootstrap: capability %q requires an event mapping store",
			BootstrapEventIngestion,
		)
	}
	return nil
}

func (b *EngineBuilder) validateSubledgersAvailability() error {
	if b.portsConfigured {
		return fmt.Errorf(
			"bootstrap: capability %q is unavailable through WithStoragePorts: subledger stores and control-account readers are not part of storage.Ports",
			BootstrapSubledgers,
		)
	}
	if _, ok := b.coreStore.(subledgers.Repository); !ok {
		return fmt.Errorf(
			"bootstrap: capability %q requires a storage source that implements the subledger repository",
			BootstrapSubledgers,
		)
	}
	return nil
}

func (b *EngineBuilder) validatePostingPolicyAvailability() error {
	if !b.postingConfig.RequirePolicy {
		return fmt.Errorf(
			"bootstrap: capability %q is required but posting policy enforcement is disabled: set WithPostingConfig(PostingConfig{RequirePolicy: true})",
			BootstrapPostingPolicy,
		)
	}
	if b.portsConfigured {
		if sdkapplication.HasPrivatePostingPolicy(b.ports) {
			return nil
		}
		return fmt.Errorf(
			"bootstrap: capability %q requires a policy provider; the adapter must expose posting capabilities with PolicyUnit",
			BootstrapPostingPolicy,
		)
	}
	if _, ok := b.coreStore.(policy.Repository); !ok {
		return fmt.Errorf(
			"bootstrap: capability %q requires a store that implements the policy repository",
			BootstrapPostingPolicy,
		)
	}
	return nil
}
