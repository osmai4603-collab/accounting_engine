package ledger

import (
	"time"

	"accounting_engine/pkg/api/v1/types"
)

type PeriodState string

const (
	PeriodOpen       PeriodState = "open"
	PeriodSoftClosed PeriodState = "soft_closed"
	PeriodHardClosed PeriodState = "hard_closed"
)

type FiscalYearState string

const (
	FiscalYearOpen       FiscalYearState = "open"
	FiscalYearSoftClosed FiscalYearState = "soft_closed"
	FiscalYearHardClosed FiscalYearState = "hard_closed"
)

type FiscalPeriodType string

const (
	FiscalPeriodTypeNormal            FiscalPeriodType = "normal"
	FiscalPeriodTypeYearEndAdjustment FiscalPeriodType = "year_end_adjustment"
	FiscalPeriodTypeAuditAdjustment   FiscalPeriodType = "audit_adjustment"
)

type FiscalYear struct {
	ID        types.CompanyID // or ID
	CompanyID types.CompanyID
	Name      string
	StartDate time.Time
	EndDate   time.Time
	State     FiscalYearState
	Periods   []FiscalPeriod
}

type FiscalPeriod struct {
	ID           types.FiscalPeriodID
	CompanyID    types.CompanyID
	FiscalYearID types.CompanyID
	Number       int
	Name         string
	StartDate    time.Time
	EndDate      time.Time
	State        PeriodState
	Type         FiscalPeriodType
}

func (p FiscalPeriod) Contains(date time.Time) bool {
	if p.Type == FiscalPeriodTypeYearEndAdjustment || p.Type == FiscalPeriodTypeAuditAdjustment {
		return false
	}
	date = date.UTC()
	start := p.StartDate.UTC()
	end := p.EndDate.UTC()
	return !date.Before(start) && !date.After(end)
}
