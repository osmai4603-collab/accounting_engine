package ifrs9

import (
	"context"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

type fakeApplication struct{ publicapplication.Service }
type fakeEntryReader struct{ standards.EntryReader }
type fakeMasterDataReader struct{ standards.MasterDataReader }

func validPorts() standards.ModulePorts {
	return standards.NewModulePorts(fakeEntryReader{}, nil, fakeMasterDataReader{}, fakeApplication{})
}

func TestModuleDependencies(t *testing.T) {
	module := &Module{}

	if got := module.Dependencies(); len(got) != 0 {
		t.Fatalf("Dependencies() = %v, want no module dependencies", got)
	}
	if err := module.ValidateDependencies(nil); err == nil {
		t.Fatal("ValidateDependencies(nil) returned nil, want module ports error")
	}
	if err := module.Initialize(context.Background(), nil); err == nil {
		t.Fatal("Initialize(nil) returned nil, want module ports error")
	}
	if err := module.ValidateDependencies(validPorts()); err != nil {
		t.Fatalf("ValidateDependencies with complete module ports: %v", err)
	}
	if err := module.Initialize(context.Background(), validPorts()); err != nil {
		t.Fatalf("Initialize with complete module ports: %v", err)
	}
}

func TestRegisterRules(t *testing.T) {
	module := &Module{}
	if err := module.RegisterRules(nil); err != nil {
		t.Fatalf("RegisterRules(nil): %v", err)
	}

	router := events.NewRouter()
	if err := module.RegisterRules(router); err != nil {
		t.Fatalf("RegisterRules(router): %v", err)
	}
	for _, eventType := range []string{"IFRS9_ECL_PROVISION", "IFRS9_LEASE_PAYMENT"} {
		if got := router.GetRules(eventType); len(got) != 0 {
			t.Errorf("unexpected hard-coded posting rules for %s: %+v", eventType, got)
		}
	}

	descriptor := module.Descriptor()
	if err := descriptor.Profile.Validate(); err != nil {
		t.Fatalf("invalid standard profile: %v", err)
	}
	for _, workflow := range descriptor.Workflows {
		if err := workflow.Validate(*descriptor.Profile); err != nil {
			t.Errorf("invalid workflow %q: %v", workflow.ID, err)
		}
	}
}

func TestVersionedGeneralECLWorkflowIsBoundedAndDoesNotPost(t *testing.T) {
	descriptor := (&Module{}).Descriptor()
	var supported bool
	for _, requirement := range descriptor.Profile.SupportedRequirements {
		if requirement.ID == "IFRS9-ECL-GENERAL-VERSIONED-01" {
			supported = requirement.Status == "limited"
		}
	}
	if !supported {
		t.Fatal("versioned general ECL requirement is not declared as limited support")
	}
	for _, workflow := range descriptor.Workflows {
		if workflow.ID != "versioned-general-ecl-measurement" {
			continue
		}
		if workflow.ProducesJournalEntries || workflow.RequiresJournalMapping ||
			workflow.ApplicationContract != "" || len(workflow.RequiredCapabilities) != 0 {
			t.Fatalf("measurement-only workflow unexpectedly declares posting: %+v", workflow)
		}
		return
	}
	t.Fatal("versioned general ECL workflow not declared")
}
