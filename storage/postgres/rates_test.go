package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/valuation"
	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestPostgresExchangeRatesAreCompanyScoped(t *testing.T) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL exchange-rate test: unable to connect (%v)", err)
	}
	defer driver.Close()

	companyIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for _, companyID := range companyIDs {
		if err := driver.CreateCompany(masterdata.Company{
			ID: companyID, Code: fmt.Sprintf("RATE-%s", companyID.String()[:8]),
			LegalName: "Exchange Rate Test", FunctionalCurrency: "SAR",
			PresentationCurrency: "USD", Active: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	date := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for i, companyID := range companyIDs {
		value := decimal.RequireFromString("0.20")
		if i == 1 {
			value = decimal.RequireFromString("0.25")
		}
		if err := driver.UpsertExchangeRate(ctx, companyID, valuation.ExchangeRate{
			FromCurrency: "SAR", ToCurrency: "USD", Rate: value,
			EffectiveDate: date, Type: valuation.ClosingRate,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, companyID := range companyIDs {
		want := decimal.RequireFromString("0.20")
		if i == 1 {
			want = decimal.RequireFromString("0.25")
		}
		got, err := driver.GetRateOfTypeForCompany(ctx, companyID, valuation.ClosingRate, "SAR", "USD", date)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(want) {
			t.Errorf("company %s rate = %s, want %s", companyID, got, want)
		}
	}
	inverse, err := driver.GetRateOfTypeForCompany(
		ctx, companyIDs[0], valuation.ClosingRate, "USD", "SAR", date,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !inverse.Equal(decimal.NewFromInt(1).DivRound(decimal.RequireFromString("0.20"), 12)) {
		t.Fatalf("inverse company rate = %s", inverse)
	}
}
