package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// Precision contract (Task 1.3):
//
//   - Internal arithmetic and FX conversions keep InternalScale (12) decimal
//     places so intermediate results are not truncated prematurely.
//   - Amounts that are persisted -- posted move lines and materialized
//     account balances -- are expressed in the currency's MinorUnit
//     (integer minor units, e.g. cents for USD/SAR, fils for KWD).
//   - Converting an amount to minor units must never silently drop a
//     fraction. Callers either round explicitly (Rounded / RoundForPosting)
//     or settle the residual returned by RoundingDifference on a dedicated
//     rounding difference account.
const (
	FinancialScale = 4
	InternalScale  = 12
)
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

func NewMoneyFromDecimal(amount decimal.Decimal, currency string) (Money, error) {
	return NewMoney(amount, currency)
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
	return Money{amount: amount, currency: currency}, nil
}

// Rounded returns the amount rounded to the currency's minor unit using
// banker's rounding. This is the explicit rounding step of the precision
// contract; callers that must not lose value should compare the result with
// the original via RoundingDifference.
func (m Money) Rounded() (Money, error) {
	currency, ok := LookupCurrency(m.currency)
	if !ok {
		return Money{}, ErrInvalidCurrency
	}
	return newMoneyAtScale(m.amount.RoundBank(currency.MinorUnit), m.currency)
}

func newMoneyAtScale(amount decimal.Decimal, currency string) (Money, error) {
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

// MinorUnits converts the amount to the currency's integer minor units using
// banker's rounding. It is the explicit rounding path of the contract; callers
// that must detect a lossy conversion use MinorUnitsExact or
// RoundingDifference instead.
func (m Money) MinorUnits() (int64, error) {
	currency, ok := LookupCurrency(m.currency)
	if !ok {
		return 0, ErrInvalidCurrency
	}
	rounded := m.amount.RoundBank(currency.MinorUnit)
	minorUnits := rounded.Shift(currency.MinorUnit)
	if !minorUnits.Equal(minorUnits.Truncate(0)) || !minorUnits.BigInt().IsInt64() {
		return 0, ErrAmountExceedsPrecision
	}
	return minorUnits.IntPart(), nil
}

// MinorUnitsExact converts the amount to integer minor units without any
// rounding. It fails with ErrSubMinorUnitAmount when the amount carries a
// fraction smaller than one minor unit, so a lossy conversion can never be
// stored silently.
func (m Money) MinorUnitsExact() (int64, error) {
	currency, ok := LookupCurrency(m.currency)
	if !ok {
		return 0, ErrInvalidCurrency
	}
	minorUnits := m.amount.Shift(currency.MinorUnit)
	if !minorUnits.Equal(minorUnits.Truncate(0)) {
		return 0, ErrSubMinorUnitAmount
	}
	if !minorUnits.BigInt().IsInt64() {
		return 0, ErrAmountExceedsPrecision
	}
	return minorUnits.IntPart(), nil
}

// HasSubMinorUnitFraction reports whether the amount cannot be represented
// exactly in the currency's minor units.
func (m Money) HasSubMinorUnitFraction() bool {
	currency, ok := LookupCurrency(m.currency)
	if !ok {
		return false
	}
	minorUnits := m.amount.Shift(currency.MinorUnit)
	return !minorUnits.Equal(minorUnits.Truncate(0))
}

// RoundingDifference returns the residual between the exact amount and its
// minor-unit-rounded value (banker's rounding). The residual is what a posting
// path must book on a dedicated rounding difference account when it cannot
// represent the amount exactly in minor units.
func (m Money) RoundingDifference() (Money, error) {
	rounded, err := m.Rounded()
	if err != nil {
		return Money{}, err
	}
	return newMoney(m.amount.Sub(rounded.amount), m.currency)
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
	return newMoney(m.amount.DivRound(scalar, InternalScale), m.currency)
}

// RoundForPosting rounds the amount to the currency's minor unit (banker's
// rounding), the representation used by posted move lines and account
// balances. Internal computations keep InternalScale precision until this
// boundary.
func (m Money) RoundForPosting() Money {
	rounded, err := m.Rounded()
	if err != nil {
		return Money{amount: m.amount.Round(FinancialScale), currency: m.currency}
	}
	return rounded
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
	return fmt.Sprintf("%s %s", m.amount.RoundBank(FinancialScale).StringFixed(FinancialScale), m.currency)
}

func CurrencyMinorUnit(currency string) int32 {
	if c, ok := LookupCurrency(currency); ok {
		return c.MinorUnit
	}
	return FinancialScale
}

func (m Money) MarshalJSON() ([]byte, error) {
	if m.currency == "" {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{
		Amount:   m.amount.RoundBank(FinancialScale).StringFixed(FinancialScale),
		Currency: m.currency,
	})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*m = Money{}
		return nil
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
