package standards

import (
	"errors"
	"testing"
)

func TestRegistryRequiresCompleteProfileAndBoundedWorkflows(t *testing.T) {
	profile := StandardProfile{
		Standard:      "Test Standard",
		Edition:       "Host configured edition",
		Amendments:    []string{"Host configured amendments"},
		EffectiveDate: "Host configured effective date",
		Jurisdiction:  "Host configured jurisdiction",
		Scope:         "Limited test scope",
		SupportedRequirements: []StandardRequirement{{
			ID: "REQ-1", Summary: "A measured requirement", Status: RequirementLimited,
			Implementation: "internal/example/measure.go:Measure",
			TestEvidence:   "internal/example/measure_test.go:TestMeasure",
		}},
		UnimplementedRequirements: []StandardRequirement{{
			ID: "REQ-2", Summary: "A separately tracked unimplemented requirement",
			Status: RequirementNotImplemented,
		}},
		Exclusions: []string{"Posting is not implemented"},
	}
	workflow := WorkflowDescriptor{
		ID: "measure", Name: "Measure", Summary: "Measure supported inputs.",
		Inputs: []string{"validated inputs"}, Steps: []string{"validate", "measure"},
		Outputs: []string{"measurement"}, RequirementIDs: []string{"REQ-1"},
	}
	module := &dummyModule{
		name: "Profiled Module",
		descriptor: &ModuleDescriptor{
			ID: "profiled", Name: "Profiled Module", Version: "1.0.0",
			Provides: []Capability{CapRevenueRecognition},
			Profile:  &profile, Workflows: []WorkflowDescriptor{workflow},
		},
	}

	registry := NewRegistry()
	if err := registry.Register(module); err != nil {
		t.Fatalf("register profiled module: %v", err)
	}
	descriptor, ok := registry.GetDescriptor("profiled")
	if !ok {
		t.Fatal("registered profile descriptor is not discoverable")
	}
	if descriptor.Profile.Standard != profile.Standard || len(descriptor.Workflows) != 1 {
		t.Fatalf("registered descriptor lost profile/workflows: %+v", descriptor)
	}
	if registry.HasCapability(CapRevenueRecognition) {
		t.Fatal("capability is active before the profile edition and jurisdiction are configured")
	}
	if _, ok := registry.GetDisabledReason("capability:" + string(CapRevenueRecognition)); !ok {
		t.Fatal("unconfigured profile did not explain why its capability is disabled")
	}

	badProfile := profile
	badProfile.EffectiveDate = ""
	badModule := &dummyModule{
		name: "Incomplete Module",
		descriptor: &ModuleDescriptor{
			ID: "incomplete", Name: "Incomplete Module", Version: "1.0.0",
			Profile: &badProfile, Workflows: []WorkflowDescriptor{workflow},
		},
	}
	if err := NewRegistry().Register(badModule); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("register incomplete profile error = %v, want ErrInvalidMetadata", err)
	}
	badUnimplementedProfile := profile
	badUnimplementedProfile.UnimplementedRequirements = []StandardRequirement{{
		ID: "REQ-2", Summary: "A requirement with unsupported evidence",
		Status: RequirementNotImplemented, TestEvidence: "test evidence",
	}}
	badUnimplementedModule := &dummyModule{
		name: "Misstated Module",
		descriptor: &ModuleDescriptor{
			ID: "misstated", Name: "Misstated Module", Version: "1.0.0",
			Profile: &badUnimplementedProfile, Workflows: []WorkflowDescriptor{workflow},
		},
	}
	if err := NewRegistry().Register(badUnimplementedModule); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("register unimplemented requirement with evidence error = %v, want ErrInvalidMetadata", err)
	}

	configuredProfile := profile
	configuredProfile.Edition = "2026"
	configuredProfile.Amendments = []string{"2026-01"}
	configuredProfile.EffectiveDate = "2026-01-01"
	configuredProfile.Jurisdiction = "US"
	configuredModule := &dummyModule{
		name: "Configured Module",
		descriptor: &ModuleDescriptor{
			ID: "configured", Name: "Configured Module", Version: "1.0.0",
			Provides: []Capability{CapRevenueRecognition},
			Profile:  &configuredProfile, Workflows: []WorkflowDescriptor{workflow},
		},
	}
	configuredRegistry := NewRegistry()
	if err := configuredRegistry.Register(configuredModule); err != nil {
		t.Fatalf("register configured module: %v", err)
	}
	if !configuredRegistry.HasCapability(CapRevenueRecognition) {
		t.Fatal("capability is inactive after all profile fields are configured")
	}
}

func TestWorkflowCannotClaimPostingWithoutApplicationMappingContract(t *testing.T) {
	profile := StandardProfile{
		Standard: "Test Standard", Edition: "Host configured edition",
		Amendments:    []string{"Host configured amendments"},
		EffectiveDate: "Host configured effective date",
		Jurisdiction:  "Host configured jurisdiction", Scope: "Limited",
		SupportedRequirements: []StandardRequirement{{
			ID: "REQ-1", Summary: "A measured requirement", Status: RequirementLimited,
			Implementation: "internal/example/measure.go:Measure",
			TestEvidence:   "internal/example/measure_test.go:TestMeasure",
		}},
		Exclusions: []string{"No posting"},
	}
	workflow := WorkflowDescriptor{
		ID: "post", Name: "Post", Summary: "Create a mapped entry.",
		Inputs: []string{"measurement"}, Steps: []string{"resolve mapping", "post"},
		Outputs: []string{"posted entry"}, RequirementIDs: []string{"REQ-1"},
		ProducesJournalEntries: true,
	}
	if err := workflow.Validate(profile); err == nil {
		t.Fatal("posting workflow without application contract was accepted")
	}
	workflow.ApplicationContract = ApplicationLedgerService
	workflow.RequiredCapabilities = []Capability{CapLedgerPosting}
	if err := workflow.Validate(profile); err == nil {
		t.Fatal("posting workflow without a configured journal mapping was accepted")
	}
	workflow.RequiresJournalMapping = true
	if err := workflow.Validate(profile); err != nil {
		t.Fatalf("valid application-backed posting workflow: %v", err)
	}
}
