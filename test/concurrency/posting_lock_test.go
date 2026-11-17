package concurrency

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

func TestKeyedPostingLocker_SerializesSameJournalOnly(t *testing.T) {
	locker := posting.NewKeyedPostingLocker()
	companyID := uuid.New()
	journalA := uuid.New()
	journalB := uuid.New()

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	done := make(chan error, 2)

	go func() {
		done <- locker.WithinPostingLock(context.Background(), companyID, journalA, func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	// Same journal must wait for the first critical section.
	go func() {
		done <- locker.WithinPostingLock(context.Background(), companyID, journalA, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second same-journal lock entered before the first released")
	case <-time.After(50 * time.Millisecond):
	}

	// A different journal must not be blocked.
	otherDone := make(chan error, 1)
	go func() {
		otherDone <- locker.WithinPostingLock(context.Background(), companyID, journalB, func(context.Context) error {
			return nil
		})
	}()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatalf("other journal lock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("different journal was serialized by the same-journal lock")
	}

	close(releaseFirst)
	if err := <-done; err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("second lock: %v", err)
	}
}

func TestPostingConcurrency_SameJournalKeepsContiguousSequences(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	masterData := driver.MasterDataStore()

	companyID := types.CompanyID(uuid.MustParse("10000000-0000-4000-8000-0000000000c1"))
	cashAccountID := types.AccountID(uuid.MustParse("20000000-0000-4000-8000-0000000000c1"))
	revenueAccountID := types.AccountID(uuid.MustParse("20000000-0000-4000-8000-0000000000c2"))
	yearID := types.FiscalYearID(uuid.MustParse("30000000-0000-4000-8000-0000000000c1"))
	periodID := types.FiscalPeriodID(uuid.MustParse("40000000-0000-4000-8000-0000000000c1"))
	journalID := publicmasterdata.NewJournalID()

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "CONC", LegalName: "Concurrency Ltd",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "CONC", Name: "Concurrency journal",
	}); err != nil {
		t.Fatalf("create journal: %v", err)
	}
	for _, account := range []publicmasterdata.Account{
		{
			ID: cashAccountID, CompanyID: companyID, Code: "1000", Name: "Cash",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		{
			ID: revenueAccountID, CompanyID: companyID, Code: "4000", Name: "Revenue",
			Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			t.Fatalf("create account %s: %v", account.Code, err)
		}
	}
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: start, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatalf("create fiscal year: %v", err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}

	engine, err := sdk.NewBuilder(sdk.WithStoragePorts(driver.Ports())).Build(ctx)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	defer engine.Close()

	const postings = 12
	amount, err := types.NewMoneyFromString("10.00", "USD")
	if err != nil {
		t.Fatalf("amount: %v", err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		t.Fatalf("zero: %v", err)
	}
	date := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	sequences := make([]int64, postings)
	errs := make(chan error, postings)
	var wg sync.WaitGroup
	for i := 0; i < postings; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			entry := ledger.JournalEntry{
				ID: types.NewJournalEntryID(), CompanyID: companyID,
				JournalID: types.JournalID(journalID), FiscalPeriodID: periodID,
				Status: ledger.EntryDraft, Classification: ledger.EntryClassRegular,
				EffectiveDate: date, PostingDate: date,
				Description: "concurrent posting", SourceModule: "concurrency-test",
				TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
				CreatedBy: "concurrency-test",
				Lines: []ledger.EntryLine{
					{
						ID: types.NewJournalEntryLineID(), AccountID: cashAccountID,
						TransactionAmount: amount, TransactionCurrency: "USD",
						ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
					},
					{
						ID: types.NewJournalEntryLineID(), AccountID: revenueAccountID,
						TransactionAmount: amount, TransactionCurrency: "USD",
						ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
					},
				},
			}
			posted, postErr := engine.Ledger().PostEntry(ctx, entry)
			if postErr != nil {
				errs <- postErr
				return
			}
			sequences[index] = int64(posted.SequenceNumber)
		}(i)
	}
	wg.Wait()
	close(errs)
	for postErr := range errs {
		t.Fatalf("concurrent posting failed: %v", postErr)
	}

	seen := make(map[int64]bool, postings)
	for _, sequence := range sequences {
		if sequence <= 0 {
			t.Fatalf("posting returned non-positive sequence %d", sequence)
		}
		if seen[sequence] {
			t.Fatalf("duplicate sequence %d across concurrent postings", sequence)
		}
		seen[sequence] = true
	}
	for expected := int64(1); expected <= postings; expected++ {
		if !seen[expected] {
			t.Fatalf("missing sequence %d: sequences=%v", expected, sequences)
		}
	}
}
