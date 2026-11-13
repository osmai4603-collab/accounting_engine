package posting

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func roundingTestMoney(t *testing.T, value, currency string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(value, currency)
	if err != nil {
		t.Fatalf("NewMoneyFromString(%q, %q): %v", value, currency, err)
	}
	return money
}

// roundingTestEntry builds a balanced entry whose functional legs carry
// fractions smaller than the USD minor unit (10.005 + 10.005 = 20.010).
func roundingTestEntry(t *testing.T, companyID, journalID uuid.UUID) JournalEntry {
	t.Helper()
	zero := roundingTestMoney(t, "0", "USD")
	debitA := roundingTestMoney(t, "10.005", "USD")
	debitB := roundingTestMoney(t, "10.005", "USD")
	credit := roundingTestMoney(t, "20.010", "USD")
	rate := decimal.RequireFromString("1.0005")
	entry, err := NewJournalEntry(journalID, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), "maker", []JournalEntryLine{
		{
			CompanyID: companyID, ID: uuid.New(), AccountID: uuid.New(),
			TransactionAmount: roundingTestMoney(t, "10.00", "USD"), TransactionCurrency: "USD",
			ExchangeRate: rate, FunctionalDebit: debitA, FunctionalCredit: zero,
		},
		{
			CompanyID: companyID, ID: uuid.New(), AccountID: uuid.New(),
			TransactionAmount: roundingTestMoney(t, "10.00", "USD"), TransactionCurrency: "USD",
			ExchangeRate: rate, FunctionalDebit: debitB, FunctionalCredit: zero,
		},
		{
			CompanyID: companyID, ID: uuid.New(), AccountID: uuid.New(),
			TransactionAmount: roundingTestMoney(t, "20.01", "USD"), TransactionCurrency: "USD",
			ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: credit,
		},
	})
	if err != nil {
		t.Fatalf("NewJournalEntry: %v", err)
	}
	entry.CompanyID = companyID
	return entry
}

func TestRoundEntryToMinorUnitsSettlesResidual(t *testing.T) {
	companyID, journalID := uuid.New(), uuid.New()
	roundingAccountID := uuid.New()
	entry := roundingTestEntry(t, companyID, journalID)

	if err := RoundEntryToMinorUnits(&entry, roundingAccountID); err != nil {
		t.Fatalf("RoundEntryToMinorUnits: %v", err)
	}
	if len(entry.Lines) != 4 {
		t.Fatalf("expected 4 lines after settlement, got %d", len(entry.Lines))
	}
	for _, line := range entry.Lines {
		if line.FunctionalDebit.HasSubMinorUnitFraction() || line.FunctionalCredit.HasSubMinorUnitFraction() {
			t.Fatalf("line %s still carries sub-minor-unit fractions", line.ID)
		}
	}
	settlement := entry.Lines[3]
	if settlement.AccountID != roundingAccountID {
		t.Fatalf("settlement line account = %s, want rounding account %s", settlement.AccountID, roundingAccountID)
	}
	// Rounded debits are 10.00 + 10.00 while the rounded credit is 20.01, so
	// the rounding difference account is debited by 0.01.
	if !settlement.FunctionalDebit.Amount().Equal(decimal.RequireFromString("0.01")) ||
		!settlement.FunctionalCredit.Amount().IsZero() {
		t.Fatalf("settlement line = debit %s credit %s, want debit 0.01 credit 0",
			settlement.FunctionalDebit, settlement.FunctionalCredit)
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("rounded entry is not balanced: %v", err)
	}
}

func TestRoundEntryToMinorUnitsRequiresAccountForResidual(t *testing.T) {
	entry := roundingTestEntry(t, uuid.New(), uuid.New())
	if err := RoundEntryToMinorUnits(&entry, uuid.Nil); !errors.Is(err, ErrRoundingAccountRequired) {
		t.Fatalf("error = %v, want ErrRoundingAccountRequired", err)
	}
}

func TestRoundEntryToMinorUnitsLeavesExactEntriesUntouched(t *testing.T) {
	zero := roundingTestMoney(t, "0", "USD")
	debit := roundingTestMoney(t, "100.00", "USD")
	entry, err := NewJournalEntry(uuid.New(), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), "maker", []JournalEntryLine{
		{
			ID: uuid.New(), AccountID: uuid.New(),
			TransactionAmount: debit, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
			FunctionalDebit: debit, FunctionalCredit: zero,
		},
		{
			ID: uuid.New(), AccountID: uuid.New(),
			TransactionAmount: debit, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1),
			FunctionalDebit: zero, FunctionalCredit: debit,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := RoundEntryToMinorUnits(&entry, uuid.Nil); err != nil {
		t.Fatalf("exact entry should not require a rounding account: %v", err)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("exact entry gained %d settlement line(s)", len(entry.Lines)-2)
	}
}
