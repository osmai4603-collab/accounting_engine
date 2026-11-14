package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
)

func TestP406FiscalYearAuditedReopenRequiresActorAndReasonOnPostgres(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	period, err := env.Driver.GetPeriodByDateForCompany(ctx, env.CompanyID, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	yearID := period.FiscalYearID

	// Normal forward transitions to HardClosed
	if err := env.Driver.UpdateFiscalYearStateForCompanyContext(ctx, env.CompanyID, yearID, masterdata.FiscalYearSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := env.Driver.UpdateFiscalYearStateForCompanyContext(ctx, env.CompanyID, yearID, masterdata.FiscalYearHardClosed); err != nil {
		t.Fatal(err)
	}

	actorID := uuid.New()

	// Reopen attempt without actor or reason must fail
	if err := env.Driver.ReopenFiscalYearForCompanyContext(ctx, env.CompanyID, yearID, uuid.Nil, "approved"); !errors.Is(err, masterdata.ErrPeriodReopenRequiresAudit) {
		t.Fatalf("expected ErrPeriodReopenRequiresAudit for nil actor, got %v", err)
	}
	if err := env.Driver.ReopenFiscalYearForCompanyContext(ctx, env.CompanyID, yearID, actorID, "  "); !errors.Is(err, masterdata.ErrPeriodReopenRequiresAudit) {
		t.Fatalf("expected ErrPeriodReopenRequiresAudit for empty reason, got %v", err)
	}

	// Normal UpdateFiscalYearState must still reject backwards transition to Open
	if err := env.Driver.UpdateFiscalYearStateForCompanyContext(ctx, env.CompanyID, yearID, masterdata.FiscalYearOpen); !errors.Is(err, masterdata.ErrInvalidFiscalYearTransition) {
		t.Fatalf("expected ErrInvalidFiscalYearTransition for normal update to open, got %v", err)
	}

	// Audited reopen succeeds
	if err := env.Driver.ReopenFiscalYearForCompanyContext(ctx, env.CompanyID, yearID, actorID, "Authorized annual post-audit adjustments"); err != nil {
		t.Fatalf("ReopenFiscalYearForCompanyContext failed: %v", err)
	}

	// Verify state is now Open
	year, err := env.Driver.GetFiscalYearForCompany(ctx, env.CompanyID, yearID)
	if err != nil {
		t.Fatal(err)
	}
	if year.State != masterdata.FiscalYearOpen {
		t.Fatalf("expected FiscalYearOpen, got %s", year.State)
	}
}

func TestP406PostgresClosingChecklistEvaluator(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	period, err := env.Driver.GetPeriodByDateForCompany(ctx, env.CompanyID, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	year, err := env.Driver.GetFiscalYearForCompany(ctx, env.CompanyID, period.FiscalYearID)
	if err != nil {
		t.Fatal(err)
	}

	closingEngine := reporting.NewClosingEngine(env.Driver)

	// Since monthly periods are open in fresh test env, checklist must detect open periods blocker
	report, err := closingEngine.EvaluateClosingChecklist(ctx, year)
	if err != nil {
		t.Fatalf("EvaluateClosingChecklist: %v", err)
	}
	if report.CanClose {
		t.Fatalf("expected CanClose=false with open periods")
	}
	var foundPeriodBlocker bool
	for _, item := range report.Items {
		if item.Code == reporting.CheckCodeMonthlyPeriodsClosed && item.Status == reporting.ChecklistFailed {
			foundPeriodBlocker = true
			break
		}
	}
	if !foundPeriodBlocker {
		t.Fatalf("expected CheckCodeMonthlyPeriodsClosed blocker in report")
	}
}
