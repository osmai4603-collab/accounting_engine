package masterdata

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPeriodOutsideFiscalYear     = errors.New("period dates must be within the fiscal year")
	ErrInvalidFiscalYearTransition = errors.New("invalid fiscal year transition")
	ErrInvalidPeriodTransition     = errors.New("invalid fiscal period transition")
	ErrPeriodTransitionNeedsAudit  = errors.New("fiscal period transition requires an actor and reason")
	ErrPeriodReopenRequiresAudit   = errors.New("reopening a fiscal period requires an actor and reason")
)

type PeriodState string

const (
	PeriodOpen             PeriodState = "open"
	PeriodSoftClosed       PeriodState = "soft_closed"
	PeriodHardClosed       PeriodState = "hard_closed"
	FiscalPeriodOpen                   = PeriodOpen
	FiscalPeriodSoftClosed             = PeriodSoftClosed
	FiscalPeriodHardClosed             = PeriodHardClosed
)

type FiscalYearState string

const (
	FiscalYearOpen       FiscalYearState = "open"
	FiscalYearSoftClosed FiscalYearState = "soft_closed"
	FiscalYearHardClosed FiscalYearState = "hard_closed"
)

type FiscalYear struct {
	CompanyID uuid.UUID
	ID        uuid.UUID
	Name      string
	StartDate time.Time
	EndDate   time.Time
	State     FiscalYearState
	Periods   []FiscalPeriod
}

type FiscalPeriod struct {
	CompanyID        uuid.UUID
	ID               uuid.UUID
	FiscalYearID     uuid.UUID
	Number           int
	Name             string
	StartDate        time.Time
	EndDate          time.Time
	State            PeriodState
	Type             FiscalPeriodType
	LastStateActorID uuid.UUID
	LastStateReason  string
	LastStateAt      time.Time
}

type FiscalPeriodType string

const (
	FiscalPeriodTypeNormal            FiscalPeriodType = "normal"
	FiscalPeriodTypeYearEndAdjustment FiscalPeriodType = "year_end_adjustment"
	FiscalPeriodTypeAuditAdjustment   FiscalPeriodType = "audit_adjustment"
	FiscalPeriodTypeAdjustment                         = FiscalPeriodTypeYearEndAdjustment
)

func (p FiscalPeriod) Contains(date time.Time) bool {
	if p.Type == FiscalPeriodTypeYearEndAdjustment || p.Type == FiscalPeriodTypeAuditAdjustment {
		return false
	}
	date = date.UTC()
	start := p.StartDate.UTC()
	end := p.EndDate.UTC()
	return !date.Before(start) && !date.After(end)
}

func GenerateFiscalYear(start time.Time) FiscalYear {
	start = start.UTC()
	id := uuid.New()
	year := FiscalYear{
		ID:        id,
		Name:      start.Format("2006"),
		StartDate: start,
		EndDate:   start.AddDate(1, 0, 0).Add(-time.Nanosecond),
		State:     FiscalYearOpen,
	}
	for i := 1; i <= 12; i++ {
		periodStart := start.AddDate(0, i-1, 0)
		periodEnd := start.AddDate(0, i, 0).Add(-time.Nanosecond)
		year.Periods = append(year.Periods, FiscalPeriod{
			ID: uuid.New(), FiscalYearID: id, Number: i, Name: periodStart.Format("January 2006"),
			StartDate: periodStart, EndDate: periodEnd, State: PeriodOpen, Type: FiscalPeriodTypeNormal,
		})
	}
	// Adjustment periods are procedural buckets on the year-end date, not
	// calendar ranges. They are selected by period number, never by date.
	year.Periods = append(year.Periods,
		FiscalPeriod{ID: uuid.New(), FiscalYearID: id, Number: 13, Name: "Period 13", StartDate: year.EndDate, EndDate: year.EndDate, State: PeriodOpen, Type: FiscalPeriodTypeYearEndAdjustment},
		FiscalPeriod{ID: uuid.New(), FiscalYearID: id, Number: 14, Name: "Period 14", StartDate: year.EndDate, EndDate: year.EndDate, State: PeriodOpen, Type: FiscalPeriodTypeAuditAdjustment},
	)
	return year
}

func ValidatePeriodTransition(from, to PeriodState) error {
	switch {
	case from == PeriodOpen && to == PeriodSoftClosed:
	case from == PeriodSoftClosed && to == PeriodHardClosed:
	default:
		return ErrInvalidPeriodTransition
	}
	return nil
}

// ValidatePeriodReopen validates the exceptional, audited transition back to
// open. Normal period transitions never reopen a closed period.
func ValidatePeriodReopen(from, to PeriodState, actorID uuid.UUID, reason string) error {
	if to != PeriodOpen || (from != PeriodSoftClosed && from != PeriodHardClosed) {
		return ErrInvalidPeriodTransition
	}
	if actorID == uuid.Nil || strings.TrimSpace(reason) == "" {
		return ErrPeriodReopenRequiresAudit
	}
	return nil
}

func ValidateFiscalYearTransition(from, to FiscalYearState) error {
	switch {
	case from == FiscalYearOpen && to == FiscalYearSoftClosed:
	case from == FiscalYearSoftClosed && to == FiscalYearHardClosed:
	default:
		return ErrInvalidFiscalYearTransition
	}
	return nil
}

func (s PeriodState) IsOpen() bool { return s == PeriodOpen }
