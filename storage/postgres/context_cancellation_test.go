package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresQueryHonorsContextCancellation(t *testing.T) {
	driver, err := newIsolatedPostgresDriver(t, context.Background())
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = driver.Pool().Exec(ctx, "SELECT pg_sleep(5)")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled PostgreSQL query error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("PostgreSQL query took %s to honor cancellation, want under one second", elapsed)
	}
}

// TestPostgresPostingContextCancellationIsAtomic interrupts a posting
// transaction mid-flight and proves the database keeps zero financial residue:
// the draft stays a draft, no subledger open line exists, the journal sequence
// counter is untouched, and no audit record was written. A temporary trigger
// stalls the draft->posted UPDATE inside PostgreSQL for a fixed window so the
// context deadline deterministically fires while the transaction is executing,
// which forces pgx to cancel the query and roll the transaction back.
func TestPostgresPostingContextCancellationIsAtomic(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}

	companyID, journalID := uuid.New(), uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("CANCEL-%s", companyID.String()[:8]),
		LegalName: "Context Cancellation Atomicity Test", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id, company_id, code, name, current_sequence)
			VALUES($1, $2, $3, 'Cancellation Journal', 0)`, journalID, companyID, "CJ")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	controlID, revenueID := uuid.New(), uuid.New()
	for _, account := range []masterdata.Account{
		{CompanyID: companyID, ID: controlID, Code: "1200", Name: "Trade receivables",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
			IsControlAccount: true, IsReconcilable: true, IsActive: true},
		{CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true},
	} {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}

	amount, err := financialtypes.NewMoneyFromString("250.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := financialtypes.Zero("USD")
	entryDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	entry, err := posting.NewJournalEntry(journalID, entryDate, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: controlID, Debit: amount, Credit: zero},
		{CompanyID: companyID, ID: uuid.New(), AccountID: revenueID, Debit: zero, Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID
	entry.PeriodID = year.Periods[2].ID
	if err := driver.SaveDraft(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status = posting.EntryPosted
	entry.PostedBy = "checker"
	postedAt := time.Now().UTC()
	entry.PostedAt = &postedAt

	lineID := uuid.New()
	attachment := posting.OpenLineAttachment{
		ID: lineID, CompanyID: companyID, JournalEntryID: entry.ID,
		PartnerID: uuid.New(), ControlAccountID: controlID,
		Kind: string(subledgers.LineReceivable), Amount: amount,
		DocumentDate: entryDate, DueDate: entryDate.AddDate(0, 0, 30), Reference: "INV-CANCEL",
	}

	// A temporary BEFORE UPDATE trigger on account_moves sleeps long enough
	// that the posting transaction is guaranteed to still be executing when
	// the context deadline fires, so the cancellation lands mid-flight.
	if _, err := driver.Pool().Exec(ctx, `CREATE OR REPLACE FUNCTION stall_move_update() RETURNS trigger AS $$
BEGIN
    PERFORM pg_sleep(1);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Pool().Exec(ctx,
		`CREATE TRIGGER trg_stall_move_update BEFORE UPDATE ON account_moves
		 FOR EACH ROW EXECUTE FUNCTION stall_move_update()`); err != nil {
		t.Fatal(err)
	}

	postCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, postErr := driver.PostAndSequenceWithOpenLine(postCtx, entry, attachment)
	if postErr == nil {
		t.Fatal("expected the stalled posting to fail with a context error")
	}
	if !errors.Is(postErr, context.DeadlineExceeded) {
		var pgErr *pgconn.PgError
		if errors.As(postErr, &pgErr) && pgErr.Code == "57014" {
			// query_canceled surfaced as a PostgreSQL error; equally valid.
		} else {
			t.Fatalf("posting cancellation error = %v, want context deadline exceeded or query canceled", postErr)
		}
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled posting took %s to abort, want prompt cancellation", elapsed)
	}

	// Nothing may remain: the draft is intact, no open line, no sequence, no audit.
	stored, err := driver.GetEntryByID(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryDraft {
		t.Fatalf("entry status after cancelled post = %s, want draft", stored.Status)
	}
	if _, err := driver.GetOpenLine(ctx, companyID, lineID); !errors.Is(err, subledgers.ErrNotFound) {
		t.Fatalf("open line after cancelled post error = %v, want not found", err)
	}
	var sequence int64
	if err := driver.Pool().QueryRow(ctx,
		`SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
		companyID, journalID).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if sequence != 0 {
		t.Fatalf("journal sequence after cancelled post = %d, want 0", sequence)
	}
	var auditCount int
	if err := driver.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM financial_audit_logs WHERE company_id=$1 AND entity_id=$2`,
		companyID, entry.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("audit rows after cancelled post = %d, want 0", auditCount)
	}
	var balanceRows int
	if err := driver.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM account_balances WHERE company_id=$1 AND fiscal_period_id=$2`,
		companyID, year.Periods[2].ID).Scan(&balanceRows); err != nil {
		t.Fatal(err)
	}
	if balanceRows != 0 {
		t.Fatalf("materialized balance rows after cancelled post = %d, want 0", balanceRows)
	}

	// After removing the stall the very same entry must post cleanly, proving
	// the rollback left no dangling state behind.
	if _, err := driver.Pool().Exec(ctx, `DROP TRIGGER IF EXISTS trg_stall_move_update ON account_moves`); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.PostAndSequenceWithOpenLine(ctx, entry, attachment); err != nil {
		t.Fatalf("posting after cancellation rollback failed: %v", err)
	}
	stored, err = driver.GetEntryByID(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != posting.EntryPosted {
		t.Fatalf("entry status after successful repost = %s, want posted", stored.Status)
	}
	if stored.SequenceNumber != 1 {
		t.Fatalf("entry sequence after successful repost = %d, want 1", stored.SequenceNumber)
	}
	if _, err := driver.GetOpenLine(ctx, companyID, lineID); err != nil {
		t.Fatalf("open line after successful repost: %v", err)
	}
}
