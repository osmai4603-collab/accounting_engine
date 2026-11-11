package valuation

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRateRegistryPreservesExchangeRatePrecision(t *testing.T) {
	registry := NewRateRegistry()
	date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rate := decimal.RequireFromString("3.7512846")
	if err := registry.Register(ExchangeRate{
		FromCurrency: "SAR", ToCurrency: "USD", Rate: rate,
		EffectiveDate: date, Type: SpotRate,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := registry.GetRateOfType(context.Background(), SpotRate, "SAR", "USD", date)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(rate) {
		t.Fatalf("GetRateOfType() = %s, want %s", got, rate)
	}
}

func TestRateRegistryConcurrentRegisterAndRead(t *testing.T) {
	registry := NewRateRegistry()
	date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var workers sync.WaitGroup
	errs := make(chan error, 100)
	for worker := range 100 {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			if worker%2 == 0 {
				errs <- registry.Register(ExchangeRate{
					FromCurrency: "SAR", ToCurrency: "USD",
					Rate: decimal.NewFromInt(int64(worker + 1)), EffectiveDate: date,
					Type: []RateType{SpotRate, ClosingRate, AverageRate, HistoricalRate}[worker%4],
				})
				return
			}
			for range 10 {
				if _, err := registry.GetRate(context.Background(), "SAR", "USD", date); err != nil && err != ErrRateNotFound {
					errs <- fmt.Errorf("read untyped rate: %w", err)
					return
				}
				if _, err := registry.GetRateOfType(context.Background(), SpotRate, "SAR", "USD", date); err != nil && err != ErrRateNotFound {
					errs <- fmt.Errorf("read typed rate: %w", err)
					return
				}
				_ = registry.GetRates()
			}
		}(worker)
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
