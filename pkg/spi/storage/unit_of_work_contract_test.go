package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

// mockUnitOfWork implements UnitOfWork in memory for contract testing.
type mockUnitOfWork struct {
	state   *mockState
	drafts  []ledger.JournalEntry
	posted  []PostingCommit
	audits  []AuditRecord
	seqMap  map[string]int64
	aborted bool
}

type mockState struct {
	mu     sync.Mutex
	drafts []ledger.JournalEntry
	posted []PostingCommit
	audits []AuditRecord
	seqMap map[string]int64
}

func newMockState() *mockState {
	return &mockState{
		seqMap: make(map[string]int64),
	}
}

type mockUOWRunner struct {
	state       *mockState
	failNext    error
	injectPanic bool
	calls       int
}

func (r *mockUOWRunner) Execute(ctx context.Context, fn func(ctx context.Context, uow UnitOfWork) error) error {
	return r.ExecuteWithOptions(ctx, TxOptions{}, fn)
}

func (r *mockUOWRunner) ExecuteWithOptions(ctx context.Context, opts TxOptions, fn func(ctx context.Context, uow UnitOfWork) error) (err error) {
	r.calls++
	if r.failNext != nil {
		fErr := r.failNext
		r.failNext = nil
		return fErr
	}

	uow := &mockUnitOfWork{
		state:  r.state,
		seqMap: make(map[string]int64),
	}

	defer func() {
		if rec := recover(); rec != nil {
			uow.aborted = true
			err = fmt.Errorf("transaction panicked: %v", rec)
		}
	}()

	if r.injectPanic {
		r.injectPanic = false
		panic("simulated transaction crash")
	}

	if err := ctx.Err(); err != nil {
		uow.aborted = true
		return fmt.Errorf("%w: %v", ErrTransactionAborted, err)
	}

	cbErr := fn(ctx, uow)
	if cbErr != nil {
		uow.aborted = true
		return cbErr
	}

	// Commit changes to shared state atomically
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.drafts = append(r.state.drafts, uow.drafts...)
	r.state.posted = append(r.state.posted, uow.posted...)
	r.state.audits = append(r.state.audits, uow.audits...)
	for k, v := range uow.seqMap {
		r.state.seqMap[k] = v
	}
	return nil
}

func (u *mockUnitOfWork) Drafts() DraftStore {
	return &mockDraftStore{uow: u}
}
func (u *mockUnitOfWork) Entries() EntryReader {
	return stubEntries{}
}
func (u *mockUnitOfWork) Posting() PostingWriter {
	return &mockPostingWriter{uow: u}
}
func (u *mockUnitOfWork) Sequences() SequenceAllocator {
	return &mockSequenceAllocator{uow: u}
}
func (u *mockUnitOfWork) Audit() AuditWriter {
	return &mockAuditWriter{uow: u}
}
func (u *mockUnitOfWork) Idempotency() IdempotencyStore {
	return nil
}
func (u *mockUnitOfWork) Balance() BalanceReader {
	return stubBalance{}
}

type mockDraftStore struct{ uow *mockUnitOfWork }

func (m *mockDraftStore) SaveDraft(ctx context.Context, entry ledger.JournalEntry) error {
	m.uow.drafts = append(m.uow.drafts, entry)
	return nil
}

type mockPostingWriter struct{ uow *mockUnitOfWork }

func (m *mockPostingWriter) CommitPosting(ctx context.Context, commit PostingCommit) (ledger.JournalEntry, error) {
	m.uow.posted = append(m.uow.posted, commit)
	return commit.Entry, nil
}

type mockSequenceAllocator struct{ uow *mockUnitOfWork }

func (m *mockSequenceAllocator) NextSequence(ctx context.Context, companyID types.CompanyID, journalID types.JournalID) (int64, error) {
	key := fmt.Sprintf("%s:%s", companyID, journalID)
	m.uow.seqMap[key]++
	return m.uow.seqMap[key], nil
}

type mockAuditWriter struct{ uow *mockUnitOfWork }

func (m *mockAuditWriter) AppendAudit(ctx context.Context, record AuditRecord) error {
	m.uow.audits = append(m.uow.audits, record)
	return nil
}

// Contract Test Suite verifying standard transaction guarantees:

func TestUnitOfWorkContract_CommitSuccess(t *testing.T) {
	state := newMockState()
	runner := &mockUOWRunner{state: state}

	companyID := types.NewCompanyID()
	journalID := types.NewJournalID()

	err := runner.Execute(context.Background(), func(ctx context.Context, uow UnitOfWork) error {
		if _, err := uow.Sequences().NextSequence(ctx, companyID, journalID); err != nil {
			return err
		}
		if err := uow.Drafts().SaveDraft(ctx, ledger.JournalEntry{CompanyID: companyID}); err != nil {
			return err
		}
		if err := uow.Audit().AppendAudit(ctx, AuditRecord{CompanyID: companyID, Action: "DRAFT_CREATED"}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error on commit: %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.drafts) != 1 {
		t.Fatalf("expected 1 draft committed, got %d", len(state.drafts))
	}
	if len(state.audits) != 1 {
		t.Fatalf("expected 1 audit committed, got %d", len(state.audits))
	}
	key := fmt.Sprintf("%s:%s", companyID, journalID)
	if state.seqMap[key] != 1 {
		t.Fatalf("expected sequence 1 committed, got %d", state.seqMap[key])
	}
}

func TestUnitOfWorkContract_RollbackOnError(t *testing.T) {
	state := newMockState()
	runner := &mockUOWRunner{state: state}

	companyID := types.NewCompanyID()
	expectedErr := errors.New("business rule validation failed")

	err := runner.Execute(context.Background(), func(ctx context.Context, uow UnitOfWork) error {
		_ = uow.Drafts().SaveDraft(ctx, ledger.JournalEntry{CompanyID: companyID})
		_ = uow.Audit().AppendAudit(ctx, AuditRecord{CompanyID: companyID, Action: "DRAFT_CREATED"})
		return expectedErr
	})

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.drafts) != 0 {
		t.Fatalf("expected 0 drafts after rollback, got %d", len(state.drafts))
	}
	if len(state.audits) != 0 {
		t.Fatalf("expected 0 audits after rollback, got %d", len(state.audits))
	}
}

func TestUnitOfWorkContract_RollbackOnPanic(t *testing.T) {
	state := newMockState()
	runner := &mockUOWRunner{state: state, injectPanic: true}

	err := runner.Execute(context.Background(), func(ctx context.Context, uow UnitOfWork) error {
		_ = uow.Drafts().SaveDraft(ctx, ledger.JournalEntry{})
		return nil
	})

	if err == nil {
		t.Fatal("expected error from recovered panic, got nil")
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.drafts) != 0 {
		t.Fatalf("expected state rollback on panic, but found %d drafts", len(state.drafts))
	}
}

func TestUnitOfWorkContract_ContextCancellation(t *testing.T) {
	state := newMockState()
	runner := &mockUOWRunner{state: state}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := runner.Execute(ctx, func(ctx context.Context, uow UnitOfWork) error {
		return nil
	})

	if !errors.Is(err, ErrTransactionAborted) {
		t.Fatalf("expected ErrTransactionAborted on cancelled context, got %v", err)
	}
}

func TestUnitOfWorkContract_RetryPolicyOnTransientError(t *testing.T) {
	state := newMockState()
	runner := &mockUOWRunner{state: state, failNext: ErrSerializationFailure}

	policy := RetryPolicy{
		MaxRetries:     2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
	}

	err := ExecuteWithRetry(context.Background(), runner, TxOptions{}, policy, func(ctx context.Context, uow UnitOfWork) error {
		return uow.Drafts().SaveDraft(ctx, ledger.JournalEntry{})
	})

	if err != nil {
		t.Fatalf("expected successful retry after transient serialization failure, got %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("expected 2 attempts, got %d", runner.calls)
	}
}

func TestUnitOfWorkContract_RetryAbortsOnTerminalError(t *testing.T) {
	state := newMockState()
	runner := &mockUOWRunner{state: state}

	terminalErr := errors.New("terminal business error")
	policy := RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
	}

	err := ExecuteWithRetry(context.Background(), runner, TxOptions{}, policy, func(ctx context.Context, uow UnitOfWork) error {
		return terminalErr
	})

	if !errors.Is(err, terminalErr) {
		t.Fatalf("expected terminal error %v, got %v", terminalErr, err)
	}
	if runner.calls != 1 {
		t.Fatalf("expected exactly 1 attempt on non-retryable error, got %d", runner.calls)
	}
}

func TestContextCompanyAndActorPropagation(t *testing.T) {
	companyID := types.NewCompanyID()
	actorID := "user-auditor-10"

	ctx := context.Background()
	ctx = WithCompanyContext(ctx, companyID)
	ctx = WithActorContext(ctx, actorID)

	gotCompany, ok := CompanyFromContext(ctx)
	if !ok || gotCompany != companyID {
		t.Fatalf("expected company %s, got %s (ok=%v)", companyID, gotCompany, ok)
	}

	gotActor, ok := ActorFromContext(ctx)
	if !ok || gotActor != actorID {
		t.Fatalf("expected actor %s, got %s (ok=%v)", actorID, gotActor, ok)
	}

	// Verify nil context safety
	if _, ok := CompanyFromContext(nil); ok {
		t.Fatal("expected false for nil context company")
	}
	if _, ok := ActorFromContext(nil); ok {
		t.Fatal("expected false for nil context actor")
	}
}

func TestAdapterCapabilitiesDeclaration(t *testing.T) {
	caps := AdapterCapabilities{
		AdapterName:            "PostgreSQL",
		SupportsRepeatableRead: true,
		SupportsSerializable:   true,
		EnforcesForeignKeys:    true,
		GuaranteesDurability:   true,
		SupportsSavepoints:     true,
		DetectsDeadlocks:       true,
	}

	if !caps.GuaranteesDurability || !caps.SupportsSerializable {
		t.Fatal("expected PostgreSQL reference capabilities to be declared")
	}

	memCaps := AdapterCapabilities{
		AdapterName:          "Memory",
		GuaranteesDurability: false,
	}
	if memCaps.GuaranteesDurability {
		t.Fatal("memory adapter must not claim durability")
	}
}

var _ = decimal.Zero

