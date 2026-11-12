package storage

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type ExchangeRateSPI interface {
	GetRate(ctx context.Context, fromCurrency, toCurrency string, date time.Time) (decimal.Decimal, error)
}
