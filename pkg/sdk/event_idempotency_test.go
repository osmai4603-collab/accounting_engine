package sdk_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type publicEventFixture struct {
	driver    *memory.Driver
	companyID types.CompanyID
	entryID   types.JournalEntryID
	draft     ledger.JournalEntry
	effective time.Time
}

func newPublicEventFixture(t *testing.T) *publicEventFixture {
	t.Helper()
	ctx := context.Background()
	driver := memory.New()
	masterData := driver.MasterDataStore()

	companyID := types.NewCompanyID()
	journalID := publicmasterdata.NewJournalID()
	debitID := types.AccountID(uuid.New())
	creditID := types.AccountID(uuid.New())
	yearID := types.FiscalYearID(uuid.New())
	periodID := types.FiscalPeriodID(uuid.New())
	entryID := types.JournalEntryID(uuid.New())
	effective := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "EVENT-IDEMPOTENCY", LegalName: "Event Idempotency Test",
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
		{
			ID: debitID, CompanyID: companyID, Code: "1000", Name: "Cash",
			Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
			Currency: "USD", IsActive: true,
		},
		{
			ID: creditID, CompanyID: companyID, Code: "4000", Name: "Revenue",
			Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit,
			Currency: "USD", IsActive: true,
		},
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
		ID: entryID, CompanyID: companyID, JournalID: types.JournalID(journalID), FiscalPeriodID: periodID,
		Status: ledger.EntryDraft, EffectiveDate: effective, PostingDate: effective,
		Description: "Idempotent event", SourceModule: "event-idempotency-test",
		TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), CreatedBy: "event-idempotency-test",
		Lines: []ledger.EntryLine{
			{
				ID: types.JournalEntryLineID(uuid.New()), AccountID: debitID,
				TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: amount, FunctionalCredit: zero,
			},
			{
				ID: types.JournalEntryLineID(uuid.New()), AccountID: creditID,
				TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
				FunctionalDebit: zero, FunctionalCredit: amount,
			},
		},
	}
	return &publicEventFixture{
		driver: driver, companyID: companyID, entryID: entryID, draft: draft, effective: effective,
	}
}

func (f *publicEventFixture) event(key string) events.AccountingEvent {
	draft := f.draft
	return events.AccountingEvent{
		EventID: "event-1", EventType: "sale", CompanyID: f.companyID,
		EffectiveDate: f.effective, IdempotencyKey: key, JournalDraft: &draft,
	}
}

func (f *publicEventFixture) assertNoPosting(t *testing.T) {
	t.Helper()
	balance, err := f.driver.GetTrialBalance(context.Background(), f.companyID, f.effective, "USD")
	if err != nil {
		t.Fatalf("read trial balance: %v", err)
	}
	if !balance.TotalDebit.Amount().IsZero() || !balance.TotalCredit.Amount().IsZero() {
		t.Fatalf("unexpected posting: debit %s / credit %s", balance.TotalDebit, balance.TotalCredit)
	}
}

func (f *publicEventFixture) assertSinglePosting(t *testing.T) {
	t.Helper()
	balance, err := f.driver.GetTrialBalance(context.Background(), f.companyID, f.effective, "USD")
	if err != nil {
		t.Fatalf("read trial balance: %v", err)
	}
	if !balance.TotalDebit.Amount().Equal(decimal.NewFromInt(100)) ||
		!balance.TotalCredit.Amount().Equal(decimal.NewFromInt(100)) {
		t.Fatalf("posted totals = debit %s / credit %s, want exactly one 100 posting",
			balance.TotalDebit, balance.TotalCredit)
	}
}

func buildEventEngine(t *testing.T, ports storage.Ports, opts ...sdk.Option) sdk.Engine {
	t.Helper()
	options := append([]sdk.Option{sdk.WithStoragePorts(ports)}, opts...)
	engine, err := sdk.NewBuilder(options...).Build(context.Background())
	if err != nil {
		t.Fatalf("build SDK engine: %v", err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close SDK engine: %v", err)
		}
	})
	return engine
}

func TestPublicEventIngestionDeduplicatesConcurrentSameKey(t *testing.T) {
	fixture := newPublicEventFixture(t)
	ports := fixture.driver.Ports()
	engine := buildEventEngine(t, ports)

	const callers = 12
	type outcome struct {
		entry ledger.JournalEntry
		err   error
	}
	results := make(chan outcome, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			entry, err := engine.Events().ProcessEvent(context.Background(), fixture.event("concurrent-key"))
			results <- outcome{entry: entry, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var firstID types.JournalEntryID
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent ProcessEvent: %v", result.err)
		}
		if result.entry.ID != fixture.entryID || result.entry.Status != ledger.EntryPosted {
			t.Fatalf("concurrent result = %s/%q, want posted %s",
				result.entry.ID, result.entry.Status, fixture.entryID)
		}
		if firstID == (types.JournalEntryID{}) {
			firstID = result.entry.ID
		} else if result.entry.ID != firstID {
			t.Fatalf("concurrent callers received different entries: %s and %s", firstID, result.entry.ID)
		}
	}
	fixture.assertSinglePosting(t)

	record, found, err := ports.Events.Idempotency.GetPublicEventIngestion(
		context.Background(), uuid.UUID(fixture.companyID), "concurrent-key")
	if err != nil || !found {
		t.Fatalf("read idempotency record: found=%t err=%v", found, err)
	}
	if !publicevents.IsCompleted(record.Status) || record.EntryID != uuid.UUID(fixture.entryID) {
		t.Fatalf("idempotency record = %q entry %s, want completed %s", record.Status, record.EntryID, fixture.entryID)
	}
	if record.PayloadHashVersion != sdk.PublicEventPayloadHashVersion {
		t.Fatalf("payload hash version = %q, want %q", record.PayloadHashVersion, sdk.PublicEventPayloadHashVersion)
	}
}

func TestPublicEventIngestionDifferentPayloadConflicts(t *testing.T) {
	fixture := newPublicEventFixture(t)
	engine := buildEventEngine(t, fixture.driver.Ports())

	if _, err := engine.Events().ProcessEvent(context.Background(), fixture.event("conflict-key")); err != nil {
		t.Fatalf("first ProcessEvent: %v", err)
	}
	conflicting := fixture.event("conflict-key")
	conflicting.EventID = "event-2"
	_, err := engine.Events().ProcessEvent(context.Background(), conflicting)
	if !errors.Is(err, sdk.ErrIdempotencyKeyConflict) {
		t.Fatalf("conflicting payload error = %v, want %v", err, sdk.ErrIdempotencyKeyConflict)
	}
	if !errors.Is(err, publicapplication.ErrConflict) {
		t.Fatalf("conflicting payload error = %v, want public conflict taxonomy", err)
	}
	fixture.assertSinglePosting(t)
}

func TestPublicEventIngestionSurvivesEngineRestart(t *testing.T) {
	fixture := newPublicEventFixture(t)
	ports := fixture.driver.Ports()

	firstEngine := buildEventEngine(t, ports)
	first, err := firstEngine.Events().ProcessEvent(context.Background(), fixture.event("restart-key"))
	if err != nil {
		t.Fatalf("first engine ProcessEvent: %v", err)
	}
	if err := firstEngine.Close(); err != nil {
		t.Fatalf("close first engine: %v", err)
	}

	// A second engine has its own process-local state. The durable adapter
	// must still return the original result without posting again.
	secondEngine := buildEventEngine(t, ports)
	second, err := secondEngine.Events().ProcessEvent(context.Background(), fixture.event("restart-key"))
	if err != nil {
		t.Fatalf("second engine ProcessEvent: %v", err)
	}
	if second.ID != first.ID || second.Status != ledger.EntryPosted {
		t.Fatalf("restart replay = %s/%q, want original %s", second.ID, second.Status, first.ID)
	}
	fixture.assertSinglePosting(t)
}

func TestPublicEventIngestionRejectsKeyWithoutDurableStore(t *testing.T) {
	fixture := newPublicEventFixture(t)
	ports := fixture.driver.Ports()
	ports.Events = publicevents.Stores{}
	engine := buildEventEngine(t, ports)

	_, err := engine.Events().ProcessEvent(context.Background(), fixture.event("no-durable-store"))
	if !errors.Is(err, sdk.ErrIdempotencyStateUnavailable) {
		t.Fatalf("ProcessEvent error = %v, want %v", err, sdk.ErrIdempotencyStateUnavailable)
	}
	if !errors.Is(err, publicapplication.ErrDependencyUnavailable) {
		t.Fatalf("ProcessEvent error = %v, want dependency-unavailable taxonomy", err)
	}
	balance, balanceErr := fixture.driver.GetTrialBalance(context.Background(), fixture.companyID, fixture.effective, "USD")
	if balanceErr != nil {
		t.Fatalf("read trial balance: %v", balanceErr)
	}
	if !balance.TotalDebit.Amount().IsZero() || !balance.TotalCredit.Amount().IsZero() {
		t.Fatalf("missing durable store still posted: debit %s credit %s", balance.TotalDebit, balance.TotalCredit)
	}
}

func TestPublicEventIngestionWaitsForInFlightThenReportsInProgress(t *testing.T) {
	fixture := newPublicEventFixture(t)
	ports := fixture.driver.Ports()
	scoped, ok := ports.Events.Idempotency.(sdk.PublicEventScopedIdempotencyStore)
	if !ok {
		t.Fatal("memory idempotency store does not implement the scoped capability")
	}
	companyUUID := uuid.UUID(fixture.companyID)
	if _, claimed, err := scoped.ClaimPublicEventIngestion(context.Background(), publicevents.Ingestion{
		CompanyID: companyUUID, IdempotencyKey: "in-flight-key", EventType: "sale",
		Status: publicevents.StatusStarted,
	}); err != nil || !claimed {
		t.Fatalf("seed in-flight record: claimed=%t err=%v", claimed, err)
	}

	engine := buildEventEngine(t, ports,
		sdk.WithEventIdempotencyWaitPolicy(20*time.Millisecond, 5*time.Millisecond))
	_, err := engine.Events().ProcessEvent(context.Background(), fixture.event("in-flight-key"))
	if !errors.Is(err, sdk.ErrIdempotencyInProgress) {
		t.Fatalf("ProcessEvent error = %v, want %v", err, sdk.ErrIdempotencyInProgress)
	}
	if !errors.Is(err, publicapplication.ErrConflict) {
		t.Fatalf("ProcessEvent error = %v, want conflict taxonomy", err)
	}
	fixture.assertNoPosting(t)
}

func TestPublicEventIngestionRejectsTerminalFailureRetry(t *testing.T) {
	fixture := newPublicEventFixture(t)
	ports := fixture.driver.Ports()
	companyUUID := uuid.UUID(fixture.companyID)
	if _, claimed, err := ports.Events.Idempotency.ClaimPublicEventIngestion(context.Background(), publicevents.Ingestion{
		CompanyID: companyUUID, IdempotencyKey: "terminal-key", EventType: "sale",
		Status: publicevents.StatusStarted,
	}); err != nil || !claimed {
		t.Fatalf("seed terminal record: claimed=%t err=%v", claimed, err)
	}
	if err := ports.Events.Idempotency.(publicevents.ScopedIdempotencyStore).FailPublicEventIngestionForSource(
		context.Background(), companyUUID, publicevents.DefaultSourceSystem, "terminal-key",
		publicevents.FailureClassTerminal, "invalid account mapping"); err != nil {
		t.Fatalf("record terminal failure: %v", err)
	}

	engine := buildEventEngine(t, ports)
	_, err := engine.Events().ProcessEvent(context.Background(), fixture.event("terminal-key"))
	if !errors.Is(err, sdk.ErrIdempotencyTerminalFailure) {
		t.Fatalf("ProcessEvent error = %v, want %v", err, sdk.ErrIdempotencyTerminalFailure)
	}
	if !errors.Is(err, publicapplication.ErrConflict) {
		t.Fatalf("ProcessEvent error = %v, want conflict taxonomy", err)
	}
	fixture.assertNoPosting(t)
}

type flakyUnitOfWork struct {
	storage.UnitOfWork
	posting storage.PostingWriter
}

func (u *flakyUnitOfWork) Posting() storage.PostingWriter { return u.posting }

type flakyUnitOfWorkRunner struct {
	storage.UnitOfWorkRunner
	posting *flakyPostingPort
}

func (r *flakyUnitOfWorkRunner) Execute(ctx context.Context, fn func(context.Context, storage.UnitOfWork) error) error {
	return r.UnitOfWorkRunner.Execute(ctx, func(txCtx context.Context, uow storage.UnitOfWork) error {
		r.posting.PostingWriter = uow.Posting()
		return fn(txCtx, &flakyUnitOfWork{UnitOfWork: uow, posting: r.posting})
	})
}

func (r *flakyUnitOfWorkRunner) ExecuteWithOptions(ctx context.Context, opts storage.TxOptions, fn func(context.Context, storage.UnitOfWork) error) error {
	return r.UnitOfWorkRunner.ExecuteWithOptions(ctx, opts, func(txCtx context.Context, uow storage.UnitOfWork) error {
		r.posting.PostingWriter = uow.Posting()
		return fn(txCtx, &flakyUnitOfWork{UnitOfWork: uow, posting: r.posting})
	})
}

type flakyPostingPort struct {
	storage.PostingWriter
	mu       sync.Mutex
	failures int
	err      error
}

// plainPostingPort forwards CommitPosting while hiding the driver-backed
// idempotent posting port, forcing the posting flow through the unit-of-work
// seam that flakyPostingPort wraps.
type plainPostingPort struct {
	storage.PostingWriter
}

func (p *flakyPostingPort) CommitPosting(ctx context.Context, commit storage.PostingCommit) (ledger.JournalEntry, error) {
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

func TestPublicEventIngestionRecoversFailureBeforeCommit(t *testing.T) {
	fixture := newPublicEventFixture(t)
	ports := fixture.driver.Ports()
	// The fault is injected at the unit-of-work posting seam. Hide the
	// driver-backed idempotent posting port so the flow runs through that
	// seam, exactly like adapters that only implement the public ports.
	ports.Posting = plainPostingPort{PostingWriter: ports.Posting}
	flake := &flakyPostingPort{
		PostingWriter: ports.Posting,
		failures:      1,
		err:           storage.ErrSerializationFailure,
	}
	ports.UOWRunner = &flakyUnitOfWorkRunner{UnitOfWorkRunner: ports.UOWRunner, posting: flake}
	engine := buildEventEngine(t, ports)

	_, err := engine.Events().ProcessEvent(context.Background(), fixture.event("transient-key"))
	if !errors.Is(err, storage.ErrSerializationFailure) {
		t.Fatalf("first ProcessEvent error = %v, want transient serialization failure", err)
	}
	record, found, err := ports.Events.Idempotency.GetPublicEventIngestion(
		context.Background(), uuid.UUID(fixture.companyID), "transient-key")
	if err != nil || !found {
		t.Fatalf("read transient record: found=%t err=%v", found, err)
	}
	if !publicevents.IsRetryableFailure(record.Status) {
		t.Fatalf("transient record status = %q, want retryable_failure", record.Status)
	}

	posted, err := engine.Events().ProcessEvent(context.Background(), fixture.event("transient-key"))
	if err != nil {
		t.Fatalf("retry after transient failure: %v", err)
	}
	if posted.ID != fixture.entryID || posted.Status != ledger.EntryPosted {
		t.Fatalf("retried entry = %s/%q, want posted %s", posted.ID, posted.Status, fixture.entryID)
	}
	record, found, err = ports.Events.Idempotency.GetPublicEventIngestion(
		context.Background(), uuid.UUID(fixture.companyID), "transient-key")
	if err != nil || !found || !publicevents.IsCompleted(record.Status) {
		t.Fatalf("retried record = %+v found=%t err=%v, want completed", record, found, err)
	}
	fixture.assertSinglePosting(t)
}

type failOnceCompleteStore struct {
	sdk.PublicEventScopedIdempotencyStore
	mu     sync.Mutex
	should bool
}

func (s *failOnceCompleteStore) CompletePublicEventIngestionForSource(
	ctx context.Context,
	companyID uuid.UUID,
	sourceSystem, idempotencyKey, payloadHash string,
	entry ledger.JournalEntry,
) error {
	s.mu.Lock()
	if s.should {
		s.should = false
		s.mu.Unlock()
		return errors.New("injected failure after entry commit")
	}
	s.mu.Unlock()
	return s.PublicEventScopedIdempotencyStore.CompletePublicEventIngestionForSource(
		ctx, companyID, sourceSystem, idempotencyKey, payloadHash, entry)
}

func TestPublicEventIngestionRecoversFailureAfterCommitBeforeResponse(t *testing.T) {
	fixture := newPublicEventFixture(t)
	ports := fixture.driver.Ports()
	scoped, ok := ports.Events.Idempotency.(sdk.PublicEventScopedIdempotencyStore)
	if !ok {
		t.Fatal("memory idempotency store does not implement the scoped capability")
	}
	failingStore := &failOnceCompleteStore{PublicEventScopedIdempotencyStore: scoped, should: true}
	ports.Events.Idempotency = failingStore

	engine := buildEventEngine(t, ports,
		sdk.WithEventIdempotencyWaitPolicy(20*time.Millisecond, 5*time.Millisecond))

	if _, err := engine.Events().ProcessEvent(context.Background(), fixture.event("post-commit-key")); err == nil {
		t.Fatal("first ProcessEvent unexpectedly succeeded")
	}
	record, found, err := scoped.GetPublicEventIngestionForSource(
		context.Background(), uuid.UUID(fixture.companyID), publicevents.DefaultSourceSystem, "post-commit-key")
	if err != nil || !found {
		t.Fatalf("read post-commit record: found=%t err=%v", found, err)
	}
	if !publicevents.IsStarted(record.Status) || record.EntryID != uuid.UUID(fixture.entryID) {
		t.Fatalf("record after completion failure = %q entry %s, want started %s",
			record.Status, record.EntryID, fixture.entryID)
	}

	recovered, err := engine.Events().ProcessEvent(context.Background(), fixture.event("post-commit-key"))
	if err != nil {
		t.Fatalf("retry after post-commit failure: %v", err)
	}
	if recovered.ID != fixture.entryID || recovered.Status != ledger.EntryPosted {
		t.Fatalf("recovered entry = %s/%q, want posted %s", recovered.ID, recovered.Status, fixture.entryID)
	}
	record, found, err = scoped.GetPublicEventIngestionForSource(
		context.Background(), uuid.UUID(fixture.companyID), publicevents.DefaultSourceSystem, "post-commit-key")
	if err != nil || !found || !publicevents.IsCompleted(record.Status) {
		t.Fatalf("recovered record = %+v found=%t err=%v, want completed", record, found, err)
	}
	fixture.assertSinglePosting(t)
}
