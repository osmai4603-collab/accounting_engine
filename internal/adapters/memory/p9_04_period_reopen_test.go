package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
)

func TestP904PeriodReopenTransitionsAndConcurrencyInMemory(t *testing.T) {
	driver, service, _, companyID, _, _, _ := setupMemorySubledgerEnv(t)
	ctx := context.Background()
	actorID, approvalID := uuid.New(), uuid.New()
	date := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	period, err := driver.GetPeriodByDateForCompany(ctx, companyID, date)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.TransitionPeriodStateContext(ctx, companyID, period.ID,
		masterdata.PeriodSoftClosed, actorID, "month-end close"); err != nil {
		t.Fatal(err)
	}
	if err := service.ReopenPeriodWithApprovalContext(ctx, companyID, period.ID,
		actorID, approvalID, "approved correction"); err != nil {
		t.Fatalf("reopen from soft-closed: %v", err)
	}
	period, err = driver.GetPeriodByDateForCompany(ctx, companyID, date)
	if err != nil {
		t.Fatal(err)
	}
	if period.State != masterdata.PeriodOpen || period.LastStateActorID != actorID ||
		period.LastStateReason != "approved correction" || period.LastStateAt.IsZero() ||
		period.LastStateApprovalID != approvalID {
		t.Fatalf("reopen metadata not persisted: %+v", period)
	}

	for _, state := range []masterdata.PeriodState{masterdata.PeriodSoftClosed, masterdata.PeriodHardClosed} {
		if err := service.TransitionPeriodStateContext(ctx, companyID, period.ID,
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
			errs <- service.ReopenPeriodWithApprovalContext(ctx, companyID, period.ID,
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

	for _, state := range []masterdata.PeriodState{masterdata.PeriodSoftClosed, masterdata.PeriodHardClosed} {
		if err := service.TransitionPeriodStateContext(ctx, companyID, period.ID,
			state, actorID, "annual close preparation"); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []masterdata.FiscalYearState{masterdata.FiscalYearSoftClosed, masterdata.FiscalYearHardClosed} {
		if err := driver.UpdateFiscalYearStateForCompanyContext(ctx, companyID, period.FiscalYearID, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.ReopenPeriodWithApprovalContext(ctx, companyID, period.ID,
		actorID, uuid.New(), "approved correction"); !errors.Is(err, masterdata.ErrPeriodCoveredByAnnualClose) {
		t.Fatalf("annual-close reopen error = %v, want annual-close safeguard", err)
	}
}
