package acid

import (
	"context"
	"testing"
	"time"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	"accounting_engine/storage/memory"
	"github.com/shopspring/decimal"
)

func TestExternalConsumption(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
		sdk.WithReporting(store),
		sdk.WithGovernance(store),
	).Build(ctx)
	if err != nil {
		t.Fatalf("External consumption build failed: %v", err)
	}
	defer engine.Close()

	companyID := types.NewCompanyID()
	entryID := types.NewJournalEntryID()

	amt, _ := types.NewMoneyFromString("100.00", "USD")
	zeroAmt, _ := types.NewMoneyFromString("0.00", "USD")

	entry := ledger.JournalEntry{
		ID:            entryID,
		CompanyID:     companyID,
		Status:        ledger.EntryPosted,
		EffectiveDate: time.Now().UTC(),
		Description:   "External consumption test entry",
		Lines: []ledger.EntryLine{
			{
				ID:                  types.NewAccountID(),
				AccountID:           types.NewAccountID(),
				TransactionAmount:   amt,
				TransactionCurrency: "USD",
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     amt,
				FunctionalCredit:    zeroAmt,
			},
			{
				ID:                  types.NewAccountID(),
				AccountID:           types.NewAccountID(),
				TransactionAmount:   amt,
				TransactionCurrency: "USD",
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     zeroAmt,
				FunctionalCredit:    amt,
			},
		},
	}

	posted, err := engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		t.Fatalf("Failed to post entry externally: %v", err)
	}

	fetched, err := engine.Ledger().GetEntry(ctx, companyID, posted.ID)
	if err != nil {
		t.Fatalf("Failed to retrieve posted entry externally: %v", err)
	}

	if fetched.ID != entryID {
		t.Fatalf("Expected retrieved entry ID to match")
	}

	reversal, err := engine.Ledger().ReverseEntry(ctx, companyID, posted.ID, "Testing reversal")
	if err != nil {
		t.Fatalf("Failed to reverse entry: %v", err)
	}
	if reversal.ReversalOfID == nil || *reversal.ReversalOfID != entryID {
		t.Fatalf("Expected reversal reference to match original entry ID")
	}

	tb, err := engine.Reporting().GetTrialBalance(ctx, companyID, time.Now().UTC(), "USD")
	if err != nil {
		t.Fatalf("Failed to generate trial balance: %v", err)
	}
	if tb.CompanyID != companyID {
		t.Fatalf("Trial balance company ID mismatch")
	}
}
