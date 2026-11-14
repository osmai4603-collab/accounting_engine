package acid

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs16"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
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
	moneyUSD, err := types.NewMoneyFromString("1000.00", "USD")
	if err != nil {
		t.Fatalf("create event amount: %v", err)
	}
	now := time.Now().UTC()

	for _, capability := range []standards.Capability{
		standards.CapRevenueRecognition,
		standards.CapLeaseAccounting,
		standards.CapFinancialInstruments,
	} {
		if engine.Standards().HasCapability(capability) {
			t.Errorf("standard capability %q is active without a configured profile", capability)
		}
	}

	// No standard workflow is activated until the profile and real mapping are configured.
	unregistered := []string{"HARVEST_CROP", "POS_SALE", "SUBSCRIPTION_REVENUE"}
	for _, eventType := range unregistered {
		t.Run("Unregistered/"+eventType, func(t *testing.T) {
			event := events.AccountingEvent{
				EventID:        "evt-" + eventType,
				EventType:      eventType,
				CompanyID:      companyID,
				EffectiveDate:  now,
				Amount:         moneyUSD,
				Dimensions:     map[string]string{"domain": eventType},
				IdempotencyKey: "key-" + eventType,
			}
			if _, err := engine.Events().ProcessEvent(ctx, event); err == nil {
				t.Fatalf("expected rejection for unregistered event type %s", eventType)
			}
		})
	}

	unmappedStandardEvents := []struct {
		name      string
		eventType string
	}{
		{"IFRS 15 Revenue", "IFRS15_REVENUE_RECOGNITION"},
		{"IFRS 16 Lease", "IFRS16_LEASE_PAYMENT"},
		{"IFRS 9 ECL", "IFRS9_ECL_PROVISION"},
	}
	for _, domain := range unmappedStandardEvents {
		t.Run("Unmapped/"+domain.name, func(t *testing.T) {
			event := events.AccountingEvent{
				EventID:        "evt-" + domain.eventType,
				EventType:      domain.eventType,
				CompanyID:      companyID,
				EffectiveDate:  now,
				Amount:         moneyUSD,
				IdempotencyKey: "key-" + domain.eventType,
			}
			if _, err := engine.Events().ProcessEvent(ctx, event); err == nil ||
				!strings.Contains(err.Error(), "no account mapping/rule registered") {
				t.Fatalf("ProcessEvent error = %v, want explicit missing mapping rejection for %s", err, domain.name)
			}
		})
	}

	t.Run("UnsupportedDimensionsAreRejected", func(t *testing.T) {
		event := events.AccountingEvent{
			EventID: "evt-with-dimensions", EventType: "IFRS15_REVENUE_RECOGNITION",
			CompanyID: companyID, EffectiveDate: now, Amount: moneyUSD,
			Dimensions: map[string]string{"domain": "IFRS 15 Revenue"},
		}
		if _, err := engine.Events().ProcessEvent(ctx, event); !errors.Is(err, sdk.ErrEventDimensionsUnsupported) {
			t.Fatalf("ProcessEvent error = %v, want %v", err, sdk.ErrEventDimensionsUnsupported)
		}
	})
}
