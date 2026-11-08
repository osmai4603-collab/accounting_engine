package masterdata

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrPeriodOutsideFiscalYear = errors.New("period dates must be within the fiscal year")

type PeriodState string

const (
	PeriodOpen             PeriodState = "open"
	PeriodSoftClosed       PeriodState = "soft_closed"
	PeriodHardClosed       PeriodState = "hard_closed"
	FiscalPeriodOpen                   = PeriodOpen
	FiscalPeriodSoftClosed             = PeriodSoftClosed
	FiscalPeriodHardClosed             = PeriodHardClosed
)

type FiscalYear struct {
	ID        uuid.UUID
	Name      string
	StartDate time.Time
	EndDate   time.Time
	Periods   []FiscalPeriod
}

type FiscalPeriod struct {
	ID           uuid.UUID
	FiscalYearID uuid.UUID
	Number       int
	Name         string
	StartDate    time.Time
	EndDate      time.Time
	State        PeriodState
}

func (p FiscalPeriod) Contains(date time.Time) bool {
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
	}
	for i := 1; i <= 12; i++ {
		periodStart := start.AddDate(0, i-1, 0)
		periodEnd := start.AddDate(0, i, 0).Add(-time.Nanosecond)
		year.Periods = append(year.Periods, FiscalPeriod{
			ID: uuid.New(), FiscalYearID: id, Number: i, Name: periodStart.Format("January 2006"),
			StartDate: periodStart, EndDate: periodEnd, State: PeriodOpen,
		})
	}
	// Adjustment periods follow all regular monthly periods.
	year.Periods = append(year.Periods,
		FiscalPeriod{ID: uuid.New(), FiscalYearID: id, Number: 13, Name: "Period 13", StartDate: year.EndDate.Add(-time.Hour), EndDate: year.EndDate, State: PeriodOpen},
		FiscalPeriod{ID: uuid.New(), FiscalYearID: id, Number: 14, Name: "Period 14", StartDate: year.EndDate.Add(-time.Hour), EndDate: year.EndDate, State: PeriodOpen},
	)
	return year
}

func ValidatePeriodTransition(from, to PeriodState) error {
	switch {
	case from == PeriodOpen && to == PeriodSoftClosed:
	case from == PeriodSoftClosed && to == PeriodHardClosed:
	default:
		return errors.New("invalid fiscal period transition")
	}
	return nil
}

func (s PeriodState) IsOpen() bool { return s == PeriodOpen }
