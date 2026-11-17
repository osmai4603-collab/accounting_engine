package valuation

import (
	"context"
	"strings"
	"testing"
	"time"

	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type rateOnlyProvider struct {
	rate decimal.Decimal
}

func (p rateOnlyProvider) GetRate(context.Context, string, string, time.Time) (decimal.Decimal, error) {
	return p.rate, nil
}

func (p rateOnlyProvider) GetRateOfType(context.Context, RateType, string, string, time.Time) (decimal.Decimal, error) {
	return p.rate, nil
}

func testMoney(t *testing.T, value, currency string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(value, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func TestSpotConversion(t *testing.T) {
	registry := NewRateRegistry()
	date := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if err := registry.Register(ExchangeRate{FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString("3.7500"), EffectiveDate: date, Type: SpotRate}); err != nil {
		t.Fatal(err)
	}
	engine := NewConversionEngine(registry)
	converted, err := engine.Convert(context.Background(), testMoney(t, "100", "USD"), "SAR", date)
	if err != nil {
		t.Fatal(err)
	}
	if converted.Currency() != "SAR" || !converted.Amount().Equal(decimal.RequireFromString("375.0000")) {
		t.Fatalf("unexpected conversion: %s", converted.String())
	}
}

func TestConversionAndRevaluationUseTheirRequiredRateTypes(t *testing.T) {
	registry := NewRateRegistry()
	date := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	for rateType, rate := range map[RateType]string{
		SpotRate: "3.75", ClosingRate: "4.00", AverageRate: "3.50",
	} {
		if err := registry.Register(ExchangeRate{
			FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString(rate),
			EffectiveDate: date, Type: rateType,
		}); err != nil {
			t.Fatal(err)
		}
	}

	converted, err := NewConversionEngine(registry).Convert(
		context.Background(), testMoney(t, "100", "USD"), "SAR", date,
	)
	if err != nil || !converted.Amount().Equal(decimal.NewFromInt(375)) {
		t.Fatalf("spot conversion = %v, %v; want SAR 375", converted, err)
	}

	position := MonetaryPosition{
		AccountID: uuid.New(), ForeignCurrencyBalance: decimal.NewFromInt(100),
		TransactionCurrency: "USD", FunctionalCurrency: "SAR",
		CarryingAmount: testMoney(t, "350", "SAR"), Type: Monetary,
	}
	result, err := NewRevaluationEngine(registry).Calculate(context.Background(), position, date)
	if err != nil || !result.Difference.Amount().Equal(decimal.NewFromInt(50)) {
		t.Fatalf("closing-rate revaluation = %v, %v; want SAR 50", result.Difference, err)
	}
	average, err := registry.GetRateOfType(context.Background(), AverageRate, "USD", "SAR", date)
	if err != nil || !average.Equal(decimal.RequireFromString("3.50")) {
		t.Fatalf("average rate = %s, %v; want 3.50", average, err)
	}
}

func TestRevaluationPreservesInverseRateReference(t *testing.T) {
	registry := NewRateRegistry()
	date := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	if err := registry.Register(ExchangeRate{
		FromCurrency: "SAR", ToCurrency: "USD", Rate: decimal.RequireFromString("0.25"),
		EffectiveDate: date, Type: ClosingRate, Source: "CENTRAL_BANK",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := NewRevaluationEngine(registry).Calculate(context.Background(), MonetaryPosition{
		AccountID: uuid.New(), ForeignCurrencyBalance: decimal.NewFromInt(100),
		TransactionCurrency: "USD", FunctionalCurrency: "SAR",
		CarryingAmount: testMoney(t, "375", "SAR"), Type: Monetary,
	}, date)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RateInverted || result.RateReference.FromCurrency != "SAR" ||
		result.RateReference.ToCurrency != "USD" ||
		!result.ClosingRate.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("inverse closing-rate provenance was lost: %+v inverted=%t", result.RateReference, result.RateInverted)
	}
	entry, err := GenerateRevaluationEntry(result, FXJournalAccounts{
		JournalID: uuid.New(), UnrealizedGainID: uuid.New(), UnrealizedLossID: uuid.New(),
	}, date)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.DocumentReference, "inverted=true") ||
		!strings.Contains(entry.DocumentReference, "applied=4") {
		t.Fatalf("entry does not preserve applied inverse-rate reference: %q", entry.DocumentReference)
	}
}

func TestRevaluationDoesNotInventUnavailableRateProvenance(t *testing.T) {
	date := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	result, err := NewRevaluationEngine(rateOnlyProvider{rate: decimal.NewFromInt(4)}).Calculate(
		context.Background(),
		MonetaryPosition{
			AccountID: uuid.New(), ForeignCurrencyBalance: decimal.NewFromInt(100),
			TransactionCurrency: "USD", FunctionalCurrency: "SAR",
			CarryingAmount: testMoney(t, "375", "SAR"), Type: Monetary,
		},
		date,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RateReferenceAvailable || result.RateReference.Source != "" {
		t.Fatalf("provider without rate-record support reported provenance: %+v", result.RateReference)
	}
	entry, err := GenerateRevaluationEntry(result, FXJournalAccounts{
		JournalID: uuid.New(), UnrealizedGainID: uuid.New(), UnrealizedLossID: uuid.New(),
	}, date)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.DocumentReference, "provider_record=unavailable") ||
		strings.Contains(entry.DocumentReference, "effective=") {
		t.Fatalf("draft misrepresented missing provenance: %q", entry.DocumentReference)
	}
}

func TestRevaluationGainLossAndZero(t *testing.T) {
	registry := NewRateRegistry()
	date := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	for _, rate := range []string{"3.7500", "4.0000"} {
		if err := registry.Register(ExchangeRate{FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString(rate), EffectiveDate: date, Type: ClosingRate}); err != nil {
			t.Fatal(err)
		}
	}
	accountID := uuid.New()
	engine := NewRevaluationEngine(registry)
	position := MonetaryPosition{
		AccountID: accountID, ForeignCurrencyBalance: decimal.NewFromInt(100),
		TransactionCurrency: "USD", FunctionalCurrency: "SAR",
		CarryingAmount: testMoney(t, "375.0000", "SAR"), Type: Monetary,
	}

	result, err := engine.Calculate(context.Background(), position, date)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsGain || !result.Difference.Amount().Equal(decimal.NewFromInt(25)) {
		t.Fatalf("unexpected gain: %+v", result)
	}
	entry, err := GenerateRevaluationEntry(result, FXJournalAccounts{
		JournalID: uuid.New(), UnrealizedGainID: uuid.New(), UnrealizedLossID: uuid.New(),
	}, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Lines) != 2 || !entry.Lines[0].Debit.Amount().Equal(decimal.NewFromInt(25)) {
		t.Fatalf("unexpected gain entry: %+v", entry)
	}

	if err := registry.Register(ExchangeRate{FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.RequireFromString("3.7500"), EffectiveDate: date, Type: ClosingRate}); err != nil {
		t.Fatal(err)
	}
	result, err = engine.Calculate(context.Background(), position, date)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Difference.Amount().IsZero() {
		t.Fatalf("stable rate generated difference: %s", result.Difference.String())
	}
	results, err := engine.Revalue(context.Background(), []MonetaryPosition{position}, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("stable rate generated entries: %+v", results)
	}
}

func TestNonMonetaryPositionsAreSkipped(t *testing.T) {
	registry := NewRateRegistry()
	date := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	if err := registry.Register(ExchangeRate{FromCurrency: "USD", ToCurrency: "SAR", Rate: decimal.NewFromInt(4), EffectiveDate: date, Type: ClosingRate}); err != nil {
		t.Fatal(err)
	}
	results, err := NewRevaluationEngine(registry).Revalue(context.Background(), []MonetaryPosition{{
		AccountID: uuid.New(), ForeignCurrencyBalance: decimal.NewFromInt(100), TransactionCurrency: "USD",
		FunctionalCurrency: "SAR", CarryingAmount: testMoney(t, "375", "SAR"), Type: NonMonetary,
	}}, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("non-monetary position was revalued: %+v", results)
	}
}

func TestRevaluationRequiresExplicitMonetaryClassification(t *testing.T) {
	engine := NewRevaluationEngine(NewRateRegistry())
	position := MonetaryPosition{
		AccountID: uuid.New(), ForeignCurrencyBalance: decimal.NewFromInt(100),
		TransactionCurrency: "USD", FunctionalCurrency: "SAR",
		CarryingAmount: testMoney(t, "375", "SAR"),
	}
	if _, err := engine.Calculate(context.Background(), position, time.Now()); err != ErrInvalidPosition {
		t.Fatalf("unclassified position error = %v; want ErrInvalidPosition", err)
	}
	position.Type = PositionType("UNKNOWN")
	if _, err := engine.Revalue(context.Background(), []MonetaryPosition{position}, time.Now()); err != ErrInvalidPosition {
		t.Fatalf("unknown position type error = %v; want ErrInvalidPosition", err)
	}
}

func TestRealizedFXDifference(t *testing.T) {
	difference, err := RealizedFXDifference(decimal.NewFromInt(100), decimal.RequireFromString("3.7500"), decimal.RequireFromString("4.0000"), "SAR")
	if err != nil {
		t.Fatal(err)
	}
	if !difference.Amount().Equal(decimal.NewFromInt(25)) {
		t.Fatalf("unexpected realized difference: %s", difference.String())
	}
}
