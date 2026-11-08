package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

const FinancialScale = 4
const maximumPrecision = 28

type Money struct {
	amount   decimal.Decimal
	currency string
}

func NewMoney(amount decimal.Decimal, currency string) (Money, error) {
	normalized, err := normalizeCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return newMoney(amount, normalized)
}

func NewMoneyFromString(value, currency string) (Money, error) {
	normalized, err := normalizeCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return Money{}, ErrInvalidAmount
	}
	return newMoney(amount, normalized)
}

func Zero(currency string) (Money, error) {
	return NewMoney(decimal.Zero, currency)
}

func newMoney(amount decimal.Decimal, currency string) (Money, error) {
	if precisionDigits(amount) > maximumPrecision {
		return Money{}, ErrAmountExceedsPrecision
	}
	amount = amount.RoundBank(FinancialScale)
	if precisionDigits(amount) > maximumPrecision {
		return Money{}, ErrAmountExceedsPrecision
	}
	return Money{amount: amount, currency: currency}, nil
}

func precisionDigits(value decimal.Decimal) int {
	coefficient := value.Coefficient()
	digits := len(coefficient.String())
	if digits == 0 {
		return 1
	}
	return digits
}

func (m Money) Amount() decimal.Decimal {
	return m.amount
}

func (m Money) Currency() string {
	return m.currency
}

func (m Money) Add(other Money) (Money, error) {
	if err := m.ensureSameCurrency(other); err != nil {
		return Money{}, err
	}
	return newMoney(m.amount.Add(other.amount), m.currency)
}

func (m Money) Subtract(other Money) (Money, error) {
	if err := m.ensureSameCurrency(other); err != nil {
		return Money{}, err
	}
	return newMoney(m.amount.Sub(other.amount), m.currency)
}

func (m Money) Multiply(scalar decimal.Decimal) (Money, error) {
	return newMoney(m.amount.Mul(scalar), m.currency)
}

func (m Money) Divide(scalar decimal.Decimal) (Money, error) {
	if scalar.IsZero() {
		return Money{}, ErrInvalidAmount
	}
	return newMoney(m.amount.DivRound(scalar, FinancialScale), m.currency)
}

func (m Money) IsNegative() bool {
	return m.amount.IsNegative()
}

func (m Money) Equal(other Money) bool {
	return m.currency == other.currency && m.amount.Equal(other.amount)
}

func (m Money) GreaterThan(other Money) bool {
	return m.currency == other.currency && m.amount.GreaterThan(other.amount)
}

func (m Money) LessThan(other Money) bool {
	return m.currency == other.currency && m.amount.LessThan(other.amount)
}

func (m Money) ensureSameCurrency(other Money) error {
	if m.currency != other.currency {
		return ErrCurrencyMismatch
	}
	return nil
}

func (m Money) String() string {
	return fmt.Sprintf("%s %s", m.amount.StringFixed(FinancialScale), m.currency)
}

func (m Money) MarshalJSON() ([]byte, error) {
	if m.currency == "" {
		return nil, ErrInvalidCurrency
	}
	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{
		Amount:   m.amount.StringFixed(FinancialScale),
		Currency: m.currency,
	})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalidAmount
	}

	var encoded struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(data, &encoded); err != nil {
		return ErrInvalidAmount
	}
	parsed, err := NewMoneyFromString(encoded.Amount, encoded.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
