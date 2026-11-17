package ias12

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	ias12engine "github.com/osmai4603-collab/accounting_engine/internal/ias12"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	apitypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
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
	for _, eventType := range []string{"IAS12_DEFERRED_TAX", "IFRS9_ECL_PROVISION"} {
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
	if _, ok := interface{}(module).(standards.DisclosureProvider); !ok {
		t.Fatal("IAS 12 module does not implement DisclosureProvider")
	}
	for _, requirementID := range []string{
		"IFRIC23-UNCERTAIN-TAX-TREATMENTS-01",
		"IAS12-TAX-RATE-RECONCILIATION-01",
	} {
		found := false
		for _, requirement := range descriptor.Profile.SupportedRequirements {
			if requirement.ID == requirementID && requirement.Status == standards.RequirementLimited {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing limited supported requirement %q", requirementID)
		}
	}
}

func TestModuleProvidesTaxReconciliationDisclosure(t *testing.T) {
	money := func(value string) financialtypes.Money {
		amount, err := financialtypes.NewMoneyFromString(value, "USD")
		if err != nil {
			t.Fatal(err)
		}
		return amount
	}
	periodEnd := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	report, err := ias12engine.Reconcile(ias12engine.TaxReconciliationInput{
		Period:                    ias12engine.MeasurementPeriod{Kind: ias12engine.Annual, AsOf: periodEnd},
		AccountingProfitBeforeTax: money("1000"),
		StatutoryRate: ias12engine.TaxRate{
			Value: decimal.RequireFromString("0.2"), Enacted: true,
			EnactedOn:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EffectiveFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		CurrentTaxExpense: money("215"), DeferredTaxExpense: money("30"),
	})
	if err != nil {
		t.Fatal(err)
	}
	companyID := apitypes.CompanyID(uuid.New())
	input := ias12engine.TaxReconciliationDisclosureInput{
		CompanyID:   companyID,
		PeriodStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   periodEnd,
		Report:      report,
		SourceReferences: []publicreporting.DisclosureSource{
			{Kind: "tax-workpaper", Reference: "reconciliation-2026"},
		},
	}
	provider, err := ias12engine.NewTaxReconciliationDisclosureProvider(input)
	if err != nil {
		t.Fatal(err)
	}
	module := NewModule(provider)
	disclosures, err := module.GetDisclosures(context.Background(), publicreporting.DisclosureQuery{
		CompanyID: companyID,
		Period:    publicreporting.DisclosurePeriod{StartDate: input.PeriodStart, EndDate: input.PeriodEnd},
		Currency:  "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(disclosures) != 1 || disclosures[0].Code != ias12engine.TaxReconciliationDisclosureCode {
		t.Fatalf("IAS 12 module disclosures = %+v, want tax reconciliation", disclosures)
	}
}
