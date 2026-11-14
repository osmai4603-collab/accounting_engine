package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	"github.com/shopspring/decimal"
)

func TestReconciliationExceptionLifecyclePersistsActorAndReason(t *testing.T) {
	ctx := context.Background()
	driver := New()
	companyID, accountID, actorID := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	exception := subledgers.ReconciliationException{
		ID: uuid.New(), CompanyID: companyID, Domain: "AR", ControlAccountID: accountID,
		AsOfDate: now, FunctionalCurr: "USD",
		SubledgerBalance: decimal.NewFromInt(120), GLBalance: decimal.NewFromInt(100),
		Difference: decimal.NewFromInt(20), Status: subledgers.ReconciliationExceptionUnmatched,
		ActorID: actorID, Reason: "Variance found during daily check.",
		CreatedAt: now, UpdatedAt: now,
	}

	if err := driver.CreateReconciliationException(ctx, exception); err != nil {
		t.Fatal(err)
	}

	if err := driver.TransitionReconciliationException(ctx, companyID, exception.ID,
		subledgers.ReconciliationExceptionUnmatched, subledgers.ReconciliationExceptionMatching,
		actorID, "Investigating source documents.", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := driver.TransitionReconciliationException(ctx, companyID, exception.ID,
		subledgers.ReconciliationExceptionUnmatched, subledgers.ReconciliationExceptionDisputed,
		actorID, "Stale transition.", now.Add(2*time.Minute)); !errors.Is(err, subledgers.ErrReconciliationExceptionConflict) {
		t.Fatalf("stale transition error = %v, want status conflict", err)
	}

	stored, err := driver.GetReconciliationException(ctx, companyID, exception.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != subledgers.ReconciliationExceptionMatching ||
		stored.ActorID != actorID || stored.Reason != "Investigating source documents." {
		t.Fatalf("stored exception lost transition details: %+v", stored)
	}
	history, err := driver.ListReconciliationExceptionTransitions(ctx, companyID, exception.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[1].FromStatus != subledgers.ReconciliationExceptionUnmatched ||
		history[1].ToStatus != subledgers.ReconciliationExceptionMatching ||
		history[1].ActorID != actorID || history[1].Reason != stored.Reason {
		t.Fatalf("unexpected exception history: %+v", history)
	}

	listed, err := driver.ListReconciliationExceptions(ctx, companyID)
	if err != nil || len(listed) != 1 || listed[0].ID != exception.ID {
		t.Fatalf("listed exceptions = %+v, err=%v", listed, err)
	}
	if _, err := driver.GetReconciliationException(ctx, uuid.New(), exception.ID); !errors.Is(err, subledgers.ErrReconciliationExceptionNotFound) {
		t.Fatalf("cross-company read error = %v, want not found", err)
	}
}

func TestSavingDiscrepancyReportCreatesItsException(t *testing.T) {
	ctx := context.Background()
	driver := New()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	report := subledgers.DiscrepancyReport{
		CompanyID: uuid.New(), Domain: "AP", ControlAccountID: uuid.New(),
		AsOfDate:       time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC),
		FunctionalCurr: "USD", SubledgerBalance: decimal.NewFromInt(80),
		GLBalance: decimal.NewFromInt(100), Difference: decimal.NewFromInt(-20),
		DiscoveredAt: now,
	}
	if err := driver.SaveReconciliationReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	driver.mu.RLock()
	defer driver.mu.RUnlock()
	if len(driver.reconciliationReports) != 1 || len(driver.reconciliationExceptions) != 1 {
		t.Fatalf("report count=%d exception count=%d, want one of each",
			len(driver.reconciliationReports), len(driver.reconciliationExceptions))
	}
	for _, exception := range driver.reconciliationExceptions {
		if exception.CompanyID != report.CompanyID || exception.Difference.Cmp(report.Difference) != 0 ||
			exception.Status != subledgers.ReconciliationExceptionUnmatched {
			t.Fatalf("report exception does not match discrepancy: %+v", exception)
		}
	}
}
