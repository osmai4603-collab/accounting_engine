package valuation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
	"github.com/shopspring/decimal"
)

func TestIAS21_MonetaryPayableLifecycleRevaluationAndSettlement(t *testing.T) {
	ctx := context.Background()
	registry := valuation.NewRateRegistry()

	t0 := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC) // Invoice date
	t1 := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC) // Q1 Close date
	t2 := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC) // Settlement date

	// Exchange rates: USD to SAR
	// Spot at invoice: 3.7500
	// Closing at Q1:  3.8000
	// Spot at settle: 3.8200
	if err := registry.Register(valuation.ExchangeRate{
		FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString("3.75"),
		EffectiveDate: t0, Type: valuation.SpotRate, Source: "SAMA_CENTRAL_BANK",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(valuation.ExchangeRate{
		FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString("3.80"),
		EffectiveDate: t1, Type: valuation.ClosingRate, Source: "SAMA_CENTRAL_BANK",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(valuation.ExchangeRate{
		FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString("3.82"),
		EffectiveDate: t2, Type: valuation.SpotRate, Source: "SAMA_CENTRAL_BANK",
	}); err != nil {
		t.Fatal(err)
	}

	companyID := uuid.New()
	apAccountID := uuid.New()
	bankAccountID := uuid.New()
	unrealizedGainID := uuid.New()
	unrealizedLossID := uuid.New()
	realizedGainID := uuid.New()
	realizedLossID := uuid.New()
	journalID := uuid.New()

	foreignAmt := decimal.RequireFromString("10000.00") // 10,000 USD
	origRate := decimal.RequireFromString("3.75")
	initialCarrying, err := financialtypes.NewMoney(foreignAmt.Mul(origRate), "SAR")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Period-end revaluation at t1 (IAS 21.23(a))
	revalEngine := valuation.NewRevaluationEngine(registry)
	position := valuation.MonetaryPosition{
		AccountID:              apAccountID,
		ForeignCurrencyBalance: foreignAmt,
		TransactionCurrency:    "USD",
		FunctionalCurrency:     "SAR",
		CarryingAmount:         initialCarrying,
		Type:                   valuation.Monetary,
		IsLiability:            true,
	}

	res, err := revalEngine.Calculate(ctx, position, t1)
	if err != nil {
		t.Fatalf("Calculate reval: %v", err)
	}

	// 10,000 * 3.80 = 38,000 SAR. Difference = 38,000 - 37,500 = +500 SAR (Liability increase -> Loss)
	if !res.ClosingRate.Equal(decimal.RequireFromString("3.80")) {
		t.Fatalf("expected closing rate 3.80, got %s", res.ClosingRate)
	}
	if !res.Difference.Amount().Equal(decimal.RequireFromString("500.00")) {
		t.Fatalf("expected diff 500.00, got %s", res.Difference.Amount())
	}

	// Generate unrealized revaluation entry
	revalEntry, err := valuation.GenerateRevaluationEntry(res, valuation.FXJournalAccounts{
		CompanyID:        companyID,
		JournalID:        journalID,
		UnrealizedGainID: unrealizedGainID,
		UnrealizedLossID: unrealizedLossID,
	}, t1)
	if err != nil {
		t.Fatalf("GenerateRevaluationEntry: %v", err)
	}

	if err := revalEntry.Validate(); err != nil {
		t.Fatalf("revalEntry validate: %v", err)
	}
	if len(revalEntry.Lines) != 2 {
		t.Fatalf("expected 2 lines in reval entry, got %d", len(revalEntry.Lines))
	}
	// For payable revaluation loss: debit unrealized loss, credit AP
	if revalEntry.Lines[0].AccountID != unrealizedLossID || !revalEntry.Lines[0].Debit.Amount().Equal(decimal.RequireFromString("500.00")) {
		t.Errorf("line 0 should debit unrealized loss 500, got %v", revalEntry.Lines[0])
	}
	if revalEntry.Lines[1].AccountID != apAccountID || !revalEntry.Lines[1].Credit.Amount().Equal(decimal.RequireFromString("500.00")) {
		t.Errorf("line 1 should credit AP 500, got %v", revalEntry.Lines[1])
	}

	// 2. Settlement at t2 (IAS 21.28)
	settleRate := decimal.RequireFromString("3.82")
	settleMove := valuation.RealizedFXSettlementMove{
		CompanyID:             companyID,
		JournalID:             journalID,
		MonetaryAccountID:     apAccountID,
		SettlementAccountID:   bankAccountID,
		RealizedGainAccountID: realizedGainID,
		RealizedLossAccountID: realizedLossID,
		ForeignAmount:         foreignAmt,
		TransactionCurrency:   "USD",
		FunctionalCurrency:    "SAR",
		OriginalRate:          origRate,
		SettlementRate:        settleRate,
		SettlementDate:        t2,
		IsReceivable:          false, // AP settlement
	}

	settleEntry, realizedDiff, err := valuation.GenerateRealizedFXSettlementEntry(settleMove)
	if err != nil {
		t.Fatalf("GenerateRealizedFXSettlementEntry: %v", err)
	}
	if err := settleEntry.Validate(); err != nil {
		t.Fatalf("settleEntry validate: %v", err)
	}

	// Realized difference = 10,000 * (3.82 - 3.75) = 700 SAR loss
	if !realizedDiff.Amount().Equal(decimal.RequireFromString("700.00")) {
		t.Fatalf("expected realized diff 700.00, got %s", realizedDiff.Amount())
	}

	// AP debited 37,500, Bank credited 38,200, Realized loss debited 700
	var apDebited, bankCredited, lossDebited decimal.Decimal
	for _, l := range settleEntry.Lines {
		if l.AccountID == apAccountID {
			apDebited = l.Debit.Amount()
		}
		if l.AccountID == bankAccountID {
			bankCredited = l.Credit.Amount()
		}
		if l.AccountID == realizedLossID {
			lossDebited = l.Debit.Amount()
		}
	}
	if !apDebited.Equal(decimal.RequireFromString("37500.00")) {
		t.Errorf("expected AP debit 37500, got %s", apDebited)
	}
	if !bankCredited.Equal(decimal.RequireFromString("38200.00")) {
		t.Errorf("expected bank credit 38200, got %s", bankCredited)
	}
	if !lossDebited.Equal(decimal.RequireFromString("700.00")) {
		t.Errorf("expected realized loss debit 700, got %s", lossDebited)
	}
}

func TestIAS21_MonetaryReceivableSettlementRealizedGain(t *testing.T) {
	companyID := uuid.New()
	arAccountID := uuid.New()
	bankAccountID := uuid.New()
	realizedGainID := uuid.New()
	realizedLossID := uuid.New()
	journalID := uuid.New()

	foreignAmt := decimal.RequireFromString("20000.00") // 20,000 EUR
	origRate := decimal.RequireFromString("4.10")       // 4.10 SAR
	settleRate := decimal.RequireFromString("4.25")     // 4.25 SAR

	settleMove := valuation.RealizedFXSettlementMove{
		CompanyID:             companyID,
		JournalID:             journalID,
		MonetaryAccountID:     arAccountID,
		SettlementAccountID:   bankAccountID,
		RealizedGainAccountID: realizedGainID,
		RealizedLossAccountID: realizedLossID,
		ForeignAmount:         foreignAmt,
		TransactionCurrency:   "EUR",
		FunctionalCurrency:    "SAR",
		OriginalRate:          origRate,
		SettlementRate:        settleRate,
		SettlementDate:        time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		IsReceivable:          true, // AR settlement
	}

	settleEntry, diff, err := valuation.GenerateRealizedFXSettlementEntry(settleMove)
	if err != nil {
		t.Fatalf("GenerateRealizedFXSettlementEntry: %v", err)
	}
	if err := settleEntry.Validate(); err != nil {
		t.Fatalf("settleEntry validate: %v", err)
	}

	// Realized gain = 20,000 * (4.25 - 4.10) = 3,000 SAR gain
	if !diff.Amount().Equal(decimal.RequireFromString("3000.00")) {
		t.Fatalf("expected realized gain 3000.00, got %s", diff.Amount())
	}

	// Bank debited 85,000, AR credited 82,000, Realized Gain credited 3,000
	var bankDebited, arCredited, gainCredited decimal.Decimal
	for _, l := range settleEntry.Lines {
		if l.AccountID == bankAccountID {
			bankDebited = l.Debit.Amount()
		}
		if l.AccountID == arAccountID {
			arCredited = l.Credit.Amount()
		}
		if l.AccountID == realizedGainID {
			gainCredited = l.Credit.Amount()
		}
	}
	if !bankDebited.Equal(decimal.RequireFromString("85000.00")) {
		t.Errorf("expected bank debit 85000, got %s", bankDebited)
	}
	if !arCredited.Equal(decimal.RequireFromString("82000.00")) {
		t.Errorf("expected AR credit 82000, got %s", arCredited)
	}
	if !gainCredited.Equal(decimal.RequireFromString("3000.00")) {
		t.Errorf("expected realized gain credit 3000, got %s", gainCredited)
	}
}

func TestIAS21_NonMonetaryItemsNotRevaluedAtClosingRate(t *testing.T) {
	ctx := context.Background()
	registry := valuation.NewRateRegistry()

	t0 := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

	_ = registry.Register(valuation.ExchangeRate{
		FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString("3.75"),
		EffectiveDate: t0, Type: valuation.SpotRate,
	})
	_ = registry.Register(valuation.ExchangeRate{
		FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString("4.00"),
		EffectiveDate: t1, Type: valuation.ClosingRate,
	})

	revalEngine := valuation.NewRevaluationEngine(registry)

	// Under IAS 21.23(b), non-monetary items measured at historical cost (equipment/inventory)
	// shall be translated using the exchange rate at the date of transaction and are not revalued at closing rates.
	equipmentCarrying, _ := financialtypes.NewMoneyFromString("37500.00", "SAR")
	positions := []valuation.MonetaryPosition{
		{
			AccountID:              uuid.New(),
			ForeignCurrencyBalance: decimal.RequireFromString("10000.00"),
			TransactionCurrency:    "USD",
			FunctionalCurrency:     "SAR",
			CarryingAmount:         equipmentCarrying,
			Type:                   valuation.NonMonetary,
		},
	}

	results, err := revalEngine.Revalue(ctx, positions, t1)
	if err != nil {
		t.Fatalf("Revalue failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for non-monetary items, got %d", len(results))
	}

	// Directly attempting Calculate with NonMonetary type returns ErrInvalidPosition
	_, err = revalEngine.Calculate(ctx, positions[0], t1)
	if err != valuation.ErrInvalidPosition {
		t.Fatalf("expected ErrInvalidPosition for NonMonetary Calculate, got %v", err)
	}
}

func TestIAS21_RateRegistryHistoricalReproducibilityAndProvenance(t *testing.T) {
	ctx := context.Background()
	registry := valuation.NewRateRegistry()

	d1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	_ = registry.Register(valuation.ExchangeRate{
		FromCurrency: "EUR", ToCurrency: "USD", Rate: decimal.RequireFromString("1.05"),
		EffectiveDate: d1, Type: valuation.ClosingRate, Source: "ECB_OFFICIAL",
	})
	_ = registry.Register(valuation.ExchangeRate{
		FromCurrency: "EUR", ToCurrency: "USD", Rate: decimal.RequireFromString("1.08"),
		EffectiveDate: d2, Type: valuation.ClosingRate, Source: "ECB_OFFICIAL",
	})
	_ = registry.Register(valuation.ExchangeRate{
		FromCurrency: "EUR", ToCurrency: "USD", Rate: decimal.RequireFromString("1.12"),
		EffectiveDate: d3, Type: valuation.ClosingRate, Source: "ECB_OFFICIAL",
	})

	// Query as of March 2026: should reproduce d1 rate (1.05)
	rec1, isInv1, eff1, err := registry.GetRateRecord(ctx, valuation.ClosingRate, "EUR", "USD", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetRateRecord March: %v", err)
	}
	if isInv1 || !eff1.Equal(decimal.RequireFromString("1.05")) || rec1.Source != "ECB_OFFICIAL" {
		t.Fatalf("March rate mismatch: got eff=%s, source=%s", eff1, rec1.Source)
	}

	// Query as of October 2026: should reproduce d2 rate (1.08)
	rec2, _, eff2, err := registry.GetRateRecord(ctx, valuation.ClosingRate, "EUR", "USD", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetRateRecord October: %v", err)
	}
	if !eff2.Equal(decimal.RequireFromString("1.08")) || rec2.Source != "ECB_OFFICIAL" {
		t.Fatalf("October rate mismatch: got eff=%s", eff2)
	}

	// Query inverse rate (USD to EUR) as of October 2026: should identify inverted quote
	_, isInv3, eff3, err := registry.GetRateRecord(ctx, valuation.ClosingRate, "USD", "EUR", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetRateRecord inverse: %v", err)
	}
	if !isInv3 {
		t.Fatalf("expected isInv3 to be true for USD -> EUR")
	}
	expectedInv := decimal.NewFromInt(1).DivRound(decimal.RequireFromString("1.08"), financialtypes.InternalScale)
	if !eff3.Equal(expectedInv) {
		t.Fatalf("inverse rate calculation mismatch: got %s, want %s", eff3, expectedInv)
	}
}
