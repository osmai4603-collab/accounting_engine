package accounting_engine_test

import (
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/memory"

	"github.com/google/uuid"
)

func BenchmarkSinglePosting(b *testing.B) {
	driver := memory.New()
	cash, revenue := uuid.New(), uuid.New()
	_ = driver.CreateAccount(masterdata.Account{ID: cash, Code: "BEN-CASH", Name: "Cash", AccountType: masterdata.AccountTypeAsset})
	_ = driver.CreateAccount(masterdata.Account{ID: revenue, Code: "BEN-REV", Name: "Revenue", AccountType: masterdata.AccountTypeIncome})
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
		if err := driver.PostAtomic(entry); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBulkPosting10000(b *testing.B) {
	driver := memory.New()
	entries := make([]posting.JournalEntry, 10000)
	amount, _ := financialtypes.NewMoneyFromString("1", "USD")
	journalID := uuid.New()
	debitAccount, creditAccount := uuid.New(), uuid.New()
	for i := range entries {
		entry, err := posting.NewJournalEntry(journalID, time.Now().UTC(), fmt.Sprintf("maker-%d", i),
			[]posting.JournalEntryLine{
				{ID: uuid.New(), AccountID: debitAccount, Debit: amount},
				{ID: uuid.New(), AccountID: creditAccount, Credit: amount},
			})
		if err != nil {
			b.Fatal(err)
		}
		entries[i] = entry
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := driver.PostBulk(entries); err != nil {
			b.Fatal(err)
		}
	}
}
