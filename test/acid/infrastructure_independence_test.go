package acid

import (
	"context"
	"sync"
	"testing"
	"time"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	"accounting_engine/pkg/spi/storage"
)

type memoryStore struct {
	storage.CoreLedgerStore
	mu      sync.Mutex
	entries map[types.JournalEntryID]ledger.JournalEntry
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		entries: make(map[types.JournalEntryID]ledger.JournalEntry),
	}
}

func (m *memoryStore) PostEntry(ctx context.Context, entry ledger.JournalEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[entry.ID] = entry
	return nil
}

func (m *memoryStore) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[entryID]; ok {
		return e, nil
	}
	return ledger.JournalEntry{}, types.ErrAccountNotFound
}

func TestInfrastructureIndependence(t *testing.T) {
	ctx := context.Background()
	memStore := newMemoryStore()

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(memStore),
	).Build(ctx)
	if err != nil {
		t.Fatalf("Failed to build engine with memory store: %v", err)
	}
	defer engine.Close()

	companyID := types.NewCompanyID()
	var wg sync.WaitGroup
	numEntries := 100

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

	memStore.mu.Lock()
	count := len(memStore.entries)
	memStore.mu.Unlock()

	if count != numEntries {
		t.Fatalf("Expected %d entries posted concurrently in memory store, got %d", numEntries, count)
	}
}
