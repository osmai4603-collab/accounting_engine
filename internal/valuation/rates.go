package valuation

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

var (
	ErrRateNotFound    = errors.New("exchange rate not found")
	ErrInvalidRate     = errors.New("invalid exchange rate")
	ErrInvalidCurrency = errors.New("invalid exchange-rate currency")
)

type RateType string

const (
	SpotRate       RateType = "SPOT"
	ClosingRate    RateType = "CLOSING"
	AverageRate    RateType = "AVERAGE"
	HistoricalRate RateType = "HISTORICAL"
)

type ExchangeRate struct {
	FromCurrency  string
	ToCurrency    string
	Rate          decimal.Decimal
	EffectiveDate time.Time
	Type          RateType
}

func (r ExchangeRate) Validate() error {
	from := strings.ToUpper(strings.TrimSpace(r.FromCurrency))
	to := strings.ToUpper(strings.TrimSpace(r.ToCurrency))
	if _, ok := financialtypes.LookupCurrency(from); !ok {
		return ErrInvalidCurrency
	}
	if _, ok := financialtypes.LookupCurrency(to); !ok {
		return ErrInvalidCurrency
	}
	if r.Rate.IsZero() || r.Rate.IsNegative() {
		return ErrInvalidRate
	}
	switch r.Type {
	case SpotRate, ClosingRate, AverageRate, HistoricalRate:
	default:
		return ErrInvalidRate
	}
	return nil
}

type RateRegistry struct {
	mu    sync.RWMutex
	rates []ExchangeRate
}

func NewRateRegistry() *RateRegistry {
	return &RateRegistry{}
}

func (r *RateRegistry) AddRate(rate ExchangeRate) error {
	return r.Register(rate)
}

func (r *RateRegistry) Register(rate ExchangeRate) error {
	if err := rate.Validate(); err != nil {
		return err
	}
	rate.FromCurrency = strings.ToUpper(strings.TrimSpace(rate.FromCurrency))
	rate.ToCurrency = strings.ToUpper(strings.TrimSpace(rate.ToCurrency))
	rate.EffectiveDate = dateOnly(rate.EffectiveDate)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, current := range r.rates {
		if sameRateKey(current, rate) {
			r.rates[i] = rate
			return nil
		}
	}
	r.rates = append(r.rates, rate)
	return nil
}

func (r *RateRegistry) GetRate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	if err := ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	from = strings.ToUpper(strings.TrimSpace(from))
	to = strings.ToUpper(strings.TrimSpace(to))
	if _, ok := financialtypes.LookupCurrency(from); !ok {
		return decimal.Zero, ErrInvalidCurrency
	}
	if _, ok := financialtypes.LookupCurrency(to); !ok {
		return decimal.Zero, ErrInvalidCurrency
	}
	if from == to {
		return decimal.NewFromInt(1), nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	targetDate := dateOnly(date)
	var candidates []ExchangeRate
	for _, rate := range r.rates {
		if rate.EffectiveDate.After(targetDate) {
			continue
		}
		if rate.FromCurrency == from && rate.ToCurrency == to {
			candidates = append(candidates, rate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].EffectiveDate.After(candidates[j].EffectiveDate)
	})
	if len(candidates) > 0 {
		return candidates[0].Rate, nil
	}

	candidates = candidates[:0]
	for _, rate := range r.rates {
		if !rate.EffectiveDate.After(targetDate) && rate.FromCurrency == to && rate.ToCurrency == from {
			candidates = append(candidates, rate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].EffectiveDate.After(candidates[j].EffectiveDate)
	})
	if len(candidates) > 0 && !candidates[0].Rate.IsZero() {
		return decimal.NewFromInt(1).DivRound(candidates[0].Rate, financialtypes.InternalScale), nil
	}
	return decimal.Zero, ErrRateNotFound
}

func (r *RateRegistry) GetRateOfType(ctx context.Context, rateType RateType, from, to string, date time.Time) (decimal.Decimal, error) {
	if err := ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	switch rateType {
	case SpotRate, ClosingRate, AverageRate, HistoricalRate:
	default:
		return decimal.Zero, ErrInvalidRate
	}
	from = strings.ToUpper(strings.TrimSpace(from))
	to = strings.ToUpper(strings.TrimSpace(to))
	if _, ok := financialtypes.LookupCurrency(from); !ok {
		return decimal.Zero, ErrInvalidCurrency
	}
	if _, ok := financialtypes.LookupCurrency(to); !ok {
		return decimal.Zero, ErrInvalidCurrency
	}
	if from == to {
		return decimal.NewFromInt(1), nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	targetDate := dateOnly(date)
	var direct, inverse []ExchangeRate
	for _, rate := range r.rates {
		if rate.Type != rateType || rate.EffectiveDate.After(targetDate) {
			continue
		}
		if rate.FromCurrency == from && rate.ToCurrency == to {
			direct = append(direct, rate)
		}
		if rate.FromCurrency == to && rate.ToCurrency == from {
			inverse = append(inverse, rate)
		}
	}
	sort.Slice(direct, func(i, j int) bool { return direct[i].EffectiveDate.After(direct[j].EffectiveDate) })
	if len(direct) > 0 {
		return direct[0].Rate, nil
	}
	sort.Slice(inverse, func(i, j int) bool { return inverse[i].EffectiveDate.After(inverse[j].EffectiveDate) })
	if len(inverse) > 0 && !inverse[0].Rate.IsZero() {
		return decimal.NewFromInt(1).DivRound(inverse[0].Rate, financialtypes.InternalScale), nil
	}
	return decimal.Zero, ErrRateNotFound
}

func (r *RateRegistry) GetRates() []ExchangeRate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rates := append([]ExchangeRate(nil), r.rates...)
	sort.Slice(rates, func(i, j int) bool {
		return rates[i].EffectiveDate.After(rates[j].EffectiveDate)
	})
	return rates
}

func sameRateKey(a, b ExchangeRate) bool {
	return a.FromCurrency == b.FromCurrency &&
		a.ToCurrency == b.ToCurrency &&
		a.Type == b.Type &&
		a.EffectiveDate.Equal(b.EffectiveDate)
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
