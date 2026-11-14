package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
)

type p404TestContext struct {
	ctx       context.Context
	env       isolatedPostingEnv
	service   *masterdata.Service
	engine    *posting.Engine
	marchID   uuid.UUID
	aprilID   uuid.UUID
	original  posting.JournalEntry
	actorID   string
	reason    string
	reference string
	idemKey   string
}

func newP404TestContext(t *testing.T, poolSize int32) *p404TestContext {
	t.Helper()
	ctx, env := setupIsolatedPostingEnvWithPool(t, poolSize)
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, service)

	march, err := service.GetPeriodForCompanyDateContext(ctx, env.CompanyID,
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	april, err := service.GetPeriodForCompanyDateContext(ctx, env.CompanyID,
		time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	return &p404TestContext{
		ctx:       ctx,
		env:       env,
		service:   service,
		engine:    engine,
		marchID:   march.ID,
		aprilID:   april.ID,
		actorID:   uuid.NewString(),
		reason:    "P4-04 audited reversal test",
		reference: "REF-P4-04-AUDIT",
		idemKey:   "p4-04-idem-key",
	}
}

func (tc *p404TestContext) postMarchOriginal(t *testing.T) {
	t.Helper()
	entry := createDraftTestEntry(t, tc.ctx, tc.engine, tc.env.CompanyID, tc.env.JournalID,
		tc.env.DebitAccID, tc.env.CreditAccID, reversalTestMoney(t, "100.00"),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err := tc.engine.PostEntryContext(tc.ctx, &entry, "poster"); err != nil {
		t.Fatal(err)
	}
	tc.original = entry
}

func (tc *p404TestContext) hardCloseMarch(t *testing.T) {
	t.Helper()
	actor := uuid.New()
	if err := tc.service.TransitionPeriodStateContext(tc.ctx, tc.env.CompanyID, tc.marchID,
		masterdata.PeriodSoftClosed, actor, "soft close for month end"); err != nil {
		t.Fatal(err)
	}
	if err := tc.service.TransitionPeriodStateContext(tc.ctx, tc.env.CompanyID, tc.marchID,
		masterdata.PeriodHardClosed, actor, "hard close for audit"); err != nil {
		t.Fatal(err)
	}
}

func (tc *p404TestContext) reversalCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := tc.env.Driver.Pool().QueryRow(tc.ctx,
		`SELECT count(*) FROM account_moves
		 WHERE company_id=$1 AND classification='reversing'
		   AND entry->>'OriginalEntryID'=$2`,
		tc.env.CompanyID, tc.original.ID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestP404ReversalOnPostgres tests the complete reversal flow on PostgreSQL:
// posting an entry, reversing it, and verifying that the net functional balance
// across account_move_lines is exactly 0 minor units per account and currency.
func TestP404ReversalOnPostgres(t *testing.T) {
	tc := newP404TestContext(t, 10)
	tc.postMarchOriginal(t)

	reversal, err := tc.engine.ReverseWithRequestContext(tc.ctx, tc.env.CompanyID, tc.original.ID,
		posting.ReversalRequest{
			ActorID:        tc.actorID,
			Reason:         tc.reason,
			EffectiveDate:  time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC),
			Reference:      tc.reference,
			SourceSystem:   "billing",
			IdempotencyKey: tc.idemKey,
		}, tc.actorID)
	if err != nil {
		t.Fatalf("ReverseWithRequestContext failed on PostgreSQL: %v", err)
	}

	if reversal.Status != posting.EntryPosted || reversal.Classification != posting.EntryClassReversing {
		t.Fatalf("Reversal status/classification = %s/%s, want posted/reversing",
			reversal.Status, reversal.Classification)
	}

	// Verify Net Zero across account_move_lines in PostgreSQL
	rows, err := tc.env.Driver.Pool().Query(tc.ctx,
		`SELECT account_id,
		        COALESCE(SUM(functional_debit_minor), 0) - COALESCE(SUM(functional_credit_minor), 0) AS net_functional,
		        COALESCE(SUM(transaction_amount_minor), 0) AS net_transaction
		 FROM account_move_lines
		 WHERE company_id = $1
		 GROUP BY account_id`, tc.env.CompanyID)
	if err != nil {
		t.Fatalf("Query account_move_lines failed: %v", err)
	}
	defer rows.Close()

	accountsChecked := 0
	for rows.Next() {
		var accountID uuid.UUID
		var netFunctional, netTransaction int64
		if err := rows.Scan(&accountID, &netFunctional, &netTransaction); err != nil {
			t.Fatal(err)
		}
		accountsChecked++
		if netFunctional != 0 {
			t.Fatalf("Account %s net functional minor = %d, expected 0", accountID, netFunctional)
		}
		if netTransaction != 0 {
			t.Fatalf("Account %s net transaction minor = %d, expected 0", accountID, netTransaction)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if accountsChecked != 2 {
		t.Fatalf("Checked %d accounts, expected 2", accountsChecked)
	}

	// Verify original entry state in DB
	storedOrig, err := tc.env.Driver.GetEntryByID(tc.ctx, tc.env.CompanyID, tc.original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOrig.Status != posting.EntryReversed || storedOrig.ReversedEntryID == nil || *storedOrig.ReversedEntryID != reversal.ID {
		t.Fatalf("Original entry state on DB incorrect: %#v", storedOrig)
	}
}

// TestP404ReversalInLaterOpenPeriod tests that when the original entry's period is hard-closed,
// reversal inside that period is rejected, but reversal with a date in a later open period succeeds.
func TestP404ReversalInLaterOpenPeriod(t *testing.T) {
	tc := newP404TestContext(t, 10)
	tc.postMarchOriginal(t)
	tc.hardCloseMarch(t)

	// 1. Attempt reversal inside the hard-closed March period -> must fail
	_, err := tc.engine.ReverseWithRequestContext(tc.ctx, tc.env.CompanyID, tc.original.ID,
		posting.ReversalRequest{
			ActorID:        tc.actorID,
			Reason:         "attempt reversal in closed March",
			EffectiveDate:  time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
			Reference:      tc.reference,
			SourceSystem:   "billing",
			IdempotencyKey: "closed-period-attempt",
		}, tc.actorID)
	if err == nil {
		t.Fatal("Expected reversal inside hard-closed period to fail, but it succeeded")
	}
	if !errors.Is(err, masterdata.ErrFiscalPeriodClosed) {
		t.Fatalf("Reversal in closed period returned error = %v, want ErrFiscalPeriodClosed", err)
	}

	if count := tc.reversalCount(t); count != 0 {
		t.Fatalf("Rejected reversal persisted %d rows in DB", count)
	}

	storedOrig, err := tc.env.Driver.GetEntryByID(tc.ctx, tc.env.CompanyID, tc.original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOrig.Status != posting.EntryPosted || storedOrig.ReversedEntryID != nil {
		t.Fatalf("Rejected reversal mutated original entry: %#v", storedOrig)
	}

	// 2. Perform reversal in April (open period) -> must succeed
	reversal, err := tc.engine.ReverseWithRequestContext(tc.ctx, tc.env.CompanyID, tc.original.ID,
		posting.ReversalRequest{
			ActorID:        tc.actorID,
			Reason:         "reversal in open April period",
			EffectiveDate:  time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
			Reference:      tc.reference,
			SourceSystem:   "billing",
			IdempotencyKey: tc.idemKey,
		}, tc.actorID)
	if err != nil {
		t.Fatalf("Reversal in open April period failed: %v", err)
	}

	if reversal.PeriodID != tc.aprilID || reversal.PeriodID == tc.original.PeriodID {
		t.Fatalf("Reversal PeriodID = %s, want April PeriodID %s (original was %s)",
			reversal.PeriodID, tc.aprilID, tc.original.PeriodID)
	}
	if count := tc.reversalCount(t); count != 1 {
		t.Fatalf("Reversal rows in DB = %d, want exactly 1", count)
	}
}

// TestP404ConcurrentReversalHighContention tests that under extreme concurrency
// (16 goroutines attempting to reverse the exact same entry with distinct idempotency keys),
// exactly ONE reversal succeeds and the remaining 15 fail with ErrEntryAlreadyReversed.
func TestP404ConcurrentReversalHighContention(t *testing.T) {
	const concurrency = 16
	tc := newP404TestContext(t, concurrency+10)
	tc.postMarchOriginal(t)

	var wg sync.WaitGroup
	start := make(chan struct{})
	var successCount int64
	var alreadyReversedCount int64
	errs := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start

			req := posting.ReversalRequest{
				ActorID:        fmt.Sprintf("actor-concurrent-%d", idx),
				Reason:         fmt.Sprintf("concurrent reversal attempt %d", idx),
				EffectiveDate:  time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC),
				Reference:      fmt.Sprintf("REF-%d", idx),
				IdempotencyKey: fmt.Sprintf("idem-key-%d", idx),
			}

			eng := posting.NewEngine(tc.env.Driver, tc.service)
			_, err := eng.ReverseWithRequestContext(tc.ctx, tc.env.CompanyID, tc.original.ID, req, req.ActorID)
			errs[idx] = err
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else if errors.Is(err, posting.ErrEntryAlreadyReversed) {
				atomic.AddInt64(&alreadyReversedCount, 1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("Concurrent reversals: successCount = %d, want exactly 1", successCount)
	}
	if alreadyReversedCount != int64(concurrency-1) {
		t.Fatalf("Concurrent reversals: alreadyReversedCount = %d, want %d",
			alreadyReversedCount, concurrency-1)
	}

	// Verify only 1 reversal row exists in the database
	count := tc.reversalCount(t)
	if count != 1 {
		t.Fatalf("Reversal rows in database = %d, want exactly 1", count)
	}
}

// TestP404ConcurrentIdempotencyReplay tests that when 16 goroutines concurrently
// attempt to reverse the same entry using the SAME IdempotencyKey and identical payload,
// ALL goroutines succeed, return the exact same reversal ID, and only 1 reversal row is created.
func TestP404ConcurrentIdempotencyReplay(t *testing.T) {
	const concurrency = 16
	tc := newP404TestContext(t, concurrency+10)
	tc.postMarchOriginal(t)

	var wg sync.WaitGroup
	start := make(chan struct{})
	sharedIdemKey := "p404-shared-idem-key"

	results := make([]posting.JournalEntry, concurrency)
	replays := make([]bool, concurrency)
	errs := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start

			req := posting.ReversalRequest{
				ActorID:        "shared-actor",
				Reason:         "shared idempotency reversal",
				EffectiveDate:  time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC),
				Reference:      "SHARED-REF",
				SourceSystem:   "idem-service",
				IdempotencyKey: sharedIdemKey,
			}

			eng := posting.NewEngine(tc.env.Driver, tc.service)
			entry, replay, err := eng.ReverseWithRequestResultContext(tc.ctx, tc.env.CompanyID, tc.original.ID, req, req.ActorID)
			results[idx] = entry
			replays[idx] = replay
			errs[idx] = err
		}(i)
	}

	close(start)
	wg.Wait()

	// Check all goroutines succeeded
	var expectedID uuid.UUID
	replayCount := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Goroutine %d returned error on idempotent replay: %v", i, err)
		}
		if i == 0 {
			expectedID = results[i].ID
		} else if results[i].ID != expectedID {
			t.Fatalf("Goroutine %d returned ID %s, expected identical ID %s", i, results[i].ID, expectedID)
		}
		if replays[i] {
			replayCount++
		}
	}

	// Exactly one reversal row created in DB
	count := tc.reversalCount(t)
	if count != 1 {
		t.Fatalf("Database contains %d reversal rows, want exactly 1", count)
	}

	t.Logf("Concurrent idempotency replay succeeded: 16 goroutines, %d replays, reversal ID %s",
		replayCount, expectedID)
}

// TestP404PersistenceAndRetrieval tests that the reversed entry, reversal entry,
// and tamper-evident audit logs survive engine/pool restart and retain all metadata.
func TestP404PersistenceAndRetrieval(t *testing.T) {
	tc := newP404TestContext(t, 10)
	tc.postMarchOriginal(t)

	req := posting.ReversalRequest{
		ActorID:        tc.actorID,
		Reason:         "Full persistence and retrieval verification",
		EffectiveDate:  time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC),
		Reference:      tc.reference,
		SourceSystem:   "audited-service",
		IdempotencyKey: "p404-persist-key-001",
	}

	reversal, err := tc.engine.ReverseWithRequestContext(tc.ctx, tc.env.CompanyID, tc.original.ID, req, req.ActorID)
	if err != nil {
		t.Fatalf("Reversal failed: %v", err)
	}

	// Simulate service restart by connecting with a fresh pool and engine
	var currentSchema string
	if err := tc.env.Driver.Pool().QueryRow(tc.ctx, "SELECT current_schema()").Scan(&currentSchema); err != nil {
		t.Fatal(err)
	}

	config, err := pgxpool.ParseConfig(getTestPostgresConn())
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["search_path"] = currentSchema + ",public"

	freshPool, err := pgxpool.NewWithConfig(tc.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer freshPool.Close()

	freshDriver := postgres.NewWithPool(freshPool)
	freshEngine := posting.NewEngine(freshDriver, nil)

	// 1. Retrieve reversal entry
	storedReversal, err := freshEngine.GetEntry(tc.ctx, tc.env.CompanyID, reversal.ID)
	if err != nil {
		t.Fatalf("Retrieve reversal entry after restart failed: %v", err)
	}
	if storedReversal.ReversalReason != req.Reason {
		t.Errorf("ReversalReason = %q, want %q", storedReversal.ReversalReason, req.Reason)
	}
	if storedReversal.ReversalReference != req.Reference {
		t.Errorf("ReversalReference = %q, want %q", storedReversal.ReversalReference, req.Reference)
	}
	if storedReversal.SourceModule != req.SourceSystem {
		t.Errorf("SourceModule = %q, want %q", storedReversal.SourceModule, req.SourceSystem)
	}
	if storedReversal.CreatedBy != req.ActorID || storedReversal.PostedBy != req.ActorID {
		t.Errorf("Actor metadata = (%s, %s), want %s", storedReversal.CreatedBy, storedReversal.PostedBy, req.ActorID)
	}
	if storedReversal.OriginalEntryID == nil || *storedReversal.OriginalEntryID != tc.original.ID {
		t.Errorf("OriginalEntryID = %v, want %s", storedReversal.OriginalEntryID, tc.original.ID)
	}
	if storedReversal.IdempotencyKey != req.IdempotencyKey || storedReversal.IdempotencyPayloadHash == "" {
		t.Errorf("Idempotency evidence missing: key=%q hash=%q",
			storedReversal.IdempotencyKey, storedReversal.IdempotencyPayloadHash)
	}

	// 2. Retrieve original entry
	storedOriginal, err := freshEngine.GetEntry(tc.ctx, tc.env.CompanyID, tc.original.ID)
	if err != nil {
		t.Fatalf("Retrieve original entry after restart failed: %v", err)
	}
	if storedOriginal.Status != posting.EntryReversed {
		t.Errorf("Original status = %v, want reversed", storedOriginal.Status)
	}
	if storedOriginal.ReversedEntryID == nil || *storedOriginal.ReversedEntryID != reversal.ID {
		t.Errorf("Original ReversedEntryID = %v, want %s", storedOriginal.ReversedEntryID, reversal.ID)
	}

	// 3. Verify financial audit logs
	logs := auditLogsForCompany(t, tc.ctx, freshDriver, tc.env.CompanyID)
	verifyAuditChain(t, logs)

	if got := auditLogsWithAction(logs, "REVERSE", reversal.ID); got != 1 {
		t.Fatalf("Audit logs for reversal with action REVERSE = %d, want 1", got)
	}
	if got := auditLogsWithAction(logs, "POST", reversal.ID); got != 0 {
		t.Fatalf("Audit logs for reversal with action POST = %d, want 0", got)
	}
	if got := auditLogsWithAction(logs, "REVERSE", tc.original.ID); got != 1 {
		t.Fatalf("Audit logs for original with action REVERSE = %d, want 1", got)
	}

	var foundAudit bool
	parsedActor, err := uuid.Parse(req.ActorID)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		if log.Action == "REVERSE" && log.EntityID == reversal.ID {
			foundAudit = true
			if log.ActorID != parsedActor {
				t.Errorf("Audit log ActorID = %s, want %s", log.ActorID, parsedActor)
			}
			var payload map[string]any
			if err := json.Unmarshal(log.PayloadAfter, &payload); err != nil {
				t.Fatalf("Failed to parse audit payload: %v", err)
			}
			if payload["ReversalReason"] != req.Reason {
				t.Errorf("Audit payload ReversalReason = %v, want %q", payload["ReversalReason"], req.Reason)
			}
			if payload["reversal_reference"] != req.Reference {
				t.Errorf("Audit payload reversal_reference = %v, want %q", payload["reversal_reference"], req.Reference)
			}
			if payload["idempotency_key"] != req.IdempotencyKey {
				t.Errorf("Audit payload idempotency_key = %v, want %q", payload["idempotency_key"], req.IdempotencyKey)
			}
		}
	}
	if !foundAudit {
		t.Fatal("Audit log row for reversal entry not found")
	}
}

