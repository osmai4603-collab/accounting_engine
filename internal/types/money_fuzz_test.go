package types

import (
	"encoding/json"
	"testing"
)

// FuzzMoneyFromStringNeverPanics feeds malformed currency amounts and currency
// codes into the money parser. Any accepted value must survive the full
// formatting and minor-unit conversion path without panicking.
func FuzzMoneyFromStringNeverPanics(f *testing.F) {
	seeds := []struct {
		amount, currency string
	}{
		{"100.50", "USD"},
		{"-0.0001", "KWD"},
		{"1e999999", "USD"},
		{" 42 ", "SAR"},
		{"", "USD"},
		{"1.2.3", "USD"},
		{"999999999999999999999999999999999999", "JPY"},
		{"0", "ZZZ"},
		{"-", "usd"},
		{"1,000.00", "EUR"},
	}
	for _, seed := range seeds {
		f.Add(seed.amount, seed.currency)
	}
	f.Fuzz(func(t *testing.T, amount, currency string) {
		parsed, err := NewMoneyFromString(amount, currency)
		if err != nil {
			return
		}
		_ = parsed.String()
		_, _ = parsed.Rounded()
		_, _ = parsed.MinorUnits()
		_, _ = parsed.MinorUnitsExact()
		_, _ = parsed.RoundingDifference()
		if _, err := json.Marshal(parsed); err != nil {
			t.Fatalf("accepted money value failed to marshal: %v", err)
		}
	})
}

// FuzzMoneyJSONNeverPanics exercises the persisted money representation with
// arbitrary JSON and verifies that a decoded value can always be re-encoded.
func FuzzMoneyJSONNeverPanics(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"amount":"100.50","currency":"USD"}`),
		[]byte(`{"amount":"","currency":""}`),
		[]byte(`{"amount":"1e999999","currency":"USD"}`),
		[]byte(`null`),
		[]byte(`{}`),
		[]byte(`[]`),
		[]byte(`{"amount":100.50,"currency":"USD"}`),
		[]byte(`{"amount":"100","currency":null}`),
		[]byte("\xff\xfe\xfd"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var value Money
		if err := json.Unmarshal(data, &value); err != nil {
			return
		}
		if _, err := json.Marshal(value); err != nil {
			t.Fatalf("decoded money value failed to marshal: %v", err)
		}
	})
}
