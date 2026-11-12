package acid

import (
	"context"
	"testing"
	"time"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	"accounting_engine/pkg/spi/storage"
)

type acidCoreStore struct {
	storage.CoreLedgerStore
	entries map[types.JournalEntryID]ledger.JournalEntry
}

func (s *acidCoreStore) PostEntry(ctx context.Context, entry ledger.JournalEntry) error {
	if s.entries == nil {
		s.entries = make(map[types.JournalEntryID]ledger.JournalEntry)
	}
	s.entries[entry.ID] = entry
	return nil
}

func (s *acidCoreStore) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	if entry, ok := s.entries[entryID]; ok {
		return entry, nil
	}
	return ledger.JournalEntry{}, types.ErrAccountNotFound
}

func TestExternalConsumption(t *testing.T) {
	ctx := context.Background()
	store := &acidCoreStore{}

	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(store),
	).Build(ctx)
	if err != nil {
		t.Fatalf("External consumption build failed: %v", err)
	}
	defer engine.Close()

	companyID := types.NewCompanyID()
	entryID := types.NewJournalEntryID()

	amt, _ := types.NewMoneyFromString("100.00", "USD")
	entry := ledger.JournalEntry{
		ID:            entryID,
		CompanyID:     companyID,
		Status:        ledger.EntryPosted,
		EffectiveDate: time.Now().UTC(),
		Description:   "External consumption test entry",
		Lines: []ledger.EntryLine{
			{ID: types.NewAccountID(), AccountID: types.NewAccountID(), FunctionalDebit: amt, TransactionAmount: amt, TransactionCurrency: "USD"},
			{ID: types.NewAccountID(), AccountID: types.NewAccountID(), FunctionalCredit: amt, TransactionAmount: amt, TransactionCurrency: "USD"},
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
}
