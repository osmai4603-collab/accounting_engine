package consumer_test

import (
	"context"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/test/external_adapter_consumer/adapter"
	"github.com/shopspring/decimal"
)

// TestEngineReverseEntryUsesPublicPostingWriter exercises the SDK Engine
// convenience surface (PostEntry/ReverseEntry) against the same public ports.
// The reversal has no idempotency key, so it must flow through the public
// PostingWriter.CommitPosting fallback with OperationReverse.
func TestEngineReverseEntryUsesPublicPostingWriter(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	fixture := adapter.DefaultFixture()
	fixture.FiscalYear.StartDate = now.AddDate(-1, 0, 0)
	fixture.FiscalYear.EndDate = now.AddDate(1, 0, 0)
	fixture.FiscalPeriod.StartDate = now.AddDate(0, 0, -1)
	fixture.FiscalPeriod.EndDate = now.AddDate(0, 0, 1)

	store, err := adapter.New(fixture)
	if err != nil {
		t.Fatalf("provision adapter: %v", err)
	}
	engine, err := sdk.NewBuilder(sdk.WithStoragePorts(store.Ports())).Build(ctx)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	}()

	cash, err := fixture.Account("1000")
	if err != nil {
		t.Fatal(err)
	}
	revenue, err := fixture.Account("4000")
	if err != nil {
		t.Fatal(err)
	}
	amount, err := types.NewMoneyFromString("99.50", fixture.FunctionalCurrency)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := types.Zero(fixture.FunctionalCurrency)
	if err != nil {
		t.Fatal(err)
	}

	posted, err := engine.Ledger().PostEntry(ctx, ledger.JournalEntry{
		ID:                  types.NewJournalEntryID(),
		CompanyID:           fixture.Company.ID,
		JournalID:           types.JournalID(fixture.Journal.ID),
		FiscalPeriodID:      fixture.FiscalPeriod.ID,
		Status:              ledger.EntryDraft,
		Classification:      ledger.EntryClassRegular,
		EffectiveDate:       now,
		PostingDate:         now,
		Description:         "Engine convenience posting",
		SourceModule:        "external-adapter-consumer",
		TransactionCurrency: fixture.FunctionalCurrency,
		ExchangeRate:        fixture.ExchangeRate,
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
	})
	if err != nil {
		t.Fatalf("post through engine: %v", err)
	}
	if posted.Status != ledger.EntryPosted || posted.SequenceNumber != 1 {
		t.Fatalf("posted = status %q sequence %d", posted.Status, posted.SequenceNumber)
	}

	reversal, err := engine.Ledger().ReverseEntry(ctx, fixture.Company.ID, posted.ID, "engine path correction")
	if err != nil {
		t.Fatalf("reverse through engine: %v", err)
	}
	if reversal.ReversalOfID == nil || *reversal.ReversalOfID != posted.ID {
		t.Fatalf("reversal original = %v, want %s", reversal.ReversalOfID, posted.ID)
	}

	original, err := engine.Ledger().GetEntry(ctx, fixture.Company.ID, posted.ID)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if original.Status != ledger.EntryReversed || original.ReversedByID == nil || *original.ReversedByID != reversal.ID {
		t.Fatalf("original = status %q reversed-by %v, want reversed/%s", original.Status, original.ReversedByID, reversal.ID)
	}

	if _, err := engine.Ledger().ReverseEntry(ctx, fixture.Company.ID, posted.ID, "second engine reversal"); err == nil {
		t.Fatal("second engine reversal succeeded, want rejection")
	}
}
