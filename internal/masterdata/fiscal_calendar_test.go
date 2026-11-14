package masterdata

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAdjustmentPeriodsAreProceduralBuckets(t *testing.T) {
	year := GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	period12, period13, period14 := year.Periods[11], year.Periods[12], year.Periods[13]
	if period12.Type != FiscalPeriodTypeNormal ||
		period13.Type != FiscalPeriodTypeYearEndAdjustment ||
		period14.Type != FiscalPeriodTypeAuditAdjustment {
		t.Fatalf("generated period types = %q, %q, %q", period12.Type, period13.Type, period14.Type)
	}
	if !period12.EndDate.Equal(period13.StartDate) || !period13.StartDate.Equal(period14.StartDate) {
		t.Fatalf("adjustment periods do not share the fiscal year-end boundary")
	}
	if period13.Contains(year.EndDate) || period14.Contains(year.EndDate) {
		t.Fatal("adjustment period must not claim calendar-date membership")
	}
	if !period12.Contains(time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("regular period 12 must continue to contain its calendar dates")
	}
}

func TestFiscalPeriodTransitionsRequireExplicitAuditedReopen(t *testing.T) {
	for _, transition := range [][2]PeriodState{
		{PeriodOpen, PeriodSoftClosed},
		{PeriodSoftClosed, PeriodHardClosed},
	} {
		if err := ValidatePeriodTransition(transition[0], transition[1]); err != nil {
			t.Errorf("valid transition %q -> %q rejected: %v", transition[0], transition[1], err)
		}
	}
	for _, transition := range [][2]PeriodState{
		{PeriodOpen, PeriodHardClosed},
		{PeriodSoftClosed, PeriodOpen},
		{PeriodHardClosed, PeriodOpen},
		{PeriodHardClosed, PeriodSoftClosed},
	} {
		if err := ValidatePeriodTransition(transition[0], transition[1]); !errors.Is(err, ErrInvalidPeriodTransition) {
			t.Errorf("transition %q -> %q error = %v, want invalid transition", transition[0], transition[1], err)
		}
	}
	if err := ValidatePeriodReopen(PeriodHardClosed, PeriodOpen, uuid.Nil, ""); !errors.Is(err, ErrPeriodReopenRequiresAudit) {
		t.Fatalf("reopen without actor/reason error = %v", err)
	}
	if err := ValidatePeriodReopen(PeriodHardClosed, PeriodOpen, uuid.New(), "approved correction"); err != nil {
		t.Fatalf("audited reopen rejected: %v", err)
	}
	if err := ValidatePeriodReopen(PeriodOpen, PeriodOpen, uuid.New(), "approved correction"); !errors.Is(err, ErrInvalidPeriodTransition) {
		t.Fatalf("reopening an already open period error = %v", err)
	}
}
