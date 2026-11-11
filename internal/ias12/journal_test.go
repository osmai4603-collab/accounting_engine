package ias12

import (
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
)

func TestGenerateDeferredTaxEntryForIncreaseAndReversal(t *testing.T) {
	expense, dtaAccount, dtlAccount := uuid.New(), uuid.New(), uuid.New()
	base := DeferredTaxEntryInput{
		CompanyID: uuid.New(), JournalID: uuid.New(), EntryDate: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy: "tax-engine", Accounts: DeferredTaxAccounts{Expense: expense, DTA: dtaAccount, DTL: dtlAccount},
		Previous: DeferredTaxPosition{DTA: testMoney(t, "10", "USD"), DTL: testMoney(t, "20", "USD")},
		Closing:  DeferredTaxPosition{DTA: testMoney(t, "15", "USD"), DTL: testMoney(t, "27", "USD")},
	}
	entry, err := GenerateDeferredTaxEntry(base)
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
	if len(entry.Lines) != 4 || entry.Classification != posting.EntryClassAdjusting ||
		entry.CompanyID != base.CompanyID || entry.JournalID != base.JournalID {
		t.Fatalf("unexpected generated entry: %+v", entry)
	}
	assertAccountTotal(t, entry, expense, "7", "5")
	assertAccountTotal(t, entry, dtaAccount, "5", "0")
	assertAccountTotal(t, entry, dtlAccount, "0", "7")

	base.Previous = base.Closing
	base.Closing = DeferredTaxPosition{DTA: testMoney(t, "8", "USD"), DTL: testMoney(t, "11", "USD")}
	reversal, err := GenerateDeferredTaxEntry(base)
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, reversal)
	assertAccountTotal(t, reversal, expense, "7", "16")
	assertAccountTotal(t, reversal, dtaAccount, "0", "7")
	assertAccountTotal(t, reversal, dtlAccount, "16", "0")
}

func TestGenerateDeferredTaxEntryRejectsInvalidAndZeroMovements(t *testing.T) {
	input := DeferredTaxEntryInput{
		CompanyID: uuid.New(), JournalID: uuid.New(), EntryDate: time.Now().UTC(), CreatedBy: "tax-engine",
		Previous: DeferredTaxPosition{DTA: testMoney(t, "1", "USD"), DTL: testMoney(t, "0", "USD")},
		Closing:  DeferredTaxPosition{DTA: testMoney(t, "1", "USD"), DTL: testMoney(t, "0", "USD")},
		Accounts: DeferredTaxAccounts{Expense: uuid.New(), DTA: uuid.New(), DTL: uuid.New()},
	}

	if _, err := GenerateDeferredTaxEntry(input); !errors.Is(err, ErrNoDeferredTaxMovement) {
		t.Fatalf("zero movement error = %v, want %v", err, ErrNoDeferredTaxMovement)
	}
	input.Closing.DTA = testMoney(t, "-1", "USD")
	if _, err := GenerateDeferredTaxEntry(input); !errors.Is(err, financialtypes.ErrNegativeAmountProhibited) {
		t.Fatalf("negative balance error = %v, want %v", err, financialtypes.ErrNegativeAmountProhibited)
	}
	input.Closing.DTA = testMoney(t, "2", "EUR")
	if _, err := GenerateDeferredTaxEntry(input); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("mixed currency error = %v, want %v", err, ErrCurrencyMismatch)
	}
}

func TestGenerateDeferredTaxEntryRoutesOCIAndCurrentTaxAccrual(t *testing.T) {
	expense, dta, dtl, oci := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	zero := testMoney(t, "0", "USD")
	entry, err := GenerateDeferredTaxEntry(DeferredTaxEntryInput{
		CompanyID: uuid.New(), JournalID: uuid.New(), EntryDate: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy: "tax-engine",
		Previous:  DeferredTaxPosition{DTA: zero, DTL: zero},
		Closing:   DeferredTaxPosition{DTA: testMoney(t, "5", "USD"), DTL: zero},
		OCIChange: DeferredTaxPosition{DTA: testMoney(t, "2", "USD"), DTL: zero},
		Accounts:  DeferredTaxAccounts{Expense: expense, DTA: dta, DTL: dtl, OCI: oci},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, entry)
	assertAccountTotal(t, entry, expense, "0", "3")
	assertAccountTotal(t, entry, oci, "0", "2")
	assertAccountTotal(t, entry, dta, "5", "0")

	currentTax, err := GenerateCurrentTaxEntry(CurrentTaxEntryInput{
		CompanyID: uuid.New(), JournalID: uuid.New(), EntryDate: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy: "tax-engine", Amount: testMoney(t, "25", "USD"),
		ExpenseAccountID: expense, PayableAccountID: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBalanced(t, currentTax)
}

func assertBalanced(t *testing.T, entry posting.JournalEntry) {
	t.Helper()
	if err := entry.Validate(); err != nil {
		t.Fatalf("generated journal entry is invalid or unbalanced: %v", err)
	}
}

func assertAccountTotal(t *testing.T, entry posting.JournalEntry, account uuid.UUID, debit, credit string) {
	t.Helper()
	debitAmount, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	creditAmount, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range entry.Lines {
		if line.AccountID != account {
			continue
		}
		var debitAmountErr, creditAmountErr error
		if line.Debit.Currency() != "" {
			debitAmount, debitAmountErr = debitAmount.Add(line.Debit)
		}
		if line.Credit.Currency() != "" {
			creditAmount, creditAmountErr = creditAmount.Add(line.Credit)
		}
		if debitAmountErr != nil || creditAmountErr != nil {
			t.Fatalf("failed to total journal lines: %v %v", debitAmountErr, creditAmountErr)
		}
	}
	debitTotal, creditTotal := debitAmount.Amount().String(), creditAmount.Amount().String()
	if debitTotal != debit || creditTotal != credit {
		t.Errorf("account %s totals debit=%s credit=%s, want debit=%s credit=%s", account, debitTotal, creditTotal, debit, credit)
	}
}
