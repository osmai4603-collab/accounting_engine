package templates_test

import (
	"context"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk/templates"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestSectorAdaptersUseSuppliedRulesAndMasterData(t *testing.T) {
	ctx := context.Background()
	companyID := types.CompanyID(uuid.New())
	amount, err := types.NewMoneyFromString("31.25", "USD")
	if err != nil {
		t.Fatal(err)
	}
	effectiveDate := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	retail, err := templates.NewRetailAdapter(map[templates.RetailEffect]events.PostingRule{
		templates.RetailSale: {
			RuleID: "consumer-retail-sale", EventType: "retail.sale",
			JournalCode:      "RETAIL",
			DebitAccountCode: "R-CASH", CreditAccountCode: "R-REVENUE",
			DescriptionTemplate: "Retail sale {{.EventID}}",
		},
		templates.RetailInventoryReceipt: {
			RuleID: "consumer-retail-inventory", EventType: "retail.inventory_receipt",
			JournalCode:      "RETAIL",
			DebitAccountCode: "R-NOT-IN-MASTER-DATA", CreditAccountCode: "R-REVENUE",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	agriculture, err := templates.NewAgricultureAdapter(map[templates.AgricultureEffect]events.PostingRule{
		templates.AgricultureHarvest: {
			RuleID: "consumer-ag-harvest", EventType: "agriculture.harvest",
			JournalCode:      "AGRICULTURE",
			DebitAccountCode: "A-ASSET", CreditAccountCode: "A-INCOME",
			DescriptionTemplate: "Harvest {{.EventID}}",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	saas, err := templates.NewSaaSAdapter(map[templates.SaaSEffect]events.PostingRule{
		templates.SaaSSubscriptionCharge: {
			RuleID: "consumer-saas-charge", EventType: "saas.subscription_charge",
			JournalCode:      "SAAS",
			DebitAccountCode: "S-RECEIVABLE", CreditAccountCode: "S-DEFERRED",
			DescriptionTemplate: "Subscription charge {{.EventID}}",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	driver := memory.New()
	masterDataStore := driver.MasterDataStore()
	if err := masterDataStore.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "ADAPTER-TEST", LegalName: "Adapter Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	journalIDs := map[string]types.JournalID{}
	for _, code := range []string{"RETAIL", "AGRICULTURE", "SAAS"} {
		journalID := masterdata.JournalID(uuid.New())
		journalIDs[code] = types.JournalID(journalID)
		if err := masterDataStore.CreateJournal(ctx, masterdata.Journal{
			ID: journalID, CompanyID: companyID, Code: code, Name: code,
		}); err != nil {
			t.Fatal(err)
		}
	}

	accountIDs := map[string]types.AccountID{}
	for _, code := range []string{"R-CASH", "A-ASSET", "S-RECEIVABLE"} {
		id := types.AccountID(uuid.New())
		accountIDs[code] = id
		if err := masterDataStore.CreateAccount(ctx, masterdata.Account{
			ID: id, CompanyID: companyID, Code: code, Name: code,
			Type: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{"R-REVENUE", "A-INCOME", "S-DEFERRED"} {
		id := types.AccountID(uuid.New())
		accountIDs[code] = id
		if err := masterDataStore.CreateAccount(ctx, masterdata.Account{
			ID: id, CompanyID: companyID, Code: code, Name: code,
			Type: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	yearStart := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	yearID := types.NewFiscalYearID()
	if err := masterDataStore.CreateFiscalYear(ctx, masterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: yearStart, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: masterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := masterDataStore.CreateFiscalPeriod(ctx, masterdata.FiscalPeriod{
		ID: types.NewFiscalPeriodID(), CompanyID: companyID,
		FiscalYearID: yearID, Number: 1, Name: "January 2026",
		StartDate: yearStart, EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State: masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(driver),
		sdk.WithStandard(retail),
		sdk.WithStandard(agriculture),
		sdk.WithStandard(saas),
	).Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close SDK: %v", err)
		}
	}()

	tests := []struct {
		name        string
		eventType   string
		journalCode string
		debitCode   string
		creditCode  string
		buildEvent  func() (events.AccountingEvent, error)
	}{
		{
			name: "retail sale", eventType: "retail.sale", journalCode: "RETAIL", debitCode: "R-CASH", creditCode: "R-REVENUE",
			buildEvent: func() (events.AccountingEvent, error) {
				return retail.Event(templates.RetailSale, templates.EventInput{
					EventID: "retail-1", CompanyID: companyID, EffectiveDate: effectiveDate,
					Amount: amount, IdempotencyKey: "retail-key",
				})
			},
		},
		{
			name: "agriculture harvest", eventType: "agriculture.harvest", journalCode: "AGRICULTURE", debitCode: "A-ASSET", creditCode: "A-INCOME",
			buildEvent: func() (events.AccountingEvent, error) {
				return agriculture.Event(templates.AgricultureHarvest, templates.EventInput{
					EventID: "agriculture-1", CompanyID: companyID, EffectiveDate: effectiveDate,
					Amount: amount, IdempotencyKey: "agriculture-key",
				})
			},
		},
		{
			name: "SaaS subscription", eventType: "saas.subscription_charge", journalCode: "SAAS", debitCode: "S-RECEIVABLE", creditCode: "S-DEFERRED",
			buildEvent: func() (events.AccountingEvent, error) {
				return saas.Event(templates.SaaSSubscriptionCharge, templates.EventInput{
					EventID: "saas-1", CompanyID: companyID, EffectiveDate: effectiveDate,
					Amount: amount, IdempotencyKey: "saas-key",
				})
			},
		},
	}
	seenEventTypes := make(map[string]bool)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			event, err := tc.buildEvent()
			if err != nil {
				t.Fatal(err)
			}
			if event.EventType != tc.eventType || event.Amount.Currency() != "USD" || !event.Amount.Equal(amount) {
				t.Fatalf("adapter event = %+v", event)
			}
			if seenEventTypes[event.EventType] {
				t.Fatalf("sector effects reused event type %q", event.EventType)
			}
			seenEventTypes[event.EventType] = true

			entry, err := engine.Events().ProcessEvent(ctx, event)
			if err != nil {
				t.Fatalf("process event: %v", err)
			}
			if len(entry.Lines) != 2 {
				t.Fatalf("entry has %d lines, want 2", len(entry.Lines))
			}
			if entry.JournalID != journalIDs[tc.journalCode] {
				t.Fatalf("entry journal = %s, want configured %s journal %s",
					entry.JournalID, tc.journalCode, journalIDs[tc.journalCode])
			}
			if entry.Lines[0].AccountID != accountIDs[tc.debitCode] || entry.Lines[1].AccountID != accountIDs[tc.creditCode] {
				t.Fatalf("mapped accounts = (%v, %v), want (%v, %v)",
					entry.Lines[0].AccountID, entry.Lines[1].AccountID, tc.debitCode, tc.creditCode)
			}
			debit, credit := entry.Lines[0], entry.Lines[1]
			if !debit.TransactionAmount.Equal(amount) || !credit.TransactionAmount.Equal(amount) ||
				debit.TransactionCurrency != "USD" || credit.TransactionCurrency != "USD" {
				t.Fatalf("transaction amounts/currency were not preserved: %+v", entry.Lines)
			}
			if !debit.FunctionalDebit.Equal(amount) || !debit.FunctionalCredit.IsZero() ||
				!credit.FunctionalCredit.Equal(amount) || !credit.FunctionalDebit.IsZero() {
				t.Fatalf("entry is not balanced as configured: %+v", entry.Lines)
			}
			debits := debit.FunctionalDebit.Amount().Add(credit.FunctionalDebit.Amount())
			credits := debit.FunctionalCredit.Amount().Add(credit.FunctionalCredit.Amount())
			if !debits.Equal(credits) || !debits.Equal(decimal.RequireFromString("31.25")) {
				t.Fatalf("debits %s do not equal credits %s", debits, credits)
			}
		})
	}

	unmappedAccountEvent, err := retail.Event(templates.RetailInventoryReceipt, templates.EventInput{
		EventID: "retail-inventory-1", CompanyID: companyID,
		EffectiveDate: effectiveDate, Amount: amount,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Events().ProcessEvent(ctx, unmappedAccountEvent); err == nil {
		t.Fatal("event with an account code absent from company master data was accepted")
	}
}

func TestAdaptersRequireExplicitRulesAndCodes(t *testing.T) {
	if _, err := templates.NewRetailAdapter(nil); err == nil {
		t.Fatal("expected empty retail rules to be rejected")
	}
	if _, err := templates.NewAgricultureAdapter(map[templates.AgricultureEffect]events.PostingRule{
		templates.AgricultureHarvest: {EventType: "harvest", DebitAccountCode: "1200"},
	}); err == nil {
		t.Fatal("expected a missing credit account code to be rejected")
	}
}
