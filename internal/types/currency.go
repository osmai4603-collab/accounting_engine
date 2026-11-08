package types

import "strings"

const (
	SAR CurrencyCode = "SAR"
	USD CurrencyCode = "USD"
	EUR CurrencyCode = "EUR"
	AED CurrencyCode = "AED"
	GBP CurrencyCode = "GBP"
	JPY CurrencyCode = "JPY"
	CNY CurrencyCode = "CNY"
	KWD CurrencyCode = "KWD"
)

type CurrencyCode string

type Currency struct {
	Code      CurrencyCode
	Symbol    string
	Name      string
	MinorUnit int32
}

var currencies = map[CurrencyCode]Currency{
	SAR: {Code: SAR, Symbol: "\u0631.\u0633", Name: "Saudi Riyal", MinorUnit: 2},
	USD: {Code: USD, Symbol: "$", Name: "US Dollar", MinorUnit: 2},
	EUR: {Code: EUR, Symbol: "\u20ac", Name: "Euro", MinorUnit: 2},
	AED: {Code: AED, Symbol: "\u062f.\u0625", Name: "UAE Dirham", MinorUnit: 2},
	GBP: {Code: GBP, Symbol: "\u00a3", Name: "Pound Sterling", MinorUnit: 2},
	JPY: {Code: JPY, Symbol: "\u00a5", Name: "Japanese Yen", MinorUnit: 0},
	CNY: {Code: CNY, Symbol: "\u00a5", Name: "Chinese Yuan", MinorUnit: 2},
	KWD: {Code: KWD, Symbol: "\u062f.\u0643", Name: "Kuwaiti Dinar", MinorUnit: 3},
}

func LookupCurrency(code string) (Currency, bool) {
	c, ok := currencies[CurrencyCode(strings.ToUpper(strings.TrimSpace(code)))]
	return c, ok
}

func normalizeCurrency(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if _, ok := LookupCurrency(normalized); !ok {
		return "", ErrInvalidCurrency
	}
	return normalized, nil
}
