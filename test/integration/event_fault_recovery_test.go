package integration_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
	"github.com/shopspring/decimal"
)

type eventRecoveryFixture struct {
	adapter   *memory.Adapter
	companyID types.CompanyID
	effective time.Time
	draft     ledger.JournalEntry
}

func newEventRecoveryFixture(t *testing.T) *eventRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	adapter := memory.New()
	masterData := adapter.MasterDataStore()
	companyID := types.NewCompanyID()
	journalID := publicmasterdata.NewJournalID()
	debitID := types.NewAccountID()
	creditID := types.NewAccountID()
	yearID := types.NewFiscalYearID()
	periodID := types.NewFiscalPeriodID()
	effective := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "EVENT-RECOVERY", LegalName: "Event Recovery Test",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "EVENTS", Name: "Events",
	}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []publicmasterdata.Account{
		{ID: debitID, CompanyID: companyID, Code: "1000", Name: "Cash", Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit, Currency: "USD", IsActive: true},
		{ID: creditID, CompanyID: companyID, Code: "4000", Name: "Revenue", Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit, Currency: "USD", IsActive: true},
	} {
		if err := masterData.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	yearStart := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: yearStart, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID, Number: 1, Name: "January",
		StartDate: yearStart, EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatal(err)
	}

	amount, err := types.NewMoneyFromString("100.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := types.NewMoneyFromString("0", "USD")
	if err != nil {
		t.Fatal(err)
	}
	draft := ledger.JournalEntry{
		ID: types.NewJournalEntryID(), CompanyID: companyID, JournalID: types.JournalID(journalID),
		FiscalPeriodID: periodID, Status: ledger.EntryDraft, EffectiveDate: effective, PostingDate: effective,
		Description: "Recovered event", SourceModule: "event-recovery-test",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), CreatedBy: "event-recovery-test",
		Lines: []ledger.EntryLine{
			{ID: types.NewJournalEntryLineID(), AccountID: debitID, TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero},
			{ID: types.NewJournalEntryLineID(), AccountID: creditID, TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount},
		},
	}
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("close memory adapter: %v", err)
		}
	})
	return &eventRecoveryFixture{adapter: adapter, companyID: companyID, effective: effective, draft: draft}
}

func (f *eventRecoveryFixture) event(key string) events.AccountingEvent {
	draft := f.draft
	draft.Lines = append([]ledger.EntryLine(nil), f.draft.Lines...)
	return events.AccountingEvent{
		EventID: "event-1", EventType: "sale", CompanyID: f.companyID,
		EffectiveDate: f.effective, IdempotencyKey: key, JournalDraft: &draft,
	}
}

func buildRecoveryEngine(t *testing.T, ports storage.Ports, options ...sdk.Option) sdk.Engine {
	t.Helper()
	allOptions := append([]sdk.Option{sdk.WithStoragePorts(ports)}, options...)
	engine, err := sdk.NewBuilder(allOptions...).Build(context.Background())
	if err != nil {
		t.Fatalf("build SDK engine: %v", err)
	}
	return engine
}

func closeRecoveryEngine(t *testing.T, engine sdk.Engine) {
	t.Helper()
	if err := engine.Close(); err != nil {
		t.Errorf("close SDK engine: %v", err)
	}
}

func (f *eventRecoveryFixture) assertOnePosting(t *testing.T) {
	t.Helper()
	balance, err := f.adapter.Reporting().GetTrialBalance(context.Background(), f.companyID, f.effective, "USD")
	if err != nil {
		t.Fatalf("read trial balance: %v", err)
	}
	if !balance.TotalDebit.Amount().Equal(decimal.NewFromInt(100)) ||
		!balance.TotalCredit.Amount().Equal(decimal.NewFromInt(100)) {
		t.Fatalf("trial balance = debit %s / credit %s, want exactly one balanced 100 posting",
			balance.TotalDebit, balance.TotalCredit)
	}
}

func TestEventValidationFaultIsRecordedAndDoesNotBlockFollowingEvents(t *testing.T) {
	fixture := newEventRecoveryFixture(t)
	ports := fixture.adapter.Ports()
	engine := buildRecoveryEngine(t, ports)
	defer closeRecoveryEngine(t, engine)

	invalid := fixture.event("invalid-draft-key")
	invalidDraft := *invalid.JournalDraft
	invalidDraft.Lines[0].AccountID = types.NewAccountID()
	invalid.JournalDraft = &invalidDraft
	_, err := engine.Events().ProcessEvent(context.Background(), invalid)
	if err == nil {
		t.Fatal("invalid draft unexpectedly succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "account not found") {
		t.Fatalf("invalid draft error = %v, want account-not-found rejection", err)
	}

	record, found, err := ports.Events.Idempotency.GetPublicEventIngestion(
		context.Background(), uuid.UUID(fixture.companyID), "invalid-draft-key")
	if err != nil || !found {
		t.Fatalf("read rejected event record: found=%t err=%v", found, err)
	}
	if !publicevents.IsTerminalFailure(record.Status) ||
		!strings.Contains(strings.ToLower(record.FailureReason), "account not found") {
		t.Fatalf("rejected event record = status %q reason %q, want terminal failure with reason",
			record.Status, record.FailureReason)
	}

	posted, err := engine.Events().ProcessEvent(context.Background(), fixture.event("valid-after-invalid-key"))
	if err != nil {
		t.Fatalf("valid event after rejected event: %v", err)
	}
	if posted.Status != ledger.EntryPosted {
		t.Fatalf("following event status = %q, want posted", posted.Status)
	}
	fixture.assertOnePosting(t)
}

type faultUnitOfWork struct {
	storage.UnitOfWork
	posting storage.PostingWriter
}

func (u *faultUnitOfWork) Posting() storage.PostingWriter { return u.posting }

type faultUnitOfWorkRunner struct {
	storage.UnitOfWorkRunner
	posting *faultPostingWriter
}

func (r *faultUnitOfWorkRunner) Execute(ctx context.Context, fn func(context.Context, storage.UnitOfWork) error) error {
	return r.UnitOfWorkRunner.Execute(ctx, func(txCtx context.Context, uow storage.UnitOfWork) error {
		r.posting.PostingWriter = uow.Posting()
		return fn(txCtx, &faultUnitOfWork{UnitOfWork: uow, posting: r.posting})
	})
}

func (r *faultUnitOfWorkRunner) ExecuteWithOptions(ctx context.Context, opts storage.TxOptions, fn func(context.Context, storage.UnitOfWork) error) error {
	return r.UnitOfWorkRunner.ExecuteWithOptions(ctx, opts, func(txCtx context.Context, uow storage.UnitOfWork) error {
		r.posting.PostingWriter = uow.Posting()
		return fn(txCtx, &faultUnitOfWork{UnitOfWork: uow, posting: r.posting})
	})
}

type faultPostingWriter struct {
	storage.PostingWriter
	mu       sync.Mutex
	failures int
	err      error
}

// plainRecoveryPostingPort forwards CommitPosting while hiding the
// driver-backed idempotent posting port, forcing the posting flow through the
// unit-of-work seam that faultPostingWriter wraps.
type plainRecoveryPostingPort struct {
	storage.PostingWriter
}

func (p *faultPostingWriter) CommitPosting(ctx context.Context, commit storage.PostingCommit) (ledger.JournalEntry, error) {
	p.mu.Lock()
	if p.failures > 0 {
		p.failures--
		err := p.err
		p.mu.Unlock()
		return ledger.JournalEntry{}, err
	}
	p.mu.Unlock()
	return p.PostingWriter.CommitPosting(ctx, commit)
}

func TestEventPostingFailureRemainsRetryableAndRecovers(t *testing.T) {
	fixture := newEventRecoveryFixture(t)
	ports := fixture.adapter.Ports()
	// The fault is injected at the unit-of-work posting seam. Hide the
	// driver-backed idempotent posting port so the flow runs through that
	// seam, exactly like adapters that only implement the public ports.
	ports.Posting = plainRecoveryPostingPort{PostingWriter: ports.Posting}
	fault := &faultPostingWriter{PostingWriter: ports.Posting, failures: 1, err: storage.ErrSerializationFailure}
	ports.UOWRunner = &faultUnitOfWorkRunner{UnitOfWorkRunner: ports.UOWRunner, posting: fault}
	engine := buildRecoveryEngine(t, ports)
	defer closeRecoveryEngine(t, engine)

	_, err := engine.Events().ProcessEvent(context.Background(), fixture.event("transient-event-key"))
	if !errors.Is(err, storage.ErrSerializationFailure) {
		t.Fatalf("first processing error = %v, want serialization failure", err)
	}
	record, found, err := ports.Events.Idempotency.GetPublicEventIngestion(
		context.Background(), uuid.UUID(fixture.companyID), "transient-event-key")
	if err != nil || !found || !publicevents.IsRetryableFailure(record.Status) {
		t.Fatalf("failed event record = status %q found=%t err=%v, want retryable failure",
			record.Status, found, err)
	}

	posted, err := engine.Events().ProcessEvent(context.Background(), fixture.event("transient-event-key"))
	if err != nil {
		t.Fatalf("retry transient event: %v", err)
	}
	if posted.Status != ledger.EntryPosted || !publicevents.IsCompleted(recordStatus(t, ports, fixture, "transient-event-key")) {
		t.Fatalf("retry did not complete event; posted status=%q", posted.Status)
	}
	fixture.assertOnePosting(t)
}

func recordStatus(t *testing.T, ports storage.Ports, fixture *eventRecoveryFixture, key string) string {
	t.Helper()
	record, found, err := ports.Events.Idempotency.GetPublicEventIngestion(
		context.Background(), uuid.UUID(fixture.companyID), key)
	if err != nil || !found {
		t.Fatalf("read event record %q: found=%t err=%v", key, found, err)
	}
	return record.Status
}

func TestEventDuplicateDeliveryAfterEngineRestartPostsOnce(t *testing.T) {
	fixture := newEventRecoveryFixture(t)
	ports := fixture.adapter.Ports()
	firstEngine := buildRecoveryEngine(t, ports)
	first, err := firstEngine.Events().ProcessEvent(context.Background(), fixture.event("restart-event-key"))
	if err != nil {
		t.Fatalf("first event delivery: %v", err)
	}
	if err := firstEngine.Close(); err != nil {
		t.Fatalf("close first engine: %v", err)
	}

	secondEngine := buildRecoveryEngine(t, ports)
	defer closeRecoveryEngine(t, secondEngine)
	replayed, err := secondEngine.Events().ProcessEvent(context.Background(), fixture.event("restart-event-key"))
	if err != nil {
		t.Fatalf("redeliver event after restart: %v", err)
	}
	if replayed.ID != first.ID || replayed.Status != ledger.EntryPosted {
		t.Fatalf("replay = %s/%q, want original posted entry %s", replayed.ID, replayed.Status, first.ID)
	}
	fixture.assertOnePosting(t)
}

func TestEventRecoveryResumesStartedRecordAfterCrash(t *testing.T) {
	fixture := newEventRecoveryFixture(t)
	ports := fixture.adapter.Ports()
	draft := fixture.draft
	if _, claimed, err := ports.Events.Idempotency.ClaimPublicEventIngestion(
		context.Background(), publicevents.Ingestion{
			CompanyID: uuid.UUID(fixture.companyID), IdempotencyKey: "crash-recovery-key",
			EventType: "sale", EntryID: uuid.UUID(draft.ID), Entry: draft, Status: publicevents.StatusStarted,
		}); err != nil || !claimed {
		t.Fatalf("seed pre-crash event claim: claimed=%t err=%v", claimed, err)
	}

	engine := buildRecoveryEngine(t, ports, sdk.WithEventIdempotencyWaitPolicy(10*time.Millisecond, time.Millisecond))
	defer closeRecoveryEngine(t, engine)
	posted, err := engine.Events().ProcessEvent(context.Background(), fixture.event("crash-recovery-key"))
	if err != nil {
		t.Fatalf("recover event after restart: %v", err)
	}
	if posted.ID != draft.ID || posted.Status != ledger.EntryPosted {
		t.Fatalf("recovered entry = %s/%q, want posted %s", posted.ID, posted.Status, draft.ID)
	}
	if status := recordStatus(t, ports, fixture, "crash-recovery-key"); !publicevents.IsCompleted(status) {
		t.Fatalf("recovered idempotency status = %q, want completed", status)
	}
	fixture.assertOnePosting(t)
}
