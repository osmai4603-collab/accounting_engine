package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func grantGovernanceRole(
	t *testing.T,
	ctx context.Context,
	driver *postgres.Driver,
	companyID, userID uuid.UUID,
	role governance.Role,
) {
	t.Helper()
	err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO governance_roles(company_id,role)
			VALUES($1,$2) ON CONFLICT (company_id,role) DO NOTHING`, companyID, role); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_role_assignments(company_id,user_id,role,granted_by)
			VALUES($1,$2,$3,$4)`, companyID, userID, role, uuid.New())
		return err
	})
	if err != nil {
		t.Fatalf("grant governance role %s: %v", role, err)
	}
}

func auditLogsForCompany(t *testing.T, ctx context.Context, driver *postgres.Driver, companyID uuid.UUID) []governance.FinancialAuditLog {
	t.Helper()
	var logs []governance.FinancialAuditLog
	err := driver.IterateAuditLogsForCompany(ctx, companyID, 0, func(log governance.FinancialAuditLog) error {
		logs = append(logs, log)
		return nil
	})
	if err != nil {
		t.Fatalf("iterate audit logs: %v", err)
	}
	return logs
}

func verifyAuditChain(t *testing.T, logs []governance.FinancialAuditLog) {
	t.Helper()
	previous := governance.GenesisHash
	for i, log := range logs {
		if log.PreviousLogHash != previous {
			t.Fatalf("audit log %d previous hash = %q, want %q", i, log.PreviousLogHash, previous)
		}
		if calculated := governance.CalculateHash(log); calculated != log.CurrentLogHash {
			t.Fatalf("audit log %d hash = %q, want %q", i, log.CurrentLogHash, calculated)
		}
		previous = log.CurrentLogHash
	}
}

func auditLogsWithAction(logs []governance.FinancialAuditLog, action string, entityID uuid.UUID) int {
	count := 0
	for _, log := range logs {
		if log.Action == action && log.EntityID == entityID {
			count++
		}
	}
	return count
}

// TestApprovalAmountTamperingRejection proves that an approval is bound to the
// stored entry's functional total and content fingerprint: a request cannot be
// opened with an understated amount, and editing the draft after approval
// invalidates the approval even though the approval row is still "completed".
func TestApprovalAmountTamperingRejection(t *testing.T) {
	ctx, driver, masterData, _, companyID, journalID, debitAccountID, creditAccountID := setupPostgresPostingEnv(t)
	defer driver.Close()

	amount, err := financialtypes.NewMoneyFromString("100.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	entry := createDraftTestEntry(t, ctx, posting.NewEngine(driver, masterData), companyID, journalID,
		debitAccountID, creditAccountID, amount, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))

	gov := governance.NewEngine(driver)
	if gov.Approvals == nil {
		t.Fatal("governance approvals engine is not configured")
	}

	tampered, err := financialtypes.NewMoneyFromString("99.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gov.Approvals.Start(ctx, companyID, entry.ID, uuid.New(), "general", tampered, time.Time{}); !errors.Is(err, governance.ErrApprovalAmountMismatch) {
		t.Fatalf("understated approval amount error = %v, want %v", err, governance.ErrApprovalAmountMismatch)
	}

	request, err := gov.Approvals.Start(ctx, companyID, entry.ID, uuid.New(), "general", amount, time.Time{})
	if err != nil {
		t.Fatalf("start approval: %v", err)
	}
	approverID := uuid.New()
	grantGovernanceRole(t, ctx, driver, companyID, approverID, governance.RoleSeniorAccountant)
	if _, err := gov.Approvals.Approve(ctx, companyID, request.ID, approverID, time.Time{}); err != nil {
		t.Fatalf("approve request: %v", err)
	}
	approved, err := gov.Approvals.IsApproved(ctx, companyID, entry.ID)
	if err != nil || !approved {
		t.Fatalf("approval status = %v, %v; want approved", approved, err)
	}

	// Tamper with the stored draft after approval.
	stored, err := driver.GetEntryByIDForCompany(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	tamperedLineAmount, err := financialtypes.NewMoneyFromString("50.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	stored.Lines[0].Debit = tamperedLineAmount
	stored.Lines[1].Credit = tamperedLineAmount
	payload, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE account_moves SET entry=$3
			WHERE company_id=$1 AND id=$2`, companyID, entry.ID, payload)
		return err
	}); err != nil {
		t.Fatalf("tamper with stored draft: %v", err)
	}

	approved, err = gov.Approvals.IsApproved(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatalf("approval status after tampering: %v", err)
	}
	if approved {
		t.Fatal("approval remained valid after the draft content changed")
	}

	// A second request for the same entry must be rejected by the unique index.
	if _, err := gov.Approvals.Start(ctx, companyID, entry.ID, uuid.New(), "general", tamperedLineAmount, time.Time{}); !errors.Is(err, governance.ErrApprovalAlreadyExists) {
		t.Fatalf("duplicate approval request error = %v, want %v", err, governance.ErrApprovalAlreadyExists)
	}
}

// TestMakerCheckerThresholdEnforcement proves that the company policy threshold
// is enforced at the posting boundary: below the threshold posting succeeds,
// above it posting is blocked until the entry has a matching completed approval.
func TestMakerCheckerThresholdEnforcement(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, debitAccountID, creditAccountID := setupPostgresPostingEnv(t)
	defer driver.Close()

	threshold := decimal.NewFromInt(1000)
	if err := driver.SetMakerCheckerThreshold(ctx, companyID, threshold); err != nil {
		t.Fatalf("set maker-checker threshold: %v", err)
	}
	gov := governance.NewEngine(driver)
	engine.SetApprovalGate(gov.ApprovalGate())
	engine.SetMakerCheckerPolicy(driver)

	postingDate := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	belowAmount, err := financialtypes.NewMoneyFromString("500.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	below := createDraftTestEntry(t, ctx, engine, companyID, journalID, debitAccountID, creditAccountID, belowAmount, postingDate)
	if err := engine.PostContext(ctx, companyID, below.ID, "poster"); err != nil {
		t.Fatalf("post entry below threshold: %v", err)
	}

	aboveAmount, err := financialtypes.NewMoneyFromString("1500.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	above := createDraftTestEntry(t, ctx, engine, companyID, journalID, debitAccountID, creditAccountID, aboveAmount, postingDate)
	if err := engine.PostContext(ctx, companyID, above.ID, "maker"); !errors.Is(err, posting.ErrApprovalRequired) {
		t.Fatalf("post above threshold without approval error = %v, want %v", err, posting.ErrApprovalRequired)
	}
	if err := engine.PostContext(ctx, companyID, above.ID, "checker"); !errors.Is(err, posting.ErrApprovalRequired) {
		t.Fatalf("post above threshold as checker without approval error = %v, want %v", err, posting.ErrApprovalRequired)
	}

	makerID := uuid.New()
	request, err := gov.Approvals.Start(ctx, companyID, above.ID, makerID, "general", aboveAmount, time.Time{})
	if err != nil {
		t.Fatalf("start approval for entry above threshold: %v", err)
	}
	approverID := uuid.New()
	grantGovernanceRole(t, ctx, driver, companyID, approverID, governance.RoleSeniorAccountant)
	if _, err := gov.Approvals.Approve(ctx, companyID, request.ID, approverID, time.Time{}); err != nil {
		t.Fatalf("approve entry above threshold: %v", err)
	}

	if err := engine.PostContext(ctx, companyID, above.ID, "maker"); !errors.Is(err, financialtypes.ErrMakerCheckerViolation) {
		t.Fatalf("maker posting approved entry error = %v, want %v", err, financialtypes.ErrMakerCheckerViolation)
	}
	if err := engine.PostContext(ctx, companyID, above.ID, "checker"); err != nil {
		t.Fatalf("checker posting approved entry: %v", err)
	}
}

// TestAtomicPostingAuditLogCreation proves that the posting, sequence allocation
// and audit-chain append commit or roll back as a single unit, and that the
// lifecycle events for posting, reversal and master-data changes reach the
// tamper-evident chain.
func TestAtomicPostingAuditLogCreation(t *testing.T) {
	ctx, driver, _, engine, companyID, journalID, debitAccountID, creditAccountID := setupPostgresPostingEnv(t)
	defer driver.Close()

	amount, err := financialtypes.NewMoneyFromString("250.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	entry := createDraftTestEntry(t, ctx, engine, companyID, journalID, debitAccountID, creditAccountID,
		amount, time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC))

	// Force the audit append for this entry to fail inside the posting
	// transaction. The posting must roll back entirely.
	triggerFunction := fmt.Sprintf("fail_audit_%s", strings.ReplaceAll(entry.ID.String(), "-", ""))
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`
			CREATE OR REPLACE FUNCTION %[1]s() RETURNS trigger AS $fn$
			BEGIN
				IF NEW.entity_id = '%[2]s' THEN
					RAISE EXCEPTION 'injected audit failure' USING ERRCODE = 'check_violation';
				END IF;
				RETURN NEW;
			END;
			$fn$ LANGUAGE plpgsql;
			CREATE TRIGGER %[1]s BEFORE INSERT ON financial_audit_logs
				FOR EACH ROW EXECUTE FUNCTION %[1]s();`, triggerFunction, entry.ID.String()))
		return err
	}); err != nil {
		t.Fatalf("install audit failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_ = driver.WithCompanyTx(context.WithoutCancel(ctx), companyID, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %[1]s ON financial_audit_logs;
				DROP FUNCTION IF EXISTS %[1]s();`, triggerFunction))
			return err
		})
	})

	sequenceBefore := governanceJournalSequence(t, ctx, driver, companyID, journalID)
	logsBefore := auditLogsForCompany(t, ctx, driver, companyID)
	if err := engine.PostContext(ctx, companyID, entry.ID, "poster"); err == nil ||
		!strings.Contains(err.Error(), "injected audit failure") {
		t.Fatalf("posting with failing audit append error = %v, want injected audit failure", err)
	}
	if sequenceAfter := governanceJournalSequence(t, ctx, driver, companyID, journalID); sequenceAfter != sequenceBefore {
		t.Fatalf("journal sequence = %d after rollback, want %d", sequenceAfter, sequenceBefore)
	}
	if logsAfter := auditLogsForCompany(t, ctx, driver, companyID); len(logsAfter) != len(logsBefore) {
		t.Fatalf("audit log count = %d after rollback, want %d", len(logsAfter), len(logsBefore))
	}
	rolledBack, err := driver.GetEntryByIDForCompany(ctx, companyID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Status != posting.EntryDraft {
		t.Fatalf("entry status after rollback = %s, want draft", rolledBack.Status)
	}

	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %[1]s ON financial_audit_logs;
			DROP FUNCTION IF EXISTS %[1]s();`, triggerFunction))
		return err
	}); err != nil {
		t.Fatalf("remove audit failure trigger: %v", err)
	}

	if err := engine.PostContext(ctx, companyID, entry.ID, "poster"); err != nil {
		t.Fatalf("post entry after audit trigger removal: %v", err)
	}
	logs := auditLogsForCompany(t, ctx, driver, companyID)
	if got := auditLogsWithAction(logs, "POST", entry.ID); got != 1 {
		t.Fatalf("POST audit entries = %d, want 1", got)
	}
	verifyAuditChain(t, logs)

	if _, err := engine.Reverse(companyID, entry.ID, "poster"); err != nil {
		t.Fatalf("reverse posted entry: %v", err)
	}
	logs = auditLogsForCompany(t, ctx, driver, companyID)
	if got := auditLogsWithAction(logs, "REVERSE", entry.ID); got != 1 {
		t.Fatalf("REVERSE audit entries = %d, want 1", got)
	}
	verifyAuditChain(t, logs)

	// Master-data and fiscal-year lifecycle events must also be audited.
	account := masterdata.Account{
		CompanyID: companyID, ID: debitAccountID, Code: "1000", Name: "Asset Account Renamed",
		AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true,
	}
	if err := driver.CreateAccount(ctx, account); err != nil {
		t.Fatalf("update account: %v", err)
	}
	logs = auditLogsForCompany(t, ctx, driver, companyID)
	if got := auditLogsWithAction(logs, "ACCOUNT_UPDATED", debitAccountID); got != 1 {
		t.Fatalf("ACCOUNT_UPDATED audit entries = %d, want 1", got)
	}

	yearID := governanceFiscalYearID(t, ctx, driver, companyID)
	if err := driver.UpdateFiscalYearState(ctx, companyID, yearID, masterdata.FiscalYearSoftClosed); err != nil {
		t.Fatalf("soft close fiscal year: %v", err)
	}
	if err := driver.UpdateFiscalYearState(ctx, companyID, yearID, masterdata.FiscalYearHardClosed); err != nil {
		t.Fatalf("hard close fiscal year: %v", err)
	}
	logs = auditLogsForCompany(t, ctx, driver, companyID)
	if got := auditLogsWithAction(logs, "FISCAL_YEAR_CLOSED", yearID); got != 1 {
		t.Fatalf("FISCAL_YEAR_CLOSED audit entries = %d, want 1", got)
	}
	verifyAuditChain(t, logs)
	if err := driver.VerifyDay(ctx, companyID, time.Time{}); err != nil {
		t.Fatalf("verify persisted audit chain: %v", err)
	}
}

func governanceJournalSequence(t *testing.T, ctx context.Context, driver *postgres.Driver, companyID, journalID uuid.UUID) int64 {
	t.Helper()
	var sequence int64
	err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
			companyID, journalID).Scan(&sequence)
	})
	if err != nil {
		t.Fatalf("read journal sequence: %v", err)
	}
	return sequence
}

func governanceFiscalYearID(t *testing.T, ctx context.Context, driver *postgres.Driver, companyID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM fiscal_years WHERE company_id=$1 ORDER BY start_date LIMIT 1`,
			companyID).Scan(&id)
	})
	if err != nil {
		t.Fatalf("read fiscal year id: %v", err)
	}
	return id
}
