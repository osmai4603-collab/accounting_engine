package posting

import (
	"errors"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ErrRoundingAccountRequired is returned when an entry carries functional
// amounts with fractions smaller than the currency minor unit and no rounding
// difference account was supplied to settle them.
var ErrRoundingAccountRequired = errors.New("rounding difference account is required to settle sub-minor-unit amounts")

// RoundEntryToMinorUnits applies the posting precision contract to an entry:
// every line's functional debit/credit is rounded to the currency minor unit
// (banker's rounding) and, when the rounded entry no longer balances, the
// residual is booked on roundingAccountID so the move stays balanced.
//
// The residual is the net of the per-line rounding differences:
//
//	residual = Σ round(functional debit) - Σ round(functional credit)
//
// A positive residual means debits exceed credits, so the rounding difference
// account is credited; a negative residual means it is debited. When the
// residual is zero no extra line is added. The entry is re-validated before
// returning.
//
// Transaction amounts are not modified: they are entered at transaction
// currency precision and are validated against the functional side by
// JournalEntryLine.Validate.
func RoundEntryToMinorUnits(entry *JournalEntry, roundingAccountID uuid.UUID) error {
	return RoundEntryWithRoundingPolicy(entry, roundingAccountID, nil)
}

// RoundEntryWithRoundingPolicy rounds functional lines through the supplied
// currency policy and posts any resulting imbalance to the configured account.
func RoundEntryWithRoundingPolicy(
	entry *JournalEntry,
	roundingAccountID uuid.UUID,
	round func(financialtypes.Money) (financialtypes.Money, error),
) error {
	if entry == nil {
		return ErrInvalidEntry
	}
	if len(entry.Lines) == 0 {
		return ErrMinimumLines
	}

	functionalCurrency := ""
	for i := range entry.Lines {
		resolved, err := normalizeLineCurrencies(entry.Lines[i])
		if err != nil {
			return err
		}
		currency := resolved.FunctionalDebit.Currency()
		if currency == "" {
			currency = resolved.FunctionalCredit.Currency()
		}
		if functionalCurrency == "" {
			functionalCurrency = currency
		} else if currency != functionalCurrency {
			return financialtypes.ErrCurrencyMismatch
		}
		entry.Lines[i] = resolved
	}

	residual := decimal.Zero
	for i := range entry.Lines {
		line := entry.Lines[i]
		roundedDebit, err := roundMoney(line.FunctionalDebit, round)
		if err != nil {
			return err
		}
		roundedCredit, err := roundMoney(line.FunctionalCredit, round)
		if err != nil {
			return err
		}
		residual = residual.Add(roundedDebit.Amount()).Sub(roundedCredit.Amount())
		line.FunctionalDebit = roundedDebit
		line.FunctionalCredit = roundedCredit
		entry.Lines[i] = line
	}

	if !residual.IsZero() {
		if roundingAccountID == uuid.Nil {
			return ErrRoundingAccountRequired
		}
		amount, err := financialtypes.NewMoney(residual.Abs(), functionalCurrency)
		if err != nil {
			return err
		}
		zero, err := financialtypes.Zero(functionalCurrency)
		if err != nil {
			return err
		}
		roundingLine := JournalEntryLine{
			CompanyID:           entry.CompanyID,
			ID:                  uuid.NewSHA1(entry.ID, []byte("rounding-difference")),
			AccountID:           roundingAccountID,
			TransactionAmount:   amount,
			TransactionCurrency: functionalCurrency,
			ExchangeRate:        decimal.NewFromInt(1),
			FunctionalDebit:     zero,
			FunctionalCredit:    amount,
		}
		if residual.IsNegative() {
			// Debits are short: debit the rounding difference account.
			roundingLine.FunctionalDebit = amount
			roundingLine.FunctionalCredit = zero
		}
		entry.Lines = append(entry.Lines, roundingLine)
	}

	return entry.ValidateWithRounding(round)
}

func roundMoney(amount financialtypes.Money, round func(financialtypes.Money) (financialtypes.Money, error)) (financialtypes.Money, error) {
	if round != nil {
		return round(amount)
	}
	return amount.Rounded()
}
