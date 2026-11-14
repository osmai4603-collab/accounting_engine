package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	"github.com/shopspring/decimal"
)

func TestP405ReconciliationExceptionLifecycleIsAtomicAndCompanyScoped(t *testing.T) {
	ctx, driver, _, _, companyID, _, _, _ := setupPostgresPostingEnv(t)
	t.Cleanup(driver.Close)

	accountID := uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{
		CompanyID: companyID, ID: accountID, Code: "RC-" + accountID.String()[:8],
		Name: "Reconciliation control", AccountType: masterdata.AccountTypeAsset,
		NormalBalance: masterdata.NormalBalanceDebit, IsControlAccount: true,
		IsReconcilable: true, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	report := subledgers.DiscrepancyReport{
		CompanyID: companyID, Domain: "AR", ControlAccountID: accountID,
		AsOfDate:       time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC),
		FunctionalCurr: "USD", SubledgerBalance: decimal.NewFromInt(120),
		GLBalance: decimal.NewFromInt(100), Difference: decimal.NewFromInt(20),
		DiscoveredAt: now,
	}
	exception := subledgers.ReconciliationException{
		ID: uuid.New(), CompanyID: companyID, Domain: report.Domain,
		ControlAccountID: report.ControlAccountID, AsOfDate: report.AsOfDate,
		FunctionalCurr: report.FunctionalCurr, SubledgerBalance: report.SubledgerBalance,
		GLBalance: report.GLBalance, Difference: report.Difference,
		Status: subledgers.ReconciliationExceptionUnmatched, ActorID: uuid.New(),
		Reason:    "Automated reconciliation detected a variance.",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := driver.SaveReconciliationOutcome(ctx, report, &exception); err != nil {
		t.Fatal(err)
	}

	if err := driver.SaveReconciliationOutcome(ctx, report, &exception); err == nil {
		t.Fatal("duplicate exception unexpectedly succeeded")
	}
	var reportCount int
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM subledger_reconciliation_logs
			WHERE company_id=$1 AND control_account_id=$2 AND as_of_date=$3`,
			companyID, accountID, report.AsOfDate).Scan(&reportCount)
	}); err != nil {
		t.Fatal(err)
	}
	if reportCount != 1 {
		t.Fatalf("report rows after duplicate exception rollback = %d, want 1", reportCount)
	}

	if err := driver.TransitionReconciliationException(ctx, companyID, exception.ID,
		subledgers.ReconciliationExceptionUnmatched, subledgers.ReconciliationExceptionMatching,
		uuid.New(), "Investigating source documents.", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := driver.TransitionReconciliationException(ctx, companyID, exception.ID,
		subledgers.ReconciliationExceptionUnmatched, subledgers.ReconciliationExceptionDisputed,
		uuid.New(), "Stale transition.", now.Add(2*time.Minute)); !errors.Is(err, subledgers.ErrReconciliationExceptionConflict) {
		t.Fatalf("stale transition error = %v, want status conflict", err)
	}
	stored, err := driver.GetReconciliationException(ctx, companyID, exception.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != subledgers.ReconciliationExceptionMatching ||
		stored.Reason != "Investigating source documents." {
		t.Fatalf("stored exception lost its transition: %+v", stored)
	}
	history, err := driver.ListReconciliationExceptionTransitions(ctx, companyID, exception.ID)
	if err != nil || len(history) != 2 || history[1].ToStatus != subledgers.ReconciliationExceptionMatching {
		t.Fatalf("exception history = %+v, err=%v", history, err)
	}
	if _, err := driver.GetReconciliationException(ctx, uuid.New(), exception.ID); !errors.Is(err, subledgers.ErrReconciliationExceptionNotFound) {
		t.Fatalf("cross-company read error = %v, want not found", err)
	}
	exceptions, err := driver.ListReconciliationExceptions(ctx, companyID)
	if err != nil || len(exceptions) != 1 {
		t.Fatalf("exceptions = %+v, err=%v", exceptions, err)
	}
}
