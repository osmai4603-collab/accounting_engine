package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func p402Draft(t *testing.T, companyID, journalID, debitAccountID, creditAccountID uuid.UUID) posting.JournalEntry {
	t.Helper()
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	amount, err := financialtypes.NewMoneyFromString("10.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := posting.NewJournalEntry(journalID, date, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: debitAccountID, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: creditAccountID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	return entry
}

func TestP402PeriodCloseSerializesAgainstPosting(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, service)
	entry := p402Draft(t, env.CompanyID, env.JournalID, env.DebitAccID, env.CreditAccID)
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}

	closeLocked := make(chan struct{})
	releaseClose := make(chan struct{})
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
			result, err := tx.Exec(ctx, `UPDATE fiscal_periods SET state='hard_closed',closed_at=now()
				WHERE company_id=$1 AND id=$2`, env.CompanyID, env.PeriodID)
			if err != nil {
				return err
			}
			if result.RowsAffected() != 1 {
				return errors.New("concurrent close did not find the entry period")
			}
			close(closeLocked)
			<-releaseClose
			return nil
		})
	}()
	select {
	case <-closeLocked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	postResult := make(chan error, 1)
	go func() { postResult <- engine.PostEntryContext(ctx, &entry, "poster") }()
	select {
	case err := <-postResult:
		close(releaseClose)
		if closeErr := <-closeResult; closeErr != nil {
			t.Fatalf("commit concurrent close: %v", closeErr)
		}
		t.Fatalf("posting completed before close transaction committed: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseClose)
	if err := <-closeResult; err != nil {
		t.Fatalf("commit concurrent close: %v", err)
	}
	select {
	case err := <-postResult:
		if !errors.Is(err, masterdata.ErrFiscalPeriodClosed) {
			t.Fatalf("posting after concurrent close error = %v, want closed-period rejection", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	var sequence int64
	var status posting.EntryStatus
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
			env.CompanyID, env.JournalID).Scan(&sequence); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT status FROM account_moves WHERE company_id=$1 AND id=$2`,
			env.CompanyID, entry.ID).Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if sequence != 0 || status != posting.EntryDraft {
		t.Fatalf("after rejected post: sequence=%d status=%s, want 0/draft", sequence, status)
	}
}

func TestP402PeriodReopenRequiresAndPersistsAudit(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	actorID := uuid.New()
	if err := service.ReopenPeriodContext(ctx, env.CompanyID, env.PeriodID, actorID, "reopen too early"); !errors.Is(err, masterdata.ErrInvalidPeriodTransition) {
		t.Fatalf("reopen of open period error = %v, want invalid transition", err)
	}
	if err := service.TransitionPeriodStateContext(ctx, env.CompanyID, env.PeriodID,
		masterdata.PeriodSoftClosed, actorID, "initial close"); err != nil {
		t.Fatal(err)
	}
	if err := service.TransitionPeriodStateContext(ctx, env.CompanyID, env.PeriodID,
		masterdata.PeriodHardClosed, actorID, "final close"); err != nil {
		t.Fatal(err)
	}
	var closeActor uuid.UUID
	var closeReason string
	var closedAt *time.Time
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT closed_by,last_state_reason,closed_at
			FROM fiscal_periods WHERE company_id=$1 AND id=$2`, env.CompanyID, env.PeriodID).
			Scan(&closeActor, &closeReason, &closedAt)
	}); err != nil {
		t.Fatal(err)
	}
	if closeActor != actorID || closeReason != "final close" || closedAt == nil || closedAt.IsZero() {
		t.Fatalf("close metadata = actor:%s reason:%q at:%v", closeActor, closeReason, closedAt)
	}
	if err := service.ReopenPeriodContext(ctx, env.CompanyID, env.PeriodID, uuid.Nil, "approved"); !errors.Is(err, masterdata.ErrPeriodReopenRequiresAudit) {
		t.Fatalf("reopen without actor error = %v", err)
	}
	if err := service.ReopenPeriodContext(ctx, env.CompanyID, env.PeriodID, actorID, " "); !errors.Is(err, masterdata.ErrPeriodReopenRequiresAudit) {
		t.Fatalf("reopen without reason error = %v", err)
	}
	if err := service.ReopenPeriodContext(ctx, env.CompanyID, env.PeriodID, actorID, "approved correction"); err != nil {
		t.Fatal(err)
	}
	var state masterdata.PeriodState
	var storedActor uuid.UUID
	var reason string
	var changedAt time.Time
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state,last_state_actor_id,last_state_reason,last_state_at
			FROM fiscal_periods WHERE company_id=$1 AND id=$2`, env.CompanyID, env.PeriodID).
			Scan(&state, &storedActor, &reason, &changedAt)
	}); err != nil {
		t.Fatal(err)
	}
	if state != masterdata.PeriodOpen || storedActor != actorID || reason != "approved correction" || changedAt.IsZero() {
		t.Fatalf("reopen metadata = state:%s actor:%s reason:%q at:%s", state, storedActor, reason, changedAt)
	}
	var auditActor uuid.UUID
	var auditReason string
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT actor_id,convert_from(payload_after,'UTF8')::jsonb->>'reason' FROM financial_audit_logs
			WHERE company_id=$1 AND entity_type='fiscal_period' AND entity_id=$2
			  AND action='FISCAL_PERIOD_REOPENED' ORDER BY id DESC LIMIT 1`,
			env.CompanyID, env.PeriodID).Scan(&auditActor, &auditReason)
	}); err != nil {
		t.Fatal(err)
	}
	if auditActor != actorID || auditReason != "approved correction" {
		t.Fatalf("reopen audit actor=%s reason=%q", auditActor, auditReason)
	}
}

func TestP402JournalRulesAndPostedNumberImmutability(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, service)
	if err := env.Driver.UpdateJournalSettingsContext(ctx, env.CompanyID, env.JournalID,
		true, []string{"EUR"}, masterdata.JournalNumberingPerJournal); err != nil {
		t.Fatal(err)
	}
	entry := p402Draft(t, env.CompanyID, env.JournalID, env.DebitAccID, env.CreditAccID)
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.PostEntryContext(ctx, &entry, "poster"); !errors.Is(err, masterdata.ErrJournalCurrencyNotAllowed) {
		t.Fatalf("disallowed journal currency error = %v", err)
	}
	if err := env.Driver.UpdateJournalSettingsContext(ctx, env.CompanyID, env.JournalID,
		true, []string{"USD"}, masterdata.JournalNumberingPerJournal); !errors.Is(err, masterdata.ErrJournalSettingsInUse) {
		t.Fatalf("changing journal rules with an existing draft error = %v", err)
	}
	if err := env.Driver.UpdateJournalSettingsContext(ctx, env.CompanyID, env.JournalID,
		false, []string{"EUR"}, masterdata.JournalNumberingPerJournal); err != nil {
		t.Fatal(err)
	}
	if err := engine.PostEntryContext(ctx, &entry, "poster"); !errors.Is(err, masterdata.ErrJournalInactive) {
		t.Fatalf("posting into inactive journal error = %v", err)
	}
}

func TestP402PostedEntryNumberAndUsedJournalAreProtected(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	service := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, service)
	entry := p402Draft(t, env.CompanyID, env.JournalID, env.DebitAccID, env.CreditAccID)
	if err := engine.CreateDraftContext(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := engine.PostEntryContext(ctx, &entry, "poster"); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC()
	if entry.PostingDate.Year() != today.Year() || entry.PostingDate.YearDay() != today.YearDay() {
		t.Fatalf("posting date = %s, want today's UTC date; effective date = %s", entry.PostingDate, entry.EntryDate)
	}
	if entry.EntryDate.Year() != 2026 || entry.EntryDate.Month() != time.January {
		t.Fatalf("effective date changed during posting: %s", entry.EntryDate)
	}
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE account_moves SET entry_number=entry_number+100
			WHERE company_id=$1 AND id=$2`, env.CompanyID, entry.ID)
		return err
	}); err == nil {
		t.Fatal("expected posted entry number mutation to be rejected")
	}
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM journals WHERE company_id=$1 AND id=$2`, env.CompanyID, env.JournalID)
		return err
	}); err == nil {
		t.Fatal("expected used journal deletion to be rejected")
	}
}
