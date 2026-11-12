package acid

import (
	"context"
	"testing"
	"time"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	"accounting_engine/pkg/spi/storage"
)

type industryStore struct {
	storage.CoreLedgerStore
	posted []ledger.JournalEntry
}

func (s *industryStore) PostEntry(ctx context.Context, entry ledger.JournalEntry) error {
	s.posted = append(s.posted, entry)
	return nil
}

func TestIndustryIndependence(t *testing.T) {
	ctx := context.Background()
	store := &industryStore{}

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
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

	if len(store.posted) != 3 {
		t.Fatalf("Expected 3 posted entries across industries, got %d", len(store.posted))
	}
}
