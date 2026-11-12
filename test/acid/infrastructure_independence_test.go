package acid

import (
	"context"
	"sync"
	"testing"
	"time"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	"accounting_engine/storage/memory"
)

func TestInfrastructureIndependence(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
		sdk.WithReporting(store),
		sdk.WithGovernance(store),
	).Build(ctx)
	if err != nil {
		t.Fatalf("Failed to build engine with memory store: %v", err)
	}
	defer engine.Close()

	companyID := types.NewCompanyID()
	var wg sync.WaitGroup
	numEntries := 1000

	for i := 0; i < numEntries; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			entryID := types.NewJournalEntryID()
			amt, _ := types.NewMoneyFromString("50.00", "USD")
			entry := ledger.JournalEntry{
				ID:            entryID,
				CompanyID:     companyID,
				Status:        ledger.EntryPosted,
				EffectiveDate: time.Now().UTC(),
				Description:   "Concurrent memory store test",
				Lines: []ledger.EntryLine{
					{ID: types.NewAccountID(), AccountID: types.NewAccountID(), FunctionalDebit: amt, TransactionAmount: amt, TransactionCurrency: "USD"},
					{ID: types.NewAccountID(), AccountID: types.NewAccountID(), FunctionalCredit: amt, TransactionAmount: amt, TransactionCurrency: "USD"},
				},
			}
			_, _ = engine.Ledger().PostEntry(ctx, entry)
		}(i)
	}
	wg.Wait()

	tb, err := engine.Reporting().GetTrialBalance(ctx, companyID, time.Now().UTC(), "USD")
	if err != nil {
		t.Fatalf("Failed to retrieve trial balance after concurrent load: %v", err)
	}
	if tb.CompanyID != companyID {
		t.Fatalf("Trial balance company ID mismatch")
	}
}
