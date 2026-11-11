package ias12

import (
	"errors"
	"testing"
	"time"

	financialtypes "accounting_engine/internal/types"

	"github.com/shopspring/decimal"
)

func testMoney(t *testing.T, value, currency string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(value, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func testRate(value string) TaxRate {
	return TaxRate{
		Value: decimal.RequireFromString(value), Enacted: true,
		EnactedOn:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EffectiveFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func testPeriod(kind PeriodKind) MeasurementPeriod {
	return MeasurementPeriod{Kind: kind, AsOf: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
}

func TestMeasureDTAAndDTLSignsAndRates(t *testing.T) {
	measurement, err := Measure([]TaxBalance{
		{ID: "tax-depreciation", Kind: Asset, CarryingAmount: testMoney(t, "100", "USD"), TaxBase: testMoney(t, "70", "USD"), Rate: testRate("0.25")},
		{ID: "ecl-provision", Kind: Asset, CarryingAmount: testMoney(t, "60", "USD"), TaxBase: testMoney(t, "90", "USD"), Rate: testRate("0.20")},
		{ID: "accrual", Kind: Liability, CarryingAmount: testMoney(t, "40", "USD"), TaxBase: testMoney(t, "10", "USD"), Rate: testRate("0.30")},
		{ID: "prepaid-liability", Kind: Liability, CarryingAmount: testMoney(t, "10", "USD"), TaxBase: testMoney(t, "30", "USD"), Rate: testRate("0.10")},
	}, testPeriod(Quarterly))
	if err != nil {
		t.Fatal(err)
	}
	if got := measurement.Balances[0].DTL.Amount().String(); got != "7.5" {
		t.Fatalf("asset DTL = %s, want 7.5", got)
	}
	if got := measurement.Balances[1].DTA.Amount().String(); got != "6" {
		t.Fatalf("asset DTA = %s, want 6", got)
	}
	if got := measurement.Balances[2].DTA.Amount().String(); got != "9" {
		t.Fatalf("liability DTA = %s, want 9", got)
	}
	if got := measurement.Balances[3].DTL.Amount().String(); got != "2" {
		t.Fatalf("liability DTL = %s, want 2", got)
	}
	if got := measurement.DTA.Amount().String(); got != "15" {
		t.Fatalf("total DTA = %s, want 15", got)
	}
	if got := measurement.DTL.Amount().String(); got != "9.5" {
		t.Fatalf("total DTL = %s, want 9.5", got)
	}
	if measurement.Period.Kind != Quarterly {
		t.Fatalf("measurement period = %q, want quarterly", measurement.Period.Kind)
	}
}

func TestMeasureAnnualAndZeroRate(t *testing.T) {
	rate := testRate("0")
	measurement, err := Measure([]TaxBalance{{
		ID: "zero-rated", Kind: Asset, CarryingAmount: testMoney(t, "12", "EUR"),
		TaxBase: testMoney(t, "10", "EUR"), Rate: rate,
	}}, testPeriod(Annual))
	if err != nil {
		t.Fatal(err)
	}

	if !measurement.DTL.Amount().IsZero() || measurement.Period.Kind != Annual {
		t.Fatalf("unexpected annual measurement: %+v", measurement)
	}
}

func TestMeasureSuppressesDTAWhenFutureProfitIsNotProbable(t *testing.T) {
	probable := false
	measurement, err := Measure([]TaxBalance{{
		ID: "deductible", Kind: Asset,
		CarryingAmount: testMoney(t, "80", "USD"), TaxBase: testMoney(t, "100", "USD"),
		Rate: testRate("0.25"), FutureTaxableProfitProbable: &probable,
	}}, testPeriod(Annual))
	if err != nil {
		t.Fatal(err)
	}
	if !measurement.Balances[0].DeductibleTemporaryDifference.Amount().Equal(decimal.NewFromInt(20)) ||
		!measurement.DTA.Amount().IsZero() {
		t.Fatalf("unexpected DTA recognition result: %+v", measurement)
	}
}

func TestMeasureRejectsInvalidBalancesRatesAndPeriods(t *testing.T) {
	valid := TaxBalance{
		ID: "asset", Kind: Asset, CarryingAmount: testMoney(t, "10", "USD"),
		TaxBase: testMoney(t, "9", "USD"), Rate: testRate("0.2"),
	}
	tests := []struct {
		name   string
		rows   []TaxBalance
		period MeasurementPeriod
		want   error
	}{
		{"empty balances", nil, testPeriod(Quarterly), ErrNoBalances},
		{"unsupported period", []TaxBalance{valid}, MeasurementPeriod{Kind: "monthly", AsOf: testPeriod(Quarterly).AsOf}, ErrInvalidMeasurementPeriod},
		{"negative carrying amount", []TaxBalance{{ID: "bad", Kind: Asset, CarryingAmount: testMoney(t, "-1", "USD"), TaxBase: testMoney(t, "0", "USD"), Rate: testRate("0.2")}}, testPeriod(Quarterly), financialtypes.ErrNegativeAmountProhibited},
		{"mixed currencies", []TaxBalance{valid, {ID: "foreign", Kind: Asset, CarryingAmount: testMoney(t, "1", "EUR"), TaxBase: testMoney(t, "1", "EUR"), Rate: testRate("0.2")}}, testPeriod(Quarterly), ErrCurrencyMismatch},
		{"invalid rate", []TaxBalance{{ID: "rate", Kind: Asset, CarryingAmount: testMoney(t, "1", "USD"), TaxBase: testMoney(t, "0", "USD"), Rate: testRate("1.01")}}, testPeriod(Quarterly), ErrInvalidRate},
		{"unenacted rate", []TaxBalance{{ID: "rate", Kind: Asset, CarryingAmount: testMoney(t, "1", "USD"), TaxBase: testMoney(t, "0", "USD"), Rate: TaxRate{Value: decimal.RequireFromString("0.2")}}}, testPeriod(Quarterly), ErrRateNotEnacted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Measure(tt.rows, tt.period)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Measure() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestReconcilePermanentDifferencesAndEffectiveRate(t *testing.T) {
	report, err := Reconcile(TaxReconciliationInput{
		Period:                    testPeriod(Annual),
		AccountingProfitBeforeTax: testMoney(t, "1000", "USD"),
		StatutoryRate:             testRate("0.20"),
		PermanentDifferences: []PermanentDifference{
			{Description: "nondeductible penalty", Amount: testMoney(t, "50", "USD")},
			{Description: "exempt income", Amount: testMoney(t, "-20", "USD")},
		},
		CurrentTaxExpense:  testMoney(t, "215", "USD"),
		DeferredTaxExpense: testMoney(t, "30", "USD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for label, gotWant := range map[string][2]string{
		"expected":          {report.ExpectedTaxExpense.Amount().String(), "200"},
		"permanent tax":     {report.PermanentDifferencesTax.Amount().String(), "6"},
		"deferred movement": {report.TemporaryDifferencesChange.Amount().String(), "30"},
		"effective expense": {report.EffectiveTaxExpense.Amount().String(), "245"},
		"effective rate":    {report.EffectiveTaxRate.String(), "0.245"},
	} {
		if gotWant[0] != gotWant[1] {
			t.Errorf("%s = %s, want %s", label, gotWant[0], gotWant[1])
		}
	}
	if !report.EffectiveTaxRateAvailable {
		t.Fatal("effective tax rate should be available")
	}
}

func TestReconcileZeroAndNegativeProfitRateCases(t *testing.T) {
	zeroReport, err := Reconcile(TaxReconciliationInput{
		Period: testPeriod(Quarterly), AccountingProfitBeforeTax: testMoney(t, "0", "USD"),
		StatutoryRate: testRate("0.2"), CurrentTaxExpense: testMoney(t, "5", "USD"),
		DeferredTaxExpense: testMoney(t, "-2", "USD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if zeroReport.EffectiveTaxRateAvailable || !zeroReport.EffectiveTaxRate.IsZero() {
		t.Fatalf("zero-profit effective rate should be unavailable, got %s", zeroReport.EffectiveTaxRate)
	}
	lossReport, err := Reconcile(TaxReconciliationInput{
		Period: testPeriod(Quarterly), AccountingProfitBeforeTax: testMoney(t, "-100", "USD"),
		StatutoryRate: testRate("0.2"), CurrentTaxExpense: testMoney(t, "10", "USD"),
		DeferredTaxExpense: testMoney(t, "0", "USD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !lossReport.EffectiveTaxRateAvailable || lossReport.EffectiveTaxRate.String() != "-0.1" {
		t.Fatalf("loss effective rate = %s, available=%v", lossReport.EffectiveTaxRate, lossReport.EffectiveTaxRateAvailable)
	}
}

func TestReconcileRejectsMixedCurrencyAndUnenactedRate(t *testing.T) {
	base := TaxReconciliationInput{
		Period: testPeriod(Quarterly), AccountingProfitBeforeTax: testMoney(t, "10", "USD"),
		StatutoryRate: testRate("0.2"), CurrentTaxExpense: testMoney(t, "1", "USD"),
		DeferredTaxExpense: testMoney(t, "0", "USD"),
	}
	mixed := base
	mixed.PermanentDifferences = []PermanentDifference{{Description: "foreign", Amount: testMoney(t, "1", "EUR")}}
	if _, err := Reconcile(mixed); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("mixed currency error = %v, want %v", err, ErrCurrencyMismatch)
	}
	base.StatutoryRate.Enacted = false
	if _, err := Reconcile(base); !errors.Is(err, ErrRateNotEnacted) {
		t.Fatalf("unenacted rate error = %v, want %v", err, ErrRateNotEnacted)
	}
}
