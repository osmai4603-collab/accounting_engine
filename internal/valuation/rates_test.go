package valuation

import (
	"context"
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
