package valuation

import (
	"context"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

type ConversionEngine struct {
	provider RateProviderSPI
}

func NewConversionEngine(provider RateProviderSPI) *ConversionEngine {
	return &ConversionEngine{provider: provider}
}

func (e *ConversionEngine) Convert(ctx context.Context, amount financialtypes.Money, toCurrency string, date time.Time) (financialtypes.Money, error) {
	if e.provider == nil {
		return financialtypes.Money{}, ErrRateNotFound
	}
	if amount.Currency() == toCurrency {
		return amount, nil
	}
	rate, err := getTypedSpotRate(ctx, e.provider, amount.Currency(), toCurrency, date)
	if err != nil {
		return financialtypes.Money{}, err
	}

	return ConvertAtRate(amount, toCurrency, rate)
}

func getTypedSpotRate(ctx context.Context, provider RateProviderSPI, from, to string, date time.Time) (decimal.Decimal, error) {
	return provider.GetRateOfType(ctx, SpotRate, from, to, date)
}

func ConvertAtRate(amount financialtypes.Money, toCurrency string, rate decimal.Decimal) (financialtypes.Money, error) {
	if rate.IsZero() || rate.IsNegative() {
		return financialtypes.Money{}, ErrInvalidRate
	}
	return financialtypes.NewMoney(amount.Amount().Mul(rate), toCurrency)
}
