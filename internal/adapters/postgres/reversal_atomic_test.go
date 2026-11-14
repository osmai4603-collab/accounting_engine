package postgres_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func reversalTestMoney(t *testing.T, amount string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPostgresReversalPersistsMetadataAndReplaysIdempotently(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	engine := posting.NewEngine(env.Driver, nil)
	original := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID,
		env.DebitAccID, env.CreditAccID, reversalTestMoney(t, "100.00"),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err := engine.PostEntryContext(ctx, &original, "poster"); err != nil {
		t.Fatal(err)
	}

	request := posting.ReversalRequest{
		ActorID: "reverser", Reason: "duplicate invoice",
		EffectiveDate: time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC),
		Reference:     "case-42", SourceSystem: "billing", IdempotencyKey: "pg-reversal-42",
	}
	first, replay, err := engine.ReverseWithRequestResultContext(ctx, env.CompanyID, original.ID, request, request.ActorID)
	if err != nil {
		t.Fatal(err)
	}
	if replay {
		t.Fatal("first reversal request was marked as a replay")
	}
	stored, err := env.Driver.GetEntryByID(ctx, env.CompanyID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CreatedBy != request.ActorID || stored.PostedBy != request.ActorID ||
		stored.ReversalReason != request.Reason || stored.IdempotencyKey != request.IdempotencyKey ||
		stored.ReversalReference != request.Reference || stored.SourceModule != request.SourceSystem ||
		stored.PostedAt == nil || stored.OriginalEntryID == nil || *stored.OriginalEntryID != original.ID {
		t.Fatalf("reversal metadata did not survive database round-trip: %#v", stored)
	}

	restartedEngine := posting.NewEngine(env.Driver, nil)
	second, replay, err := restartedEngine.ReverseWithRequestResultContext(ctx, env.CompanyID, original.ID, request, request.ActorID)
	if err != nil {
		t.Fatal(err)
	}
	if !replay || second.ID != first.ID {
		t.Fatalf("idempotent replay = (%s, %v), want (%s, true)", second.ID, replay, first.ID)
	}
}

func TestPostgresConcurrentReversalsCreateOnlyOneActiveReversal(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	engine := posting.NewEngine(env.Driver, nil)
	original := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID,
		env.DebitAccID, env.CreditAccID, reversalTestMoney(t, "100.00"),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err := engine.PostEntryContext(ctx, &original, "poster"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, key := range []string{"pg-concurrent-a", "pg-concurrent-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			<-start
			reversalEngine := posting.NewEngine(env.Driver, nil)
			_, err := reversalEngine.ReverseWithRequestContext(ctx, env.CompanyID, original.ID,
				posting.ReversalRequest{ActorID: "reverser", Reason: "correction", IdempotencyKey: key}, "reverser")
			errs <- err
		}(key)
	}
	close(start)
	wg.Wait()
	close(errs)
	successes, rejected := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, posting.ErrEntryAlreadyReversed):
			rejected++
		default:
			t.Fatalf("unexpected concurrent reversal error: %v", err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("concurrent reversal outcomes = %d success, %d rejected; want exactly one each", successes, rejected)
	}
	var reversalCount int
	if err := env.Driver.Pool().QueryRow(ctx,
		`SELECT count(*) FROM account_moves WHERE company_id=$1 AND classification='reversing'
		 AND entry->>'OriginalEntryID'=$2`, env.CompanyID, original.ID.String()).Scan(&reversalCount); err != nil {
		t.Fatal(err)
	}
	if reversalCount != 1 {
		t.Fatalf("created %d database reversals for original, want one", reversalCount)
	}
}

func TestPostgresReversalFailureRollsBackDraftAndOriginalLink(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	engine := posting.NewEngine(env.Driver, nil)
	original := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID,
		env.DebitAccID, env.CreditAccID, reversalTestMoney(t, "100.00"),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err := engine.PostEntryContext(ctx, &original, "poster"); err != nil {
		t.Fatal(err)
	}
	functionName := "reject_reversal_link_" + uuid.NewString()[:8]
	triggerName := "reject_reversal_link_trigger_" + uuid.NewString()[:8]
	functionSQL := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.id = '%s' AND NEW.status = 'reversed' THEN
				RAISE EXCEPTION 'injected reversal-link failure';
			END IF;
			RETURN NEW;
		END
		$$`, functionName, original.ID)
	if _, err := env.Driver.Pool().Exec(ctx, functionSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Driver.Pool().Exec(ctx,
		fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE OF status ON account_moves FOR EACH ROW EXECUTE FUNCTION %s()`,
			triggerName, functionName)); err != nil {
		t.Fatal(err)
	}

	_, err := engine.ReverseWithRequestContext(ctx, env.CompanyID, original.ID,
		posting.ReversalRequest{ActorID: "reverser", Reason: "correction", IdempotencyKey: "failure-key"},
		"reverser")
	if err == nil {
		t.Fatal("expected injected failure while linking original reversal")
	}
	storedOriginal, err := env.Driver.GetEntryByID(ctx, env.CompanyID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOriginal.Status != posting.EntryPosted || storedOriginal.ReversedEntryID != nil {
		t.Fatalf("failed transaction changed original: %#v", storedOriginal)
	}
	var reversalCount int
	if err := env.Driver.Pool().QueryRow(ctx,
		`SELECT count(*) FROM account_moves WHERE company_id=$1 AND classification='reversing'
		 AND entry->>'idempotency_key'='failure-key'`, env.CompanyID).Scan(&reversalCount); err != nil {
		t.Fatal(err)
	}
	if reversalCount != 0 {
		t.Fatalf("failed transaction left %d persisted reversal drafts", reversalCount)
	}
	logs := auditLogsForCompany(t, ctx, env.Driver, env.CompanyID)
	if got := auditLogsWithAction(logs, "REVERSE", original.ID); got != 0 {
		t.Fatalf("failed transaction left %d REVERSE audit records for original, want 0", got)
	}
	verifyAuditChain(t, logs)
}
