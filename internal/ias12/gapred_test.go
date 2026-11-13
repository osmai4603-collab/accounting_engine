//go:build gapred

// Red tests for GAP-STD-01 and GAP-STD-02 (IAS 12 deferred tax). Expected to
// pass after P3-T1/P3-T2 land.
package ias12_test

import (
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/ias12"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func gapUSD(t *testing.T, amount string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return money
}

// GAP-STD-01: a nil FutureTaxableProfitProbable currently means "recognize the
// DTA". IAS 12.24 requires probable future taxable profit; absent an explicit
// determination the engine must fail closed.
func TestGAP_STD_01_NilFutureProfitMustNotRecognizeDTA(t *testing.T) {
	measurement, err := ias12.Measure([]ias12.TaxBalance{{
		ID: "deductible", Kind: ias12.Asset,
		CarryingAmount: gapUSD(t, "80"), TaxBase: gapUSD(t, "100"),
		Rate: ias12.TaxRate{
			Value: decimal.RequireFromString("0.25"), Enacted: true,
			EnactedOn:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EffectiveFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}}, ias12.MeasurementPeriod{Kind: ias12.Annual, AsOf: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if !measurement.DTA.Amount().IsZero() {
		t.Fatalf("GAP-STD-01: DTA = %s recognized without an explicit probable-profit determination", measurement.DTA)
	}
}

// GAP-STD-02: an enacted rate whose EffectiveFrom is after the measurement date
// is the rate expected to apply on reversal; IAS 12.47 requires measuring at
// that expected rate instead of rejecting it.
func TestGAP_STD_02_EnactedFutureRateIsUsable(t *testing.T) {
	rate := ias12.TaxRate{
		Value: decimal.RequireFromString("0.20"), Enacted: true,
		EnactedOn:     time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		EffectiveFrom: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	asOf := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if err := rate.Validate(asOf); err != nil {
		t.Fatalf("GAP-STD-02: enacted future rate rejected: %v", err)
	}

	_, err := ias12.Measure([]ias12.TaxBalance{{
		ID: "taxable", Kind: ias12.Asset,
		CarryingAmount: gapUSD(t, "100"), TaxBase: gapUSD(t, "70"), Rate: rate,
	}}, ias12.MeasurementPeriod{Kind: ias12.Annual, AsOf: asOf})
	if err != nil {
		t.Fatalf("GAP-STD-02: measurement with enacted future rate failed: %v", err)
	}
}
