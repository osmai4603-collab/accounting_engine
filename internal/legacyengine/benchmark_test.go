package legacyengine_test

import (
	"context"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
)

func BenchmarkSinglePosting(b *testing.B) {
	driver := memory.New()
	ctx := context.Background()
	cash, revenue := uuid.New(), uuid.New()
	_ = driver.CreateAccount(ctx, masterdata.Account{ID: cash, Code: "BEN-CASH", Name: "Cash", AccountType: masterdata.AccountTypeAsset})
	_ = driver.CreateAccount(ctx, masterdata.Account{ID: revenue, Code: "BEN-REV", Name: "Revenue", AccountType: masterdata.AccountTypeIncome})
	journalID := uuid.New()
	amount, _ := financialtypes.NewMoneyFromString("100", "USD")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry, err := posting.NewJournalEntry(journalID, time.Now().UTC(), "benchmark",
			[]posting.JournalEntryLine{
				{ID: uuid.New(), AccountID: cash, Debit: amount},
				{ID: uuid.New(), AccountID: revenue, Credit: amount},
			})
		if err != nil {
			b.Fatal(err)
		}
		if err := driver.SaveDraft(entry); err != nil {
			b.Fatal(err)
		}
		entry.Status = posting.EntryPosted
		if _, err := driver.PostAndSequence(entry); err != nil {
			b.Fatal(err)
		}
	}
}
