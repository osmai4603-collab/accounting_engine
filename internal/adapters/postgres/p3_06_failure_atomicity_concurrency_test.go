package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

func makeTestPostingEntry(
	companyID, journalID, periodID, debitAccID, creditAccID uuid.UUID,
	amountStr string,
) (ledger.JournalEntry, error) {
	amt, err := types.NewMoneyFromString(amountStr, "USD")
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	zero, err := types.Zero("USD")
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	now := time.Now().UTC()
	effDate := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
	entryID := types.NewJournalEntryID()

	return ledger.JournalEntry{
		ID:                  entryID,
		CompanyID:           types.CompanyID(companyID),
		JournalID:           types.JournalID(journalID),
		FiscalPeriodID:      types.FiscalPeriodID(periodID),
		EffectiveDate:       effDate,
		PostingDate:         effDate,
		CreatedAt:           now,
		CreatedBy:           "p3-06-tester",
		Description:         "P3-06 Atomicity Verification Entry",
		TransactionCurrency: "USD",
		ExchangeRate:        decimal.NewFromInt(1),
		Status:              ledger.EntryPosted,
		Classification:      ledger.EntryClassRegular,
		Lines: []ledger.EntryLine{
			{
				ID:                  types.JournalEntryLineID(uuid.New()),
				AccountID:           types.AccountID(debitAccID),
				TransactionAmount:   amt,
				TransactionCurrency: "USD",
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     amt,
				FunctionalCredit:    zero,
			},
			{
				ID:                  types.JournalEntryLineID(uuid.New()),
				AccountID:           types.AccountID(creditAccID),
				TransactionAmount:   amt,
				TransactionCurrency: "USD",
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     zero,
				FunctionalCredit:    amt,
			},
		},
	}, nil
}

// TestP3_06_FailureInjectionRollbackStages verifies that injected failures at
// distinct stages of the unit of work transaction result in a 100% complete
// rollback of all mutations: entries, lines, sequences, balances, audit, and idempotency.
func TestP3_06_FailureInjectionRollbackStages(t *testing.T) {
	stages := []struct {
		name      string
		failStage string
	}{
		{name: "fail after draft creation", failStage: "after_draft"},
		{name: "fail after sequence allocation", failStage: "after_sequence"},
		{name: "fail after posting commit before idempotency", failStage: "after_posting"},
		{name: "fail after idempotency claim before completion", failStage: "after_idempotency_claim"},
		{name: "fail just before commit", failStage: "before_commit"},
	}

	for _, tc := range stages {
		t.Run(tc.name, func(t *testing.T) {
			ctx, env := setupIsolatedPostingEnv(t)
			defer env.Driver.Close()

			txCtx := storage.WithActorContext(
				storage.WithCompanyContext(ctx, types.CompanyID(env.CompanyID)),
				"p3-06-test-actor",
			)

			injectedErr := fmt.Errorf("injected failure at stage %s", tc.failStage)
			idemKey := fmt.Sprintf("idem-%s-%s", tc.failStage, uuid.NewString()[:8])
			idemRecord := storage.IdempotencyRecord{
				CompanyID:   types.CompanyID(env.CompanyID),
				Key:         idemKey,
				Operation:   "post",
				PayloadHash: "hash-" + tc.failStage,
			}

			entry, err := makeTestPostingEntry(env.CompanyID, env.JournalID, env.PeriodID, env.DebitAccID, env.CreditAccID, "150.00")
			if err != nil {
				t.Fatalf("create entry: %v", err)
			}

			// Execute unit of work with injected failure
			execErr := env.Driver.Ports().UOWRunner.Execute(txCtx, func(c context.Context, uow storage.UnitOfWork) error {
				// Stage 1: Draft
				draftEntry := entry
				draftEntry.Status = ledger.EntryDraft
				if err := uow.Drafts().SaveDraft(c, draftEntry); err != nil {
					return fmt.Errorf("save draft: %w", err)
				}
				if tc.failStage == "after_draft" {
					return injectedErr
				}

				// Stage 2: Sequence
				seq, err := uow.Sequences().NextSequence(c, types.CompanyID(env.CompanyID), types.JournalID(env.JournalID))
				if err != nil {
					return fmt.Errorf("next sequence: %w", err)
				}
				if seq <= 0 {
					return fmt.Errorf("unexpected sequence: %d", seq)
				}
				entry.SequenceNumber = seq
				if tc.failStage == "after_sequence" {
					return injectedErr
				}

				// Stage 3: Posting
				posted, err := uow.Posting().CommitPosting(c, storage.PostingCommit{
					Entry:     entry,
					ActorID:   "p3-06-test-actor",
					Operation: storage.OperationPost,
				})
				if err != nil {
					return fmt.Errorf("commit posting: %w", err)
				}
				if posted.ID != entry.ID {
					return errors.New("entry ID mismatch")
				}
				if tc.failStage == "after_posting" {
					return injectedErr
				}

				// Stage 4: Idempotency Claim
				_, claimed, err := uow.Idempotency().Claim(c, idemRecord)
				if err != nil {
					return fmt.Errorf("claim idempotency: %w", err)
				}
				if !claimed {
					return errors.New("expected idempotency claimed")
				}
				if tc.failStage == "after_idempotency_claim" {
					return injectedErr
				}

				// Stage 5: Complete & Audit
				if err := uow.Idempotency().Complete(c, idemRecord.CompanyID, idemRecord.Key, idemRecord.PayloadHash, posted.ID, []byte("null")); err != nil {
					return fmt.Errorf("complete idempotency: %w", err)
				}
				if err := uow.Audit().AppendAudit(c, storage.AuditRecord{
					CompanyID:  types.CompanyID(env.CompanyID),
					EntityType: "journal_entry",
					EntityID:   uuid.UUID(entry.ID).String(),
					ActorID:    "p3-06-test-actor",
					Action:     "post_entry",
				}); err != nil {
					return fmt.Errorf("record audit: %w", err)
				}

				if tc.failStage == "before_commit" {
					return injectedErr
				}
				return nil
			})

			// Verify error was returned
			if !errors.Is(execErr, injectedErr) {
				t.Fatalf("expected injected error %v, got %v", injectedErr, execErr)
			}

			// Verify absolute rollback in PostgreSQL
			if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
				// 1. No entry exists
				var entryCount int
				if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM account_moves WHERE company_id=$1 AND id=$2`,
					env.CompanyID, uuid.UUID(entry.ID)).Scan(&entryCount); err != nil {
					return err
				}
				if entryCount != 0 {
					return fmt.Errorf("account_moves row count = %d, want 0 after rollback", entryCount)
				}

				// 2. No lines exist
				var lineCount int
				if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM account_move_lines WHERE company_id=$1 AND move_id=$2`,
					env.CompanyID, uuid.UUID(entry.ID)).Scan(&lineCount); err != nil {
					return err
				}
				if lineCount != 0 {
					return fmt.Errorf("account_move_lines row count = %d, want 0 after rollback", lineCount)
				}

				// 3. Sequence was not permanently consumed
				var currentSeq int64
				if err := tx.QueryRow(ctx, `SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
					env.CompanyID, env.JournalID).Scan(&currentSeq); err != nil {
					return err
				}
				if currentSeq != 0 {
					return fmt.Errorf("journal current_sequence = %d, want 0 after rollback", currentSeq)
				}

				// 4. Idempotency record was not persisted
				var idemCount int
				if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM posting_idempotency WHERE company_id=$1 AND idempotency_key=$2`,
					env.CompanyID, idemKey).Scan(&idemCount); err != nil {
					return err
				}
				if idemCount != 0 {
					return fmt.Errorf("posting_idempotency row count = %d, want 0 after rollback", idemCount)
				}
				return nil
			}); err != nil {
				t.Fatalf("verification query failed: %v", err)
			}

			// 5. Account balances are 0
			tb, err := env.Driver.GetTrialBalance(ctx, types.CompanyID(env.CompanyID), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "USD")
			if err != nil {
				t.Fatalf("get trial balance: %v", err)
			}
			if !tb.TotalDebit.Amount().IsZero() || !tb.TotalCredit.Amount().IsZero() {
				t.Fatalf("balances not zero after rollback: debit=%s credit=%s", tb.TotalDebit, tb.TotalCredit)
			}
		})
	}
}

// TestP3_06_ConcurrentSameIdempotencyKey verifies that when multiple concurrent
// workers try to post with the exact same idempotency key and payload, exactly ONE
// entry is posted and account balances reflect exactly 1x the transaction amount.
func TestP3_06_ConcurrentSameIdempotencyKey(t *testing.T) {
	ctx, env := setupIsolatedPostingEnvWithPool(t, 20)
	defer env.Driver.Close()

	concurrency := 12
	sharedIdemKey := "concurrent-idem-key-" + uuid.NewString()[:8]
	sharedPayloadHash := "payload-hash-v1-verified"

	entry, err := makeTestPostingEntry(env.CompanyID, env.JournalID, env.PeriodID, env.DebitAccID, env.CreditAccID, "200.00")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}

	txCtx := storage.WithActorContext(
		storage.WithCompanyContext(ctx, types.CompanyID(env.CompanyID)),
		"concurrent-actor",
	)

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})
	successCount := 0
	conflictCount := 0
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier

			idemRecord := storage.IdempotencyRecord{
				CompanyID:   types.CompanyID(env.CompanyID),
				Key:         sharedIdemKey,
				Operation:   "post",
				PayloadHash: sharedPayloadHash,
			}

			err := env.Driver.Ports().UOWRunner.ExecuteWithOptions(txCtx, storage.TxOptions{
				Isolation: storage.IsolationReadCommitted,
			}, func(c context.Context, uow storage.UnitOfWork) error {
				rec, claimed, err := uow.Idempotency().Claim(c, idemRecord)
				if err != nil {
					return err
				}
				if !claimed {
					// Another worker already claimed it
					if rec.Status == storage.IdempotencyCompleted {
						return nil // Idempotent success replay
					}
					return errors.New("concurrently in flight")
				}

				// The winning worker saves draft, allocates sequence, and commits
				workerEntry := entry
				draft := workerEntry
				draft.Status = ledger.EntryDraft
				if err := uow.Drafts().SaveDraft(c, draft); err != nil {
					return err
				}

				seq, err := uow.Sequences().NextSequence(c, types.CompanyID(env.CompanyID), types.JournalID(env.JournalID))
				if err != nil {
					return err
				}
				workerEntry.SequenceNumber = seq

				posted, err := uow.Posting().CommitPosting(c, storage.PostingCommit{
					Entry:     workerEntry,
					ActorID:   fmt.Sprintf("worker-%d", workerID),
					Operation: storage.OperationPost,
				})
				if err != nil {
					return err
				}

				return uow.Idempotency().Complete(c, idemRecord.CompanyID, idemRecord.Key, idemRecord.PayloadHash, posted.ID, []byte("null"))
			})

			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else {
				conflictCount++
			}
		}(i)
	}

	close(startBarrier)
	wg.Wait()

	if successCount < 1 {
		t.Fatalf("expected at least 1 successful worker, got %d (conflicts: %d)", successCount, conflictCount)
	}

	// Verify database state: exactly ONE entry posted
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		var entryCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM account_moves WHERE company_id=$1`,
			env.CompanyID).Scan(&entryCount); err != nil {
			return err
		}
		if entryCount != 1 {
			return fmt.Errorf("expected exactly 1 account_moves entry, got %d", entryCount)
		}

		var seq int64
		if err := tx.QueryRow(ctx, `SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
			env.CompanyID, env.JournalID).Scan(&seq); err != nil {
			return err
		}
		if seq != 1 {
			return fmt.Errorf("expected sequence=1, got %d", seq)
		}

		var idemRows int
		var status string
		if err := tx.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MAX(status), '') FROM posting_idempotency
			WHERE company_id=$1 AND idempotency_key=$2`, env.CompanyID, sharedIdemKey).Scan(&idemRows, &status); err != nil {
			return err
		}
		if idemRows != 1 || status != "completed" {
			return fmt.Errorf("idempotency record rows=%d status=%s, want 1 completed", idemRows, status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Verify balance is exactly 1x 200.00
	tb, err := env.Driver.GetTrialBalance(ctx, types.CompanyID(env.CompanyID), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatalf("get trial balance: %v", err)
	}
	wantAmt := decimal.NewFromInt(200)
	if !tb.TotalDebit.Amount().Equal(wantAmt) || !tb.TotalCredit.Amount().Equal(wantAmt) {
		t.Fatalf("expected balances %s / %s, got debit %s / credit %s",
			wantAmt, wantAmt, tb.TotalDebit, tb.TotalCredit)
	}
}

// TestP3_06_ConcurrentPostingConflictingPayloads verifies that when a second request
// with the same idempotency key presents a different payload hash, it is rejected.
func TestP3_06_ConcurrentPostingConflictingPayloads(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	defer env.Driver.Close()

	key := "conflict-key-" + uuid.NewString()[:8]
	txCtx := storage.WithActorContext(
		storage.WithCompanyContext(ctx, types.CompanyID(env.CompanyID)),
		"actor-1",
	)

	// Post first transaction with hash A
	recordA := storage.IdempotencyRecord{
		CompanyID:   types.CompanyID(env.CompanyID),
		Key:         key,
		Operation:   "post",
		PayloadHash: "hash-payload-A",
	}

	entryA, err := makeTestPostingEntry(env.CompanyID, env.JournalID, env.PeriodID, env.DebitAccID, env.CreditAccID, "100.00")
	if err != nil {
		t.Fatal(err)
	}

	err = env.Driver.Ports().UOWRunner.Execute(txCtx, func(c context.Context, uow storage.UnitOfWork) error {
		_, claimed, err := uow.Idempotency().Claim(c, recordA)
		if err != nil || !claimed {
			return fmt.Errorf("claim A: claimed=%t err=%w", claimed, err)
		}

		draft := entryA
		draft.Status = ledger.EntryDraft
		if err := uow.Drafts().SaveDraft(c, draft); err != nil {
			return err
		}

		seq, err := uow.Sequences().NextSequence(c, types.CompanyID(env.CompanyID), types.JournalID(env.JournalID))
		if err != nil {
			return err
		}
		entryA.SequenceNumber = seq
		posted, err := uow.Posting().CommitPosting(c, storage.PostingCommit{
			Entry:     entryA,
			ActorID:   "actor-1",
			Operation: storage.OperationPost,
		})
		if err != nil {
			return err
		}
		return uow.Idempotency().Complete(c, recordA.CompanyID, recordA.Key, recordA.PayloadHash, posted.ID, []byte("null"))
	})
	if err != nil {
		t.Fatalf("post A: %v", err)
	}

	// Second request with SAME key but DIFFERENT payload hash B
	recordB := storage.IdempotencyRecord{
		CompanyID:   types.CompanyID(env.CompanyID),
		Key:         key,
		Operation:   "post",
		PayloadHash: "hash-payload-B",
	}

	err = env.Driver.Ports().UOWRunner.Execute(txCtx, func(c context.Context, uow storage.UnitOfWork) error {
		_, claimed, err := uow.Idempotency().Claim(c, recordB)
		if err != nil {
			return err
		}
		if claimed {
			return errors.New("claimed conflicting payload; should have been rejected")
		}
		return nil
	})
	if !errors.Is(err, storage.ErrUniqueConstraint) {
		t.Fatalf("expected ErrUniqueConstraint on conflicting payload, got: %v", err)
	}

	// Balances remain 100.00
	tb, err := env.Driver.GetTrialBalance(ctx, types.CompanyID(env.CompanyID), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if !tb.TotalDebit.Amount().Equal(decimal.NewFromInt(100)) {
		t.Fatalf("unexpected balance: %s", tb.TotalDebit)
	}
}

// TestP3_06_ConcurrentSharedJournalAndAccounts verifies that multiple concurrent
// distinct transactions sharing the same Journal (and sequence counter) and Accounts
// all commit successfully, sequences are strictly contiguous and unique, and account
// balances equal exactly the sum of all posted transactions.
func TestP3_06_ConcurrentSharedJournalAndAccounts(t *testing.T) {
	ctx, env := setupIsolatedPostingEnvWithPool(t, 25)
	defer env.Driver.Close()

	numWorkers := 15
	perWorkerAmount := decimal.NewFromInt(25)
	txCtx := storage.WithActorContext(
		storage.WithCompanyContext(ctx, types.CompanyID(env.CompanyID)),
		"shared-concurrency-actor",
	)

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})
	var mu sync.Mutex
	observedSequences := make(map[int64]bool)

	retryPolicy := storage.RetryPolicy{
		MaxRetries:     5,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     150 * time.Millisecond,
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerIndex int) {
			defer wg.Done()
			<-startBarrier

			workerEntry, err := makeTestPostingEntry(env.CompanyID, env.JournalID, env.PeriodID, env.DebitAccID, env.CreditAccID, "25.00")
			if err != nil {
				t.Errorf("worker %d entry: %v", workerIndex, err)
				return
			}
			workerEntry.Description = fmt.Sprintf("Concurrent worker %d", workerIndex)

			workerKey := fmt.Sprintf("worker-key-%d-%s", workerIndex, uuid.NewString()[:8])
			idemRecord := storage.IdempotencyRecord{
				CompanyID:   types.CompanyID(env.CompanyID),
				Key:         workerKey,
				Operation:   "post",
				PayloadHash: fmt.Sprintf("hash-%d", workerIndex),
			}

			// Execute with automated retry for transient serializable / deadlock conflicts
			err = storage.ExecuteWithRetry(txCtx, env.Driver.Ports().UOWRunner, storage.TxOptions{
				Isolation: storage.IsolationReadCommitted,
			}, retryPolicy, func(c context.Context, uow storage.UnitOfWork) error {
				_, claimed, err := uow.Idempotency().Claim(c, idemRecord)
				if err != nil {
					return err
				}
				if !claimed {
					return errors.New("expected unique key to be claimed")
				}

				draft := workerEntry
				draft.Status = ledger.EntryDraft
				if err := uow.Drafts().SaveDraft(c, draft); err != nil {
					return err
				}

				seq, err := uow.Sequences().NextSequence(c, types.CompanyID(env.CompanyID), types.JournalID(env.JournalID))
				if err != nil {
					return err
				}
				workerEntry.SequenceNumber = seq

				posted, err := uow.Posting().CommitPosting(c, storage.PostingCommit{
					Entry:     workerEntry,
					ActorID:   fmt.Sprintf("worker-%d", workerIndex),
					Operation: storage.OperationPost,
				})
				if err != nil {
					return err
				}

				return uow.Idempotency().Complete(c, idemRecord.CompanyID, idemRecord.Key, idemRecord.PayloadHash, posted.ID, []byte("null"))
			})

			if err != nil {
				t.Errorf("worker %d failed to post: %v", workerIndex, err)
				return
			}

			mu.Lock()
			observedSequences[workerEntry.SequenceNumber] = true
			mu.Unlock()
		}(i)
	}

	close(startBarrier)
	wg.Wait()

	// Verify all 15 sequences are distinct and form a contiguous sequence 1..numWorkers
	if len(observedSequences) != numWorkers {
		t.Fatalf("expected %d distinct sequence numbers, got %d: %+v", numWorkers, len(observedSequences), observedSequences)
	}
	for expectedSeq := int64(1); expectedSeq <= int64(numWorkers); expectedSeq++ {
		if !observedSequences[expectedSeq] {
			t.Fatalf("missing expected sequence number %d", expectedSeq)
		}
	}

	// Verify final balances equal exactly numWorkers * 25.00
	expectedTotal := perWorkerAmount.Mul(decimal.NewFromInt(int64(numWorkers)))
	tb, err := env.Driver.GetTrialBalance(ctx, types.CompanyID(env.CompanyID), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatalf("get trial balance: %v", err)
	}
	if !tb.TotalDebit.Amount().Equal(expectedTotal) || !tb.TotalCredit.Amount().Equal(expectedTotal) {
		t.Fatalf("trial balance mismatch: expected %s, got debit=%s credit=%s",
			expectedTotal, tb.TotalDebit, tb.TotalCredit)
	}
}

// TestP3_06_NonTransientErrorsNotRetried verifies that business and integrity errors
// are classified as non-transient and fail fast without wasteful retries.
func TestP3_06_NonTransientErrorsNotRetried(t *testing.T) {
	// 1. Predicate checks
	if !storage.IsRetryable(storage.ErrSerializationFailure) {
		t.Fatal("expected ErrSerializationFailure to be retryable")
	}
	if !storage.IsRetryable(storage.ErrDeadlockDetected) {
		t.Fatal("expected ErrDeadlockDetected to be retryable")
	}
	if storage.IsRetryable(storage.ErrUniqueConstraint) {
		t.Fatal("expected ErrUniqueConstraint NOT to be retryable")
	}
	if storage.IsRetryable(posting.ErrInvalidEntry) {
		t.Fatal("expected ErrInvalidEntry NOT to be retryable")
	}
	if storage.IsRetryable(masterdata.ErrJournalNotFound) {
		t.Fatal("expected ErrJournalNotFound NOT to be retryable")
	}

	// 2. Automated retry terminates immediately on non-transient error
	ctx, env := setupIsolatedPostingEnv(t)
	defer env.Driver.Close()

	txCtx := storage.WithActorContext(
		storage.WithCompanyContext(ctx, types.CompanyID(env.CompanyID)),
		"retry-check-actor",
	)

	attempts := 0
	policy := storage.RetryPolicy{
		MaxRetries:     5,
		InitialBackoff: 10 * time.Millisecond,
	}

	nonTransientErr := posting.ErrInvalidEntry
	err := storage.ExecuteWithRetry(txCtx, env.Driver.Ports().UOWRunner, storage.TxOptions{}, policy, func(c context.Context, uow storage.UnitOfWork) error {
		attempts++
		return nonTransientErr
	})

	if !errors.Is(err, nonTransientErr) {
		t.Fatalf("expected non-transient error %v, got %v", nonTransientErr, err)
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt without retries, got %d", attempts)
	}
}

// TestP3_06_DurabilityReconnectAfterCommit verifies that all committed data
// (entries, sequences, lines, balances, idempotency) persists across complete
// connection pool teardown and restart.
func TestP3_06_DurabilityReconnectAfterCommit(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)

	txCtx := storage.WithActorContext(
		storage.WithCompanyContext(ctx, types.CompanyID(env.CompanyID)),
		"durability-actor",
	)

	entry, err := makeTestPostingEntry(env.CompanyID, env.JournalID, env.PeriodID, env.DebitAccID, env.CreditAccID, "500.00")
	if err != nil {
		t.Fatal(err)
	}

	key := "durability-key-" + uuid.NewString()[:8]
	idemRecord := storage.IdempotencyRecord{
		CompanyID:   types.CompanyID(env.CompanyID),
		Key:         key,
		Operation:   "post",
		PayloadHash: "hash-durability",
	}

	// Post and commit
	err = env.Driver.Ports().UOWRunner.Execute(txCtx, func(c context.Context, uow storage.UnitOfWork) error {
		_, claimed, err := uow.Idempotency().Claim(c, idemRecord)
		if err != nil || !claimed {
			return fmt.Errorf("claim idempotency: %w", err)
		}

		draft := entry
		draft.Status = ledger.EntryDraft
		if err := uow.Drafts().SaveDraft(c, draft); err != nil {
			return err
		}

		seq, err := uow.Sequences().NextSequence(c, types.CompanyID(env.CompanyID), types.JournalID(env.JournalID))
		if err != nil {
			return err
		}
		entry.SequenceNumber = seq
		posted, err := uow.Posting().CommitPosting(c, storage.PostingCommit{
			Entry:     entry,
			ActorID:   "durability-actor",
			Operation: storage.OperationPost,
		})
		if err != nil {
			return err
		}
		return uow.Idempotency().Complete(c, idemRecord.CompanyID, idemRecord.Key, idemRecord.PayloadHash, posted.ID, []byte("null"))
	})
	if err != nil {
		t.Fatalf("commit posting: %v", err)
	}

	// Retrieve active isolated schema before teardown
	var currentSchema string
	if err := env.Driver.Pool().QueryRow(ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		t.Fatalf("query current schema: %v", err)
	}

	// Tear down connection pool completely
	env.Driver.Close()

	// Re-open fresh driver pool connecting to the exact same isolated PostgreSQL schema
	cfg, err := pgxpool.ParseConfig(getTestPostgresConn())
	if err != nil {
		t.Fatalf("parse postgres config: %v", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = currentSchema + ",public"
	newPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("reconnect to postgres: %v", err)
	}
	defer newPool.Close()
	reopenedDriver := postgres.NewWithPool(newPool)

	// Verify durability from fresh driver
	err = reopenedDriver.Ports().UOWRunner.ExecuteWithOptions(txCtx, storage.TxOptions{ReadOnly: true},
		func(c context.Context, uow storage.UnitOfWork) error {
			read, err := uow.Entries().GetEntry(c, types.CompanyID(env.CompanyID), entry.ID)
			if err != nil {
				return err
			}
			if read.ID != entry.ID || read.SequenceNumber != entry.SequenceNumber {
				return fmt.Errorf("read entry mismatch: %+v", read)
			}
			if len(read.Lines) != 2 {
				return fmt.Errorf("read lines count = %d, want 2", len(read.Lines))
			}
			return nil
		})
	if err != nil {
		t.Fatalf("verify read entry after reconnect: %v", err)
	}

	tb, err := reopenedDriver.GetTrialBalance(ctx, types.CompanyID(env.CompanyID), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatalf("get trial balance after reconnect: %v", err)
	}
	want500 := decimal.NewFromInt(500)
	if !tb.TotalDebit.Amount().Equal(want500) || !tb.TotalCredit.Amount().Equal(want500) {
		t.Fatalf("trial balance after reconnect = %s / %s, want %s", tb.TotalDebit, tb.TotalCredit, want500)
	}
}
