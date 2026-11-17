package atomic

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

// faultRunner injects a failure into CommitPosting while still driving the
// adapter's real unit of work. A failure after sequence allocation must roll
// back the sequence and every other posting effect.
type faultRunner struct {
	inner    storage.UnitOfWorkRunner
	fail     atomic.Bool
	executes atomic.Int64
}

func (r *faultRunner) Execute(ctx context.Context, fn func(context.Context, storage.UnitOfWork) error) error {
	r.executes.Add(1)
	return r.inner.Execute(ctx, func(txCtx context.Context, uow storage.UnitOfWork) error {
		return fn(txCtx, &faultUnitOfWork{UnitOfWork: uow, fail: &r.fail})
	})
}

func (r *faultRunner) ExecuteWithOptions(ctx context.Context, opts storage.TxOptions, fn func(context.Context, storage.UnitOfWork) error) error {
	r.executes.Add(1)
	return r.inner.ExecuteWithOptions(ctx, opts, func(txCtx context.Context, uow storage.UnitOfWork) error {
		return fn(txCtx, &faultUnitOfWork{UnitOfWork: uow, fail: &r.fail})
	})
}

type faultUnitOfWork struct {
	storage.UnitOfWork
	fail *atomic.Bool
}

func (u *faultUnitOfWork) Posting() storage.PostingWriter {
	if u.fail.Load() {
		return failingPostingWriter{PostingWriter: u.UnitOfWork.Posting()}
	}
	return u.UnitOfWork.Posting()
}

type failingPostingWriter struct {
	storage.PostingWriter
}

func (failingPostingWriter) CommitPosting(context.Context, storage.PostingCommit) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, storage.ErrSerializationFailure
}

type fixture struct {
	engine      sdk.Engine
	runner      *faultRunner
	companyID   types.CompanyID
	journalID   types.JournalID
	periodID    types.FiscalPeriodID
	cashAccount types.AccountID
	revAccount  types.AccountID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	driver := memory.New()
	masterData := driver.MasterDataStore()

	f := fixture{
		companyID:   types.CompanyID(uuid.MustParse("10000000-0000-4000-8000-0000000000d1")),
		cashAccount: types.AccountID(uuid.MustParse("20000000-0000-4000-8000-0000000000d1")),
		revAccount:  types.AccountID(uuid.MustParse("20000000-0000-4000-8000-0000000000d2")),
		periodID:    types.FiscalPeriodID(uuid.MustParse("40000000-0000-4000-8000-0000000000d1")),
	}
	yearID := types.FiscalYearID(uuid.MustParse("30000000-0000-4000-8000-0000000000d1"))
	journalID := publicmasterdata.NewJournalID()
	f.journalID = types.JournalID(journalID)

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: f.companyID, Code: "ATOMIC", LegalName: "Atomic Ltd",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: journalID, CompanyID: f.companyID, Code: "ATOMIC", Name: "Atomic journal",
	}); err != nil {
		t.Fatalf("create journal: %v", err)
	}
	for _, account := range []publicmasterdata.Account{
		{
			ID: f.cashAccount, CompanyID: f.companyID, Code: "1000", Name: "Cash",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		{
			ID: f.revAccount, CompanyID: f.companyID, Code: "4000", Name: "Revenue",
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
		ID: yearID, CompanyID: f.companyID, Name: "FY2026",
		StartDate: start, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatalf("create fiscal year: %v", err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: f.periodID, CompanyID: f.companyID, FiscalYearID: yearID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}

	ports := driver.Ports()
	f.runner = &faultRunner{inner: ports.UOWRunner}
	ports.UOWRunner = f.runner
	engine, err := sdk.NewBuilder(sdk.WithStoragePorts(ports)).Build(ctx)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	f.engine = engine
	t.Cleanup(func() { _ = engine.Close() })
	return f
}

func (f fixture) entry(t *testing.T) ledger.JournalEntry {
	t.Helper()
	amount, err := types.NewMoneyFromString("25.00", "USD")
	if err != nil {
		t.Fatalf("amount: %v", err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		t.Fatalf("zero: %v", err)
	}
	date := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
	return ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: f.companyID,
		JournalID: f.journalID, FiscalPeriodID: f.periodID,
		Status: ledger.EntryDraft, Classification: ledger.EntryClassRegular,
		EffectiveDate: date, PostingDate: date,
		Description: "atomic posting", SourceModule: "atomic-test",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
		CreatedBy: "atomic-test",
		Lines: []ledger.EntryLine{
			{
				ID: types.NewJournalEntryLineID(), AccountID: f.cashAccount,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.NewJournalEntryLineID(), AccountID: f.revAccount,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}
}

func TestAtomicPosting_FailureAfterSequenceRollsBackAndDoesNotBurnNumber(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	entry := f.entry(t)

	f.runner.fail.Store(true)
	if _, err := f.engine.Ledger().PostEntry(ctx, entry); !errors.Is(err, storage.ErrSerializationFailure) {
		t.Fatalf("first PostEntry error = %v, want %v", err, storage.ErrSerializationFailure)
	}
	if f.runner.executes.Load() == 0 {
		t.Fatal("posting did not use a unit of work")
	}

	stored, err := f.engine.Ledger().GetEntry(ctx, f.companyID, entry.ID)
	if err != nil {
		t.Fatalf("draft was not preserved after rollback: %v", err)
	}
	if stored.Status != ledger.EntryDraft {
		t.Fatalf("status after rollback = %q, want draft", stored.Status)
	}

	f.runner.fail.Store(false)
	posted, err := f.engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if posted.Status != ledger.EntryPosted {
		t.Fatalf("status after retry = %q, want posted", posted.Status)
	}
	if posted.SequenceNumber != 1 {
		t.Fatalf("sequence after rollback = %d, want 1 (no burned number)", posted.SequenceNumber)
	}
}

func TestAtomicPosting_SuccessUsesSingleUnitOfWork(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	entry := f.entry(t)

	posted, err := f.engine.Ledger().PostEntry(ctx, entry)
	if err != nil {
		t.Fatalf("PostEntry: %v", err)
	}
	if posted.Status != ledger.EntryPosted || posted.SequenceNumber != 1 {
		t.Fatalf("posted = %s sequence=%d", posted.Status, posted.SequenceNumber)
	}
	if got := f.runner.executes.Load(); got != 1 {
		t.Fatalf("unit-of-work executions = %d, want 1", got)
	}
}

// TestAtomicPosting_ConcurrentFailuresNeverLeavePartialState combines rollback
// with concurrency: every failed attempt must roll back its sequence, and every
// successful attempt must receive a unique contiguous number.
func TestAtomicPosting_ConcurrentFailuresNeverLeavePartialState(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	const attempts = 6
	entries := make([]ledger.JournalEntry, attempts)
	for i := range entries {
		entries[i] = f.entry(t)
	}

	f.runner.fail.Store(true)
	var wg sync.WaitGroup
	for i := range entries {
		wg.Add(1)
		go func(entry ledger.JournalEntry) {
			defer wg.Done()
			if _, err := f.engine.Ledger().PostEntry(ctx, entry); !errors.Is(err, storage.ErrSerializationFailure) {
				t.Errorf("expected serialization failure, got %v", err)
			}
		}(entries[i])
	}
	wg.Wait()

	f.runner.fail.Store(false)
	sequences := make(map[int64]bool, attempts)
	for i := range entries {
		posted, err := f.engine.Ledger().PostEntry(ctx, entries[i])
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if posted.SequenceNumber <= 0 || sequences[posted.SequenceNumber] {
			t.Fatalf("invalid/duplicate sequence %d after rollback", posted.SequenceNumber)
		}
		sequences[posted.SequenceNumber] = true
	}
	for expected := int64(1); expected <= attempts; expected++ {
		if !sequences[expected] {
			t.Fatalf("missing sequence %d after rollback: %v", expected, sequences)
		}
	}
}
