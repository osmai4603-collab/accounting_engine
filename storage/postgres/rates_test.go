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
		if err := driver.CreateCompany(ctx, masterdata.Company{
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

func TestPostgresFiscalYearCloseStatePersists(t *testing.T) {
	ctx := context.Background()
	driver, err := postgres.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL fiscal-year test: unable to connect (%v)", err)
	}
	defer driver.Close()

	companyID := uuid.New()
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("FY-%s", companyID.String()[:8]),
		LegalName: "Fiscal Year Test", FunctionalCurrency: "SAR",
		PresentationCurrency: "SAR", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatal(err)
	}
	if err := driver.UpdateFiscalYearState(ctx, companyID, year.ID, masterdata.FiscalYearSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := driver.UpdateFiscalYearState(ctx, companyID, year.ID, masterdata.FiscalYearHardClosed); err != nil {
		t.Fatal(err)
	}
	stored, err := driver.GetFiscalYearForCompany(ctx, companyID, year.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != masterdata.FiscalYearHardClosed {
		t.Fatalf("stored fiscal-year state = %s, want hard closed", stored.State)
	}
	if err := driver.UpdateFiscalYearState(ctx, companyID, year.ID, masterdata.FiscalYearOpen); err != masterdata.ErrInvalidFiscalYearTransition {
		t.Fatalf("invalid fiscal-year transition error = %v", err)
	}
}
