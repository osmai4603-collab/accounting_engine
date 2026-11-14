package adapter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/osmai4603-collab/accounting_engine/test/external_adapter_consumer/adapter"
	"github.com/shopspring/decimal"
)

func TestStoreClonesEntriesAndScopesTenant(t *testing.T) {
	ctx := context.Background()
	fixture := adapter.DefaultFixture()
	store, err := adapter.New(fixture)
	if err != nil {
		t.Fatalf("provision adapter: %v", err)
	}
	if err := store.Ports().Validate(); err != nil {
		t.Fatalf("ports.Validate: %v", err)
	}

	cash, err := fixture.Account("1000")
	if err != nil {
		t.Fatal(err)
	}
	revenue, err := fixture.Account("4000")
	if err != nil {
		t.Fatal(err)
	}
	amount, err := types.NewMoneyFromString("10.00", fixture.FunctionalCurrency)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := types.Zero(fixture.FunctionalCurrency)
	if err != nil {
		t.Fatal(err)
	}

	entry := ledger.JournalEntry{
		ID:                  types.NewJournalEntryID(),
		CompanyID:           fixture.Company.ID,
		JournalID:           types.JournalID(fixture.Journal.ID),
		FiscalPeriodID:      fixture.FiscalPeriod.ID,
		Status:              ledger.EntryDraft,
		Classification:      ledger.EntryClassRegular,
		EffectiveDate:       time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC),
		PostingDate:         time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC),
		Description:         "clone check",
		TransactionCurrency: fixture.FunctionalCurrency,
		ExchangeRate:        decimal.NewFromInt(1),
		Lines: []ledger.EntryLine{
			{
				ID: types.NewJournalEntryLineID(), AccountID: cash.ID,
				TransactionAmount: amount, TransactionCurrency: fixture.FunctionalCurrency,
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.NewJournalEntryLineID(), AccountID: revenue.ID,
				TransactionAmount: amount, TransactionCurrency: fixture.FunctionalCurrency,
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}
	if err := store.SaveDraft(ctx, entry); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	read, err := store.GetEntry(ctx, fixture.Company.ID, entry.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	read.Description = "mutated copy"
	read.Lines[0].AccountID = revenue.ID
	read.Lines[0].FunctionalDebit = zero

	again, err := store.GetEntry(ctx, fixture.Company.ID, entry.ID)
	if err != nil {
		t.Fatalf("get entry again: %v", err)
	}
	if again.Description != entry.Description {
		t.Fatalf("stored description = %q, want %q (alias leaked)", again.Description, entry.Description)
	}
	if again.Lines[0].AccountID != cash.ID || !again.Lines[0].FunctionalDebit.Equal(amount) {
		t.Fatal("stored line was mutated through a read result (slice alias leaked)")
	}

	otherCompany := types.NewCompanyID()
	if _, err := store.GetEntry(ctx, otherCompany, entry.ID); !errors.Is(err, adapter.ErrTenantMismatch) {
		t.Fatalf("cross-tenant get error = %v, want ErrTenantMismatch", err)
	}
	if err := store.SaveDraft(ctx, ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: otherCompany,
		JournalID: types.JournalID(fixture.Journal.ID), FiscalPeriodID: fixture.FiscalPeriod.ID,
	}); !errors.Is(err, adapter.ErrTenantMismatch) {
		t.Fatalf("cross-tenant draft error = %v, want ErrTenantMismatch", err)
	}
}

func TestStoreReportsTypedCapabilityError(t *testing.T) {
	fixture := adapter.DefaultFixture()
	store, err := adapter.New(fixture)
	if err != nil {
		t.Fatalf("provision adapter: %v", err)
	}
	if _, err := store.GetBalanceSheet(context.Background(), fixture.Company.ID, time.Now(), "USD"); !errors.Is(err, adapter.ErrReportNotSupported) {
		t.Fatalf("balance sheet error = %v, want ErrReportNotSupported", err)
	}
	if _, err := store.GetIncomeStatement(context.Background(), fixture.Company.ID, time.Now(), time.Now(), "USD"); !errors.Is(err, adapter.ErrReportNotSupported) {
		t.Fatalf("income statement error = %v, want ErrReportNotSupported", err)
	}
	if _, err := store.Ports().Posting.(storage.PostingWriter).CommitPosting(context.Background(), storage.PostingCommit{}); err == nil {
		t.Fatal("empty posting commit succeeded, want an error")
	}
}
