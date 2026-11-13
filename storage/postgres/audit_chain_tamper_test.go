package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/governance"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/storage/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// companyScopedAuditRepository adapts the company-scoped PostgreSQL driver to
// the governance Repository contract so VerifyAuditIntegrity walks exactly the
// audit chain of one tenant, matching the engine's company-scoped access model.
type companyScopedAuditRepository struct {
	driver    *postgres.Driver
	companyID uuid.UUID
}

func (r companyScopedAuditRepository) AppendAuditLog(ctx context.Context, log governance.FinancialAuditLog) error {
	_, err := r.driver.AppendAuditLogAtomic(ctx, log)
	return err
}

func (r companyScopedAuditRepository) GetLastAuditLog(ctx context.Context) (*governance.FinancialAuditLog, error) {
	return r.driver.GetLastAuditLogForCompany(ctx, r.companyID)
}

func (r companyScopedAuditRepository) IterateAuditLogs(ctx context.Context, fromID int64, handler governance.AuditLogHandler) error {
	return r.driver.IterateAuditLogsForCompany(ctx, r.companyID, fromID, handler)
}

// TestVerifyAuditIntegrityDetectsChainTampering proves the cryptographic audit
// chain (SHA-256 hash chain, Hash_n = SHA256(Hash_{n-1} || Payload_n ||
// Timestamp)) is tamper-evident end to end against a real PostgreSQL audit log:
//   - a chain built through real posting (drafts, posts, master-data events)
//     verifies successfully;
//   - flipping a single byte of a stored payload behind the immutability
//     trigger's back fails verification immediately with an
//     AuditIntegrityError wrapping ErrAuditChainBroken;
//   - deleting a middle log behind the no-delete trigger fails verification at
//     the orphaned successor;
//   - restoring the tampered rows returns the chain to a verified state.
func TestVerifyAuditIntegrityDetectsChainTampering(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)

	repository := companyScopedAuditRepository{driver: env.Driver, companyID: env.CompanyID}
	verify := func() error {
		ok, err := governance.VerifyAuditIntegrity(ctx, repository)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("audit integrity verification returned false without an error")
		}
		return nil
	}

	// Post three entries through the real posting path; every lifecycle event
	// appends to the tamper-evident chain inside the posting transaction.
	entryIDs := make([]uuid.UUID, 0, 3)
	entryDate := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	for i, amountText := range []string{"10.00", "20.00", "30.00"} {
		amount, err := financialtypes.NewMoneyFromString(amountText, "USD")
		if err != nil {
			t.Fatal(err)
		}
		zero, err := financialtypes.Zero("USD")
		if err != nil {
			t.Fatal(err)
		}
		entry, err := posting.NewJournalEntry(env.JournalID, entryDate, "audit-chain-maker", []posting.JournalEntryLine{
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.DebitAccID, Debit: amount, Credit: zero},
			{CompanyID: env.CompanyID, ID: uuid.New(), AccountID: env.CreditAccID, Debit: zero, Credit: amount},
		})
		if err != nil {
			t.Fatal(err)
		}
		entry.CompanyID = env.CompanyID
		entry.PeriodID = env.PeriodID
		entry.Reference = fmt.Sprintf("AUDIT-%02d", i)
		if err := env.Driver.SaveDraft(ctx, entry); err != nil {
			t.Fatalf("save draft %d: %v", i, err)
		}
		entry.Status = posting.EntryPosted
		entry.PostedBy = "audit-chain-checker"
		posted, err := env.Driver.PostAndSequence(ctx, entry)
		if err != nil {
			t.Fatalf("post entry %d: %v", i, err)
		}
		entryIDs = append(entryIDs, posted.ID)
	}

	if err := verify(); err != nil {
		t.Fatalf("clean chain verification failed: %v", err)
	}

	// Direct tampering must be rejected by the immutability trigger before we
	// bypass it: the audit trail is not writable in normal operation.
	err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE financial_audit_logs SET payload_after = set_byte(payload_after, 0, get_byte(payload_after, 0) # 1)
			 WHERE company_id=$1 AND entity_id=$2 AND action='POST'`,
			env.CompanyID, entryIDs[0])
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23001" {
		t.Fatalf("direct audit tamper error = %v, want SQLSTATE 23001 from the immutability trigger", err)
	}
	if err := verify(); err != nil {
		t.Fatalf("chain must still verify after a rejected tamper attempt: %v", err)
	}

	// Capture the pristine payload so the tamper can be reverted exactly.
	var tamperedLogID int64
	var pristinePayload []byte
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, payload_after FROM financial_audit_logs
			 WHERE company_id=$1 AND entity_id=$2 AND action='POST'`,
			env.CompanyID, entryIDs[0]).Scan(&tamperedLogID, &pristinePayload)
	}); err != nil {
		t.Fatal(err)
	}

	alterAuditGuard := func(tx pgx.Tx, trigger string, enable bool) error {
		action := "DISABLE"
		if enable {
			action = "ENABLE"
		}
		_, err := tx.Exec(ctx, "ALTER TABLE financial_audit_logs "+action+" TRIGGER "+trigger)
		return err
	}

	// Tamper vector 1: flip one bit of the first byte of a stored payload
	// behind the no-update trigger's back.
	err = env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		if err := alterAuditGuard(tx, "trg_financial_audit_logs_no_update", false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE financial_audit_logs SET payload_after = set_byte(payload_after, 0, get_byte(payload_after, 0) # 1)
			 WHERE company_id=$1 AND id=$2`, env.CompanyID, tamperedLogID); err != nil {
			return err
		}
		return alterAuditGuard(tx, "trg_financial_audit_logs_no_update", true)
	})
	if err != nil {
		t.Fatal(err)
	}

	_, verifyErr := governance.VerifyAuditIntegrity(ctx, repository)
	if verifyErr == nil {
		t.Fatal("audit integrity verification passed after a payload byte flip, want immediate failure")
	}
	var integrityErr *governance.AuditIntegrityError
	if !errors.As(verifyErr, &integrityErr) || !errors.Is(verifyErr, governance.ErrAuditChainBroken) {
		t.Fatalf("verification error = %v, want *AuditIntegrityError wrapping ErrAuditChainBroken", verifyErr)
	}
	if integrityErr.LogID != tamperedLogID {
		t.Fatalf("integrity error log id = %d, want the tampered log %d", integrityErr.LogID, tamperedLogID)
	}

	// Restore the pristine payload behind the guard and verify again.
	err = env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		if err := alterAuditGuard(tx, "trg_financial_audit_logs_no_update", false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE financial_audit_logs SET payload_after=$3 WHERE company_id=$1 AND id=$2`,
			env.CompanyID, tamperedLogID, pristinePayload); err != nil {
			return err
		}
		return alterAuditGuard(tx, "trg_financial_audit_logs_no_update", true)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Fatalf("chain verification after restoring the payload failed: %v", err)
	}

	// Tamper vector 2: delete a middle log behind the no-delete trigger. The
	// successor's previous_log_hash then points at a hash nobody produced, and
	// the verifier must fail on the orphaned link.
	var middleLogID int64
	var middleRow struct {
		EntityType      string
		EntityID        uuid.UUID
		Action          string
		ActorID         uuid.UUID
		PayloadBefore   []byte
		PayloadAfter    []byte
		Timestamp       time.Time
		PreviousLogHash string
		CurrentLogHash  string
	}
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, entity_type, entity_id, action, actor_id, payload_before, payload_after,
			        timestamp, previous_log_hash, current_log_hash
			 FROM financial_audit_logs
			 WHERE company_id=$1 AND entity_id=$2 AND action='POST'`,
			env.CompanyID, entryIDs[1]).Scan(&middleLogID, &middleRow.EntityType, &middleRow.EntityID,
			&middleRow.Action, &middleRow.ActorID, &middleRow.PayloadBefore, &middleRow.PayloadAfter,
			&middleRow.Timestamp, &middleRow.PreviousLogHash, &middleRow.CurrentLogHash)
	}); err != nil {
		t.Fatal(err)
	}
	var successorLogID int64
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM financial_audit_logs
			 WHERE company_id=$1 AND id > $2 ORDER BY id LIMIT 1`,
			env.CompanyID, middleLogID).Scan(&successorLogID)
	}); err != nil {
		t.Fatal(err)
	}

	err = env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		if err := alterAuditGuard(tx, "trg_financial_audit_logs_no_delete", false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM financial_audit_logs WHERE company_id=$1 AND id=$2`,
			env.CompanyID, middleLogID); err != nil {
			return err
		}
		return alterAuditGuard(tx, "trg_financial_audit_logs_no_delete", true)
	})
	if err != nil {
		t.Fatal(err)
	}

	_, verifyErr = governance.VerifyAuditIntegrity(ctx, repository)
	if verifyErr == nil {
		t.Fatal("audit integrity verification passed after deleting a middle log, want immediate failure")
	}
	if !errors.As(verifyErr, &integrityErr) || !errors.Is(verifyErr, governance.ErrAuditChainBroken) {
		t.Fatalf("verification error = %v, want *AuditIntegrityError wrapping ErrAuditChainBroken", verifyErr)
	}
	if integrityErr.LogID != successorLogID {
		t.Fatalf("integrity error log id = %d, want the orphaned successor %d", integrityErr.LogID, successorLogID)
	}

	// Restore the deleted log (identity column needs OVERRIDING SYSTEM VALUE)
	// and verify the chain is whole again.
	err = env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO financial_audit_logs(
			id, company_id, entity_type, entity_id, action, actor_id, payload_before, payload_after,
			timestamp, previous_log_hash, current_log_hash
		) OVERRIDING SYSTEM VALUE VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			middleLogID, env.CompanyID, middleRow.EntityType, middleRow.EntityID, middleRow.Action,
			middleRow.ActorID, middleRow.PayloadBefore, middleRow.PayloadAfter, middleRow.Timestamp,
			middleRow.PreviousLogHash, middleRow.CurrentLogHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("restored %d audit log rows, want 1", tag.RowsAffected())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Fatalf("chain verification after restoring the deleted log failed: %v", err)
	}
}
