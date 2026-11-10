package memory

import (
	"context"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func (d *Driver) Register(rate valuation.ExchangeRate) error { return d.rates.Register(rate) }
func (d *Driver) AddRate(rate valuation.ExchangeRate) error  { return d.Register(rate) }
func (d *Driver) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	return d.rates.GetRate(ctx, from, to, date)
}

func (d *Driver) GetRateForCompany(ctx context.Context, companyID uuid.UUID, from, to string, date time.Time) (decimal.Decimal, error) {
	if companyID == uuid.Nil {
		return decimal.Zero, masterdata.ErrCompanyScopeRequired
	}
	return d.GetRate(ctx, from, to, date)
}

func (d *Driver) GetRateOfType(ctx context.Context, rateType valuation.RateType, from, to string, date time.Time) (decimal.Decimal, error) {
	return d.rates.GetRateOfType(ctx, rateType, from, to, date)
}

func (d *Driver) GetRateOfTypeForCompany(
	ctx context.Context,
	companyID uuid.UUID,
	rateType valuation.RateType,
	from, to string,
	date time.Time,
) (decimal.Decimal, error) {
	if companyID == uuid.Nil {
		return decimal.Zero, masterdata.ErrCompanyScopeRequired
	}
	return d.GetRateOfType(ctx, rateType, from, to, date)
}
