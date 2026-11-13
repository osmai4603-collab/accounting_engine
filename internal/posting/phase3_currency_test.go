package posting

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func currencyLayerLine(t *testing.T, transactionAmount, transactionCurrency, rate, functionalDebit, functionalCredit string) JournalEntryLine {
	t.Helper()
	return JournalEntryLine{
		AccountID:           uuid.New(),
		TransactionAmount:   money(t, transactionAmount, transactionCurrency),
		TransactionCurrency: transactionCurrency,
		ExchangeRate:        decimal.RequireFromString(rate),
		FunctionalDebit:     money(t, functionalDebit, "SAR"),
		FunctionalCredit:    money(t, functionalCredit, "SAR"),
	}
}

func TestLayeredEntryBalancesOnlyInFunctionalCurrency(t *testing.T) {
	entry := JournalEntry{
		ID: uuid.New(), JournalID: uuid.New(), Date: time.Now().UTC(),
		Lines: []JournalEntryLine{
			currencyLayerLine(t, "100", "EUR", "3.75", "375", "0"),
			currencyLayerLine(t, "375", "USD", "1", "0", "375"),
		},
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("balanced functional entry was rejected: %v", err)
	}

	entry.Lines[1] = currencyLayerLine(t, "370", "USD", "1", "0", "370")
	err := entry.Validate()
	if !errors.Is(err, ErrFunctionalBalanceMismatch) {
		t.Fatalf("unbalanced functional entry error = %v, want ErrFunctionalBalanceMismatch", err)
	}
	if !errors.Is(err, financialtypes.ErrUnbalancedMove) {
		t.Fatalf("functional imbalance does not preserve the legacy unbalanced error: %v", err)
	}
}

func TestLayeredEntryRejectsNonPositiveExchangeRate(t *testing.T) {
	for _, rate := range []string{"0", "-1"} {
		t.Run(rate, func(t *testing.T) {
			line := currencyLayerLine(t, "10", "EUR", rate, "37.5", "0")
			if err := line.Validate(); !errors.Is(err, ErrInvalidExchangeRate) {
				t.Fatalf("line validation error = %v, want ErrInvalidExchangeRate", err)
			}
		})
	}
}

func TestLegacyJournalLinesNormalizeForBackwardCompatibility(t *testing.T) {
	entry := JournalEntry{
		ID: uuid.New(), JournalID: uuid.New(), Date: time.Now().UTC(),
		Lines: []JournalEntryLine{
			{AccountID: uuid.New(), Debit: money(t, "100", "EUR"), AmountCurrency: money(t, "375", "SAR")},
			{AccountID: uuid.New(), Credit: money(t, "375", "USD"), AmountCurrency: money(t, "375", "SAR")},
		},
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("legacy balanced entry was rejected: %v", err)
	}
	for _, line := range entry.Lines {
		normalized, err := NormalizeLineCurrencies(line)
		if err != nil {
			t.Fatalf("legacy line normalization failed: %v", err)
		}
		if normalized.TransactionCurrency == "" || normalized.FunctionalDebit.Currency() != "SAR" ||
			normalized.FunctionalCredit.Currency() != "SAR" || !normalized.ExchangeRate.IsPositive() {
			t.Fatalf("legacy line was not fully normalized: %+v", normalized)
		}
	}
}
