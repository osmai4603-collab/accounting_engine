package types

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func mustMoney(t *testing.T, value, currency string) Money {
	t.Helper()
	money, err := NewMoneyFromString(value, currency)
	if err != nil {
		t.Fatalf("NewMoneyFromString(%q, %q): %v", value, currency, err)
	}
	return money
}

func TestZeroValueMoneyJSONRoundTrip(t *testing.T) {
	data, err := json.Marshal(Money{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "null" {
		t.Fatalf("zero value marshaled as %s, want null", data)
	}
	var decoded Money
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Currency() != "" || !decoded.Amount().IsZero() {
		t.Fatalf("zero value did not round-trip: %+v", decoded)
	}
}

func TestDecimalAddition(t *testing.T) {
	a := mustMoney(t, "0.1", "SAR")
	b := mustMoney(t, "0.2", "SAR")
	got, err := a.Add(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.3000 SAR" {
		t.Fatalf("got %q", got.String())
	}
}

func TestCurrencyMismatch(t *testing.T) {
	_, err := mustMoney(t, "1", "SAR").Add(mustMoney(t, "1", "USD"))
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("got error %v", err)
	}
}

func TestInvalidAmounts(t *testing.T) {
	for _, value := range []string{"NaN", "Infinity", "-Infinity", "abc"} {
		if _, err := NewMoneyFromString(value, "SAR"); err == nil {
			t.Errorf("expected %q to be rejected", value)
		}
	}
}

func TestScaleAndBankersRounding(t *testing.T) {
	amount := mustMoney(t, "1.23445", "SAR")
	if amount.String() != "1.2344 SAR" {
		t.Fatalf("got %q", amount.String())
	}

	amount, err := mustMoney(t, "1", "SAR").Divide(decimal.NewFromInt(8))
	if err != nil {
		t.Fatal(err)
	}
	if amount.String() != "0.1250 SAR" {
		t.Fatalf("got %q", amount.String())
	}
}

func TestDivideKeepsInternalPrecisionUntilPosting(t *testing.T) {
	installment, err := mustMoney(t, "1000", "SAR").Divide(decimal.NewFromInt(360))
	if err != nil {
		t.Fatal(err)
	}
	if got := installment.Amount().StringFixed(InternalScale); got != "2.777777777778" {
		t.Fatalf("internal division precision = %s, want 2.777777777778", got)
	}
	total := decimal.Zero
	for range 360 {
		total = total.Add(installment.Amount())
	}
	totalMoney, err := NewMoney(total, "SAR")
	if err != nil {
		t.Fatal(err)
	}
	if got := totalMoney.RoundForPosting().Amount().StringFixed(FinancialScale); got != "1000.0000" {
		t.Fatalf("posted installment total = %s, want 1000.0000", got)
	}
}

func TestMinorUnitsUseCurrencyPrecisionAndBankersRounding(t *testing.T) {
	for _, test := range []struct {
		amount   string
		currency string
		want     int64
	}{
		{amount: "0.025", currency: "USD", want: 2},
		{amount: "0.035", currency: "USD", want: 4},
		{amount: "2.5", currency: "JPY", want: 2},
		{amount: "2.5", currency: "KWD", want: 2500},
	} {
		value := mustMoney(t, test.amount, test.currency)
		got, err := value.MinorUnits()
		if err != nil {
			t.Fatalf("MinorUnits(%s %s): %v", test.amount, test.currency, err)
		}
		if got != test.want {
			t.Errorf("MinorUnits(%s %s) = %d, want %d", test.amount, test.currency, got, test.want)
		}
	}
}

func TestMinorUnitsExactRejectsSubMinorFractions(t *testing.T) {
	exact := mustMoney(t, "10.01", "USD")
	if exact.HasSubMinorUnitFraction() {
		t.Fatal("10.01 USD must be exact at minor units")
	}
	if got, err := exact.MinorUnitsExact(); err != nil || got != 1001 {
		t.Fatalf("MinorUnitsExact(10.01 USD) = %d, %v; want 1001, nil", got, err)
	}

	lossy := mustMoney(t, "10.005", "USD")
	if !lossy.HasSubMinorUnitFraction() {
		t.Fatal("10.005 USD must be flagged as sub-minor-unit")
	}
	if _, err := lossy.MinorUnitsExact(); !errors.Is(err, ErrSubMinorUnitAmount) {
		t.Fatalf("MinorUnitsExact(10.005 USD) error = %v, want ErrSubMinorUnitAmount", err)
	}
	// The explicit rounding path still works and matches banker's rounding.
	if got, err := lossy.MinorUnits(); err != nil || got != 1000 {
		t.Fatalf("MinorUnits(10.005 USD) = %d, %v; want 1000, nil", got, err)
	}
	difference, err := lossy.RoundingDifference()
	if err != nil {
		t.Fatal(err)
	}
	if difference.String() != "0.0050 USD" {
		t.Fatalf("RoundingDifference(10.005 USD) = %s, want 0.0050 USD", difference)
	}

	// Three-decimal currencies keep exactness at the third digit.
	kwd := mustMoney(t, "2.505", "KWD")
	if kwd.HasSubMinorUnitFraction() {
		t.Fatal("2.505 KWD must be exact at minor units")
	}
	if got, err := kwd.MinorUnitsExact(); err != nil || got != 2505 {
		t.Fatalf("MinorUnitsExact(2.505 KWD) = %d, %v; want 2505, nil", got, err)
	}
}

func TestRoundForPostingUsesCurrencyMinorUnits(t *testing.T) {
	value := mustMoney(t, "10.005", "USD")
	if got := value.RoundForPosting(); !got.Amount().Equal(decimal.RequireFromString("10")) {
		t.Fatalf("RoundForPosting(10.005 USD) = %s, want 10", got.Amount())
	}
	value = mustMoney(t, "2.505", "KWD")
	if got := value.RoundForPosting(); !got.Amount().Equal(decimal.RequireFromString("2.505")) {
		t.Fatalf("RoundForPosting(2.505 KWD) = %s, want 2.505", got.Amount())
	}
}

func TestBigNumber(t *testing.T) {
	value := "999999999999999999999999.9999"
	money := mustMoney(t, value, "SAR")
	if money.Amount().StringFixed(FinancialScale) != value {
		t.Fatalf("got %s", money.Amount().StringFixed(FinancialScale))
	}
}

func TestMoneyJSON(t *testing.T) {
	money := mustMoney(t, "12.5", "SAR")
	data, err := json.Marshal(money)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"amount":"12.5000","currency":"SAR"}` {
		t.Fatalf("unexpected JSON: %s", data)
	}

	var decoded Money
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(money) {
		t.Fatalf("round trip mismatch: got %v, want %v", decoded, money)
	}
}

func TestComparisonsAndNegative(t *testing.T) {
	negative := mustMoney(t, "-1", "SAR")
	if !negative.IsNegative() {
		t.Fatal("expected negative amount")
	}
	positive := mustMoney(t, "1", "SAR")
	if !positive.GreaterThan(negative) || !negative.LessThan(positive) {
		t.Fatal("comparison mismatch")
	}
}

func TestISO4217NumericRegistry(t *testing.T) {
	cases := []struct {
		code        string
		numericCode string
		minorUnits  int32
	}{
		{"USD", "840", 2},
		{"EUR", "978", 2},
		{"SAR", "682", 2},
		{"KWD", "414", 3},
		{"BHD", "048", 3},
		{"JPY", "392", 0},
		{"GBP", "826", 2},
	}
	for _, tc := range cases {
		curr, ok := LookupCurrency(tc.code)
		if !ok {
			t.Fatalf("LookupCurrency(%q) failed", tc.code)
		}
		if curr.NumericCode != tc.numericCode {
			t.Errorf("currency %s numeric code = %s, want %s", tc.code, curr.NumericCode, tc.numericCode)
		}
		if curr.MinorUnit != tc.minorUnits {
			t.Errorf("currency %s minor units = %d, want %d", tc.code, curr.MinorUnit, tc.minorUnits)
		}
		byNum, ok := LookupCurrencyByNumericCode(tc.numericCode)
		if !ok || string(byNum.Code) != tc.code {
			t.Errorf("LookupCurrencyByNumericCode(%q) = %v, want %s", tc.numericCode, byNum, tc.code)
		}
	}
}
