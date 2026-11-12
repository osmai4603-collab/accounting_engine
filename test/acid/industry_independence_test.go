package acid

import (
	"context"
	"testing"
	"time"

	"accounting_engine/modules/ifrs15"
	"accounting_engine/modules/ifrs16"
	"accounting_engine/modules/ifrs9"
	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	"accounting_engine/storage/memory"
)

func TestIndustryIndependence(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
		sdk.WithReporting(store),
		sdk.WithGovernance(store),
		sdk.WithStandard(&ifrs15.Module{}),
		sdk.WithStandard(&ifrs16.Module{}),
		sdk.WithStandard(&ifrs9.Module{}),
	).Build(ctx)
	if err != nil {
		t.Fatalf("Failed to build engine for industry independence: %v", err)
	}

	companyID := types.NewCompanyID()
	moneyUSD, _ := types.NewMoneyFromString("1000.00", "USD")

	domains := []struct {
		name      string
		eventType string
	}{
		{"Agricultural (Harvest / Biological Assets)", "HARVEST_CROP"},
		{"Retail (POS Sale & VAT)", "POS_SALE"},
		{"Service (SaaS Deferred Revenue)", "SUBSCRIPTION_REVENUE"},
		{"IFRS 15 Revenue", "IFRS15_REVENUE_RECOGNITION"},
		{"IFRS 16 Lease", "IFRS16_LEASE_PAYMENT"},
		{"IFRS 9 ECL", "IFRS9_ECL_PROVISION"},
	}

	for _, domain := range domains {
		t.Run(domain.name, func(t *testing.T) {
			event := events.AccountingEvent{
				EventID:        "evt-" + domain.eventType,
				EventType:      domain.eventType,
				CompanyID:      companyID,
				EffectiveDate:  time.Now().UTC(),
				Amount:         moneyUSD,
				Dimensions:     map[string]string{"domain": domain.name},
				IdempotencyKey: "key-" + domain.eventType,
			}

			entry, err := engine.Events().ProcessEvent(ctx, event)
			if err != nil {
				t.Fatalf("Failed to process event for %s: %v", domain.name, err)
			}
			if entry.CompanyID != companyID {
				t.Fatalf("CompanyID mismatch")
			}
		})
	}
}
