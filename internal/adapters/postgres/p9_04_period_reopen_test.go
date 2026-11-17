package postgres_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func TestP904ReopenedPeriodAcceptsPosting(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	engine := posting.NewEngine(env.Driver, nil)
	amount, err := financialtypes.NewMoneyFromString("100.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	entry := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID,
		env.DebitAccID, env.CreditAccID, amount, date)
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	actorID, approvalID := uuid.New(), uuid.New()
	for _, state := range []masterdata.PeriodState{masterdata.PeriodSoftClosed, masterdata.PeriodHardClosed} {
		if err := service.TransitionPeriodStateContext(ctx, env.CompanyID, env.PeriodID,
			state, actorID, "period close"); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.PostEntryContext(ctx, &entry, "poster"); !errors.Is(err, masterdata.ErrFiscalPeriodClosed) {
		t.Fatalf("posting before reopen error = %v, want closed-period error", err)
	}
	if err := service.ReopenPeriodWithApprovalContext(ctx, env.CompanyID, env.PeriodID,
		actorID, approvalID, "approved correction"); err != nil {
		t.Fatalf("reopen period: %v", err)
	}
	var storedState masterdata.PeriodState
	var storedActor, storedApproval uuid.UUID
	var storedReason string
	var storedAt time.Time
	var auditActor uuid.UUID
	var auditReason, auditApproval, auditTimestamp string
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT state,last_state_actor_id,last_state_reason,last_state_at,last_state_approval_id
			FROM fiscal_periods WHERE company_id=$1 AND id=$2`, env.CompanyID, env.PeriodID).
			Scan(&storedState, &storedActor, &storedReason, &storedAt, &storedApproval); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT actor_id,convert_from(payload_after,'UTF8')::jsonb->>'reason',
			convert_from(payload_after,'UTF8')::jsonb->>'approval_id',
			convert_from(payload_after,'UTF8')::jsonb->>'timestamp'
			FROM financial_audit_logs
			WHERE company_id=$1 AND entity_type='fiscal_period' AND entity_id=$2
			  AND action='FISCAL_PERIOD_REOPENED' ORDER BY id DESC LIMIT 1`,
			env.CompanyID, env.PeriodID).Scan(&auditActor, &auditReason, &auditApproval, &auditTimestamp)
	}); err != nil {
		t.Fatal(err)
	}
	auditAt, err := time.Parse(time.RFC3339Nano, auditTimestamp)
	if err != nil {
		t.Fatalf("parse audited reopen timestamp %q: %v", auditTimestamp, err)
	}
	if storedState != masterdata.PeriodOpen || storedActor != actorID ||
		storedReason != "approved correction" || storedAt.IsZero() || storedApproval != approvalID {
		t.Fatalf("persisted reopen metadata = state:%s actor:%s reason:%q at:%s approval:%s",
			storedState, storedActor, storedReason, storedAt, storedApproval)
	}
	if auditActor != actorID || auditReason != storedReason || auditApproval != approvalID.String() ||
		!auditAt.Equal(storedAt) {
		t.Fatalf("audit reopen metadata = actor:%s reason:%q timestamp:%s approval:%q",
			auditActor, auditReason, auditTimestamp, auditApproval)
	}
	if err := engine.PostEntryContext(ctx, &entry, "poster"); err != nil {
		t.Fatalf("posting after reopen: %v", err)
	}
	if entry.Status != posting.EntryPosted {
		t.Fatalf("entry status after reopen post = %s, want %s", entry.Status, posting.EntryPosted)
	}
}

func TestP904AnnualCloseBlocksPeriodReopen(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	actorID, approvalID := uuid.New(), uuid.New()
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	for _, state := range []masterdata.PeriodState{masterdata.PeriodSoftClosed, masterdata.PeriodHardClosed} {
		if err := service.TransitionPeriodStateContext(ctx, env.CompanyID, env.PeriodID,
			state, actorID, "period close"); err != nil {
			t.Fatal(err)
		}
	}
	period, err := env.Driver.GetPeriodByDateForCompany(ctx, env.CompanyID, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Driver.UpdateFiscalYearStateForCompanyContext(ctx, env.CompanyID, period.FiscalYearID, masterdata.FiscalYearSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := env.Driver.UpdateFiscalYearStateForCompanyContext(ctx, env.CompanyID, period.FiscalYearID, masterdata.FiscalYearHardClosed); err != nil {
		t.Fatal(err)
	}
	if err := service.ReopenPeriodWithApprovalContext(ctx, env.CompanyID, env.PeriodID,
		actorID, approvalID, "approved correction"); !errors.Is(err, masterdata.ErrPeriodCoveredByAnnualClose) {
		t.Fatalf("reopening annually closed period error = %v, want annual-close safeguard", err)
	}
}

func TestP904ConcurrentPeriodReopenIsSingleTransition(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	actorID := uuid.New()
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	for _, state := range []masterdata.PeriodState{masterdata.PeriodSoftClosed, masterdata.PeriodHardClosed} {
		if err := service.TransitionPeriodStateContext(ctx, env.CompanyID, env.PeriodID,
			state, actorID, "period close"); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errs <- service.ReopenPeriodWithApprovalContext(ctx, env.CompanyID, env.PeriodID,
				actorID, uuid.New(), "approved correction")
		}()
	}
	close(start)
	workers.Wait()
	close(errs)

	successes, rejected := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, masterdata.ErrInvalidPeriodTransition):
			rejected++
		default:
			t.Fatalf("unexpected concurrent reopen error: %v", err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("concurrent reopen results: successes=%d rejected=%d, want 1/1", successes, rejected)
	}
}

func TestP904ConcurrentReopenAndPosting(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	engine := posting.NewEngine(env.Driver, nil)
	amount, err := financialtypes.NewMoneyFromString("100.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	entry := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID,
		env.DebitAccID, env.CreditAccID, amount, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	actorID, approvalID := uuid.New(), uuid.New()
	for _, state := range []masterdata.PeriodState{masterdata.PeriodSoftClosed, masterdata.PeriodHardClosed} {
		if err := service.TransitionPeriodStateContext(ctx, env.CompanyID, env.PeriodID,
			state, actorID, "period close"); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	reopenErr := make(chan error, 1)
	postErr := make(chan error, 1)
	go func() {
		<-start
		reopenErr <- service.ReopenPeriodWithApprovalContext(ctx, env.CompanyID, env.PeriodID,
			actorID, approvalID, "approved correction")
	}()
	go func() {
		<-start
		postErr <- engine.PostEntryContext(ctx, &entry, "poster")
	}()
	close(start)

	if err := <-reopenErr; err != nil {
		t.Fatalf("concurrent reopen: %v", err)
	}
	err = <-postErr
	if err != nil && !errors.Is(err, masterdata.ErrFiscalPeriodClosed) {
		t.Fatalf("concurrent posting returned unexpected error: %v", err)
	}
	if err != nil {
		if retryErr := engine.PostEntryContext(ctx, &entry, "poster"); retryErr != nil {
			t.Fatalf("posting after concurrent reopen: %v", retryErr)
		}
	}
	if entry.Status != posting.EntryPosted {
		t.Fatalf("entry status after concurrent reopen/post = %s, want %s", entry.Status, posting.EntryPosted)
	}
}
