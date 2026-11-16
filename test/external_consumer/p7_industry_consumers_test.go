package externalconsumer_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	application "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
	"github.com/shopspring/decimal"
)

type hostAuthorizer struct {
	mu       sync.Mutex
	requests []application.AuthorizationRequest
}

func (a *hostAuthorizer) Authorize(_ context.Context, request application.AuthorizationRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, request)
	return nil
}

func (a *hostAuthorizer) observedActor(actorID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, request := range a.requests {
		if request.Principal.ActorID == actorID &&
			len(request.Principal.CompanyIDs) == 1 {
			return true
		}
	}
	return false
}

type industryDraft struct {
	description string
	source      string
	reference   string
	amount      string
	debit       types.AccountID
	credit      types.AccountID
}

type pointOfSaleSale struct {
	receipt string
	total   string
}

type harvestReceipt struct {
	lot      string
	proceeds string
}

type manufacturingShipment struct {
	order string
	value string
}

type consumerAccounts struct {
	cash, receivable, revenue types.AccountID
}

func TestP7_IndustryConsumersUseCanonicalAccounting(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	store := driver.MasterDataStore()
	companyID := types.NewCompanyID()
	journalID := masterdata.NewJournalID()
	cashID, receivableID, revenueID := types.NewAccountID(), types.NewAccountID(), types.NewAccountID()
	yearID, periodID := types.NewFiscalYearID(), types.NewFiscalPeriodID()
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)
	effectiveDate := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	if err := store.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: "P7-INDUSTRY", LegalName: "Industry Consumer Acceptance",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateJournal(ctx, masterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GEN", Name: "General Journal",
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []masterdata.Account{
		{ID: cashID, CompanyID: companyID, Code: "1000", Name: "Cash", Type: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
		{ID: receivableID, CompanyID: companyID, Code: "1200", Name: "Receivables", Type: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
		{ID: revenueID, CompanyID: companyID, Code: "4000", Name: "Revenue", Type: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, Currency: "USD", IsActive: true},
	} {
		if err := store.CreateAccount(ctx, account); err != nil {
			t.Fatalf("create account %s: %v", account.Code, err)
		}
	}
	if err := store.CreateFiscalYear(ctx, masterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026", StartDate: start, EndDate: end, State: masterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateFiscalPeriod(ctx, masterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID, Number: 1, Name: "January 2026",
		StartDate: start, EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State: masterdata.PeriodOpen, Type: masterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatal(err)
	}

	hostAuth := &hostAuthorizer{}
	engine, err := sdk.NewBuilder(
		sdk.WithStoragePorts(driver.Ports()),
		sdk.WithReporting(driver.Reporting()),
		sdk.WithAuthorizer(hostAuth),
	).Build(ctx)
	if err != nil {
		t.Fatalf("build public SDK engine: %v", err)
	}
	defer engine.Close()
	app, err := sdk.NewApplicationServiceWithAuthorizer(driver.Ports(), hostAuth)
	if err != nil {
		t.Fatalf("build public application service: %v", err)
	}

	accounts := consumerAccounts{cash: cashID, receivable: receivableID, revenue: revenueID}
	consumers := []struct {
		name  string
		actor string
		draft industryDraft
	}{
		{
			name:  "commercial",
			actor: "host:commerce",
			draft: commercialSaleToDraft(pointOfSaleSale{receipt: "POS-41", total: "125.00"}, accounts),
		},
		{
			name:  "agricultural",
			actor: "host:agriculture",
			draft: harvestReceiptToDraft(harvestReceipt{lot: "HARVEST-08", proceeds: "225.00"}, accounts),
		},
		{
			name:  "industrial",
			actor: "host:manufacturing",
			draft: manufacturingShipmentToDraft(manufacturingShipment{order: "ORDER-19", value: "325.00"}, accounts),
		},
	}

	for _, consumer := range consumers {
		t.Run(consumer.name, func(t *testing.T) {
			commandContext := application.Context{
				Principal: application.Principal{
					ActorID: consumer.actor, TenantID: "host-tenant",
					CompanyIDs: []types.CompanyID{companyID}, Permissions: []string{"ledger.create", "ledger.post", "ledger.reverse", "ledger.read"},
				},
				CorrelationID: "p7-" + consumer.name,
				SourceSystem:  consumer.name + "-backend",
			}
			amount, err := types.NewMoneyFromString(consumer.draft.amount, "USD")
			if err != nil {
				t.Fatal(err)
			}
			zero, _ := types.Zero("USD")
			debit, credit := amount, zero
			if consumer.draft.debit == revenueID {
				debit, credit = zero, amount
			}
			cmd := application.CreateDraftCommand{
				Context: commandContext, CompanyID: companyID, JournalID: types.JournalID(journalID),
				FiscalPeriodID: periodID, EffectiveDate: effectiveDate, PostingDate: effectiveDate,
				Description: consumer.draft.description, SourceModule: consumer.draft.source,
				ReferenceID: consumer.draft.reference, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1).String(), Classification: ledger.EntryClassRegular,
				Lines: []ledger.EntryLine{
					{ID: types.NewJournalEntryLineID(), AccountID: consumer.draft.debit, TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: debit, FunctionalCredit: credit},
					{ID: types.NewJournalEntryLineID(), AccountID: consumer.draft.credit, TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: credit, FunctionalCredit: debit},
				},
			}
			draft, err := app.CreateDraft(ctx, cmd)
			if err != nil {
				t.Fatalf("create canonical draft: %v", err)
			}
			if draft.Entry.Status != ledger.EntryDraft {
				t.Fatalf("draft status = %q, want draft", draft.Entry.Status)
			}
			if err := app.Validate(ctx, application.ValidateEntryCommand{
				Context: commandContext, CompanyID: companyID, EntryID: draft.Entry.ID,
			}); err != nil {
				t.Fatalf("validate canonical draft: %v", err)
			}
			posted, err := app.Post(ctx, application.PostEntryCommand{
				Context: commandContext, CompanyID: companyID, EntryID: draft.Entry.ID,
			})
			if err != nil {
				t.Fatalf("post canonical entry: %v", err)
			}
			readBack, err := app.GetEntry(ctx, application.GetEntryQuery{
				Context: commandContext, CompanyID: companyID, EntryID: posted.Entry.ID,
			})
			if err != nil {
				t.Fatalf("read canonical entry: %v", err)
			}
			if readBack.Status != ledger.EntryPosted || len(readBack.Lines) != 2 {
				t.Fatalf("read-back status=%q lines=%d", readBack.Status, len(readBack.Lines))
			}
			reversed, err := app.Reverse(ctx, application.ReverseEntryCommand{
				Context: commandContext, CompanyID: companyID, EntryID: posted.Entry.ID,
				Reason: "P7 industry consumer acceptance", EffectiveDate: effectiveDate,
			})
			if err != nil {
				t.Fatalf("reverse canonical entry: %v", err)
			}
			original, err := app.GetEntry(ctx, application.GetEntryQuery{
				Context: commandContext, CompanyID: companyID, EntryID: posted.Entry.ID,
			})
			if err != nil {
				t.Fatalf("read reversed source entry: %v", err)
			}
			if original.Status != ledger.EntryReversed ||
				reversed.Entry.Status != ledger.EntryPosted ||
				reversed.Entry.ReversalOfID == nil || *reversed.Entry.ReversalOfID != posted.Entry.ID {
				t.Fatalf("invalid reversal state: original=%q reversal=%q", original.Status, reversed.Entry.Status)
			}
		})
	}

	balance, err := engine.Reporting().GetTrialBalance(ctx, companyID, effectiveDate, "USD")
	if err != nil {
		t.Fatalf("report after industry reversals: %v", err)
	}
	if !balance.IsBalanced || !balance.TotalDebit.Amount().IsZero() || !balance.TotalCredit.Amount().IsZero() {
		t.Fatalf("industry consumers did not reverse to zero: debit=%s credit=%s balanced=%t",
			balance.TotalDebit, balance.TotalCredit, balance.IsBalanced)
	}
	for _, consumer := range consumers {
		if !hostAuth.observedActor(consumer.actor) {
			t.Errorf("host authorization boundary did not receive execution context for %s", consumer.actor)
		}
	}
}

func commercialSaleToDraft(event pointOfSaleSale, accounts consumerAccounts) industryDraft {
	return industryDraft{
		description: "Point-of-sale receipt " + event.receipt,
		source:      "commercial-backend",
		reference:   event.receipt,
		amount:      event.total,
		debit:       accounts.cash,
		credit:      accounts.revenue,
	}
}

func harvestReceiptToDraft(event harvestReceipt, accounts consumerAccounts) industryDraft {
	return industryDraft{
		description: "Harvest proceeds for lot " + event.lot,
		source:      "agricultural-backend",
		reference:   event.lot,
		amount:      event.proceeds,
		debit:       accounts.receivable,
		credit:      accounts.revenue,
	}
}

func manufacturingShipmentToDraft(event manufacturingShipment, accounts consumerAccounts) industryDraft {
	return industryDraft{
		description: fmt.Sprintf("Manufacturing shipment for order %s", event.order),
		source:      "manufacturing-backend",
		reference:   event.order,
		amount:      event.value,
		debit:       accounts.receivable,
		credit:      accounts.revenue,
	}
}
