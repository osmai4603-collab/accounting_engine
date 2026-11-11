package valuation

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type RateProviderSPI interface {
	GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error)
	GetRateOfType(ctx context.Context, rateType RateType, from, to string, date time.Time) (decimal.Decimal, error)
}
