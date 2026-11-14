package ifrs15

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
)

func TestModuleMetadataAndDependencyErrors(t *testing.T) {
	module := &Module{}

	if err := module.ValidateDependencies(nil); err == nil {
		t.Fatal("ValidateDependencies(nil) returned nil, want dependency error")
	}
	if err := module.Initialize(context.Background(), nil); err == nil {
		t.Fatal("Initialize with nil core store returned nil, want dependency error")
	}
	if err := module.RegisterRules(nil); err != nil {
		t.Fatalf("RegisterRules(nil): %v", err)
	}

	router := events.NewRouter()
	if err := module.RegisterRules(router); err != nil {
		t.Fatalf("RegisterRules(router): %v", err)
	}
	if got := router.GetRules("IFRS15_REVENUE_RECOGNITION"); len(got) != 0 {
		t.Fatalf("registered hard-coded posting rules: %+v", got)
	}

	descriptor := module.Descriptor()
	if err := descriptor.Profile.Validate(); err != nil {
		t.Fatalf("invalid standard profile: %v", err)
	}
	if len(descriptor.Workflows) == 0 {
		t.Fatal("standard module has no declared workflows")
	}
	for _, workflow := range descriptor.Workflows {
		if err := workflow.Validate(*descriptor.Profile); err != nil {
			t.Errorf("invalid workflow %q: %v", workflow.ID, err)
		}
	}
}

func TestModuleDoesNotPostWithoutConfiguredJournalMapping(t *testing.T) {
	ctx := context.Background()
	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(memory.New()),
		sdk.WithStandard(&Module{}),
	).Build(ctx)
	if err != nil {
		t.Fatalf("build engine with IFRS 15 module: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	}()

	amount, err := types.NewMoneyFromString("250.75", "USD")
	if err != nil {
		t.Fatalf("create event amount: %v", err)
	}
	_, err = engine.Events().ProcessEvent(ctx, events.AccountingEvent{
		EventID: "revenue-001", EventType: "IFRS15_REVENUE_RECOGNITION",
		CompanyID: types.NewCompanyID(), EffectiveDate: time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		Amount: amount,
	})
	if err == nil || !strings.Contains(err.Error(), "no account mapping/rule registered") {
		t.Fatalf("ProcessEvent error = %v, want explicit missing mapping error", err)
	}
}
