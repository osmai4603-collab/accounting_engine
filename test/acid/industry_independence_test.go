package acid

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs16"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
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

	companyUUID := uuid.New()
	companyID := types.CompanyID(companyUUID)
	moneyUSD, _ := types.NewMoneyFromString("1000.00", "USD")
	now := time.Now().UTC()

	// P0-1/P0-3: real posting and event mapping require company, open period, and real accounts.
	if err := store.CreateCompany(ctx, masterdata.Company{
		ID: companyUUID, Code: "CMP", LegalName: "Test Co",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("setup company: %v", err)
	}
	year := masterdata.GenerateFiscalYear(time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyUUID
	for i := range year.Periods {
		year.Periods[i].CompanyID = companyUUID
	}
	if err := store.CreateFiscalYear(ctx, year); err != nil {
		t.Fatalf("setup fiscal year: %v", err)
	}
	for _, acc := range []masterdata.Account{
		{ID: uuid.New(), CompanyID: companyUUID, Code: "1250", Name: "Contract Asset", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, CurrencyID: "USD", IsActive: true},
		{ID: uuid.New(), CompanyID: companyUUID, Code: "4200", Name: "Revenue", AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, CurrencyID: "USD", IsActive: true},
		{ID: uuid.New(), CompanyID: companyUUID, Code: "2300", Name: "Lease Liability", AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit, CurrencyID: "USD", IsActive: true},
		{ID: uuid.New(), CompanyID: companyUUID, Code: "1010", Name: "Cash", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, CurrencyID: "USD", IsActive: true},
		{ID: uuid.New(), CompanyID: companyUUID, Code: "5300", Name: "ECL Expense", AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit, CurrencyID: "USD", IsActive: true},
		{ID: uuid.New(), CompanyID: companyUUID, Code: "1290", Name: "Loss Allowance", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceCredit, CurrencyID: "USD", IsActive: true},
	} {
		if err := store.CreateAccount(ctx, acc); err != nil {
			t.Fatalf("setup account %s: %v", acc.Code, err)
		}
	}

	// P0-3/P0-6: unregistered event types must be rejected (no default guessing),
	// while module-registered standards rules must be accepted and posted.
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

	registered := []struct {
		name      string
		eventType string
	}{
		{"IFRS 15 Revenue", "IFRS15_REVENUE_RECOGNITION"},
		{"IFRS 16 Lease", "IFRS16_LEASE_PAYMENT"},
		{"IFRS 9 ECL", "IFRS9_ECL_PROVISION"},
	}
	for _, domain := range registered {
		t.Run(domain.name, func(t *testing.T) {
			event := events.AccountingEvent{
				EventID:        "evt-" + domain.eventType,
				EventType:      domain.eventType,
				CompanyID:      companyID,
				EffectiveDate:  now,
				Amount:         moneyUSD,
				IdempotencyKey: "key-" + domain.eventType,
			}
			entry, err := engine.Events().ProcessEvent(ctx, event)
			if err != nil {
				t.Fatalf("Failed to process registered event for %s: %v", domain.name, err)
			}
			if entry.CompanyID != companyID {
				t.Fatalf("CompanyID mismatch")
			}
			if len(entry.Lines) != 2 {
				t.Fatalf("expected 2 lines, got %d", len(entry.Lines))
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
