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
