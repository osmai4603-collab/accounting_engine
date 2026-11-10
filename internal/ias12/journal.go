package ias12

import (
	"strings"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type DeferredTaxPosition struct {
	DTA financialtypes.Money
	DTL financialtypes.Money
}

type DeferredTaxAccounts struct {
	Expense uuid.UUID
	DTA     uuid.UUID
	DTL     uuid.UUID
}

type DeferredTaxEntryInput struct {
	CompanyID uuid.UUID
	JournalID uuid.UUID
	EntryDate time.Time
	CreatedBy string
	Previous  DeferredTaxPosition
	Closing   DeferredTaxPosition
	Accounts  DeferredTaxAccounts
}

// GenerateDeferredTaxEntry posts each gross DTA/DTL change, including reversals.
func GenerateDeferredTaxEntry(input DeferredTaxEntryInput) (posting.JournalEntry, error) {
	if input.CompanyID == uuid.Nil || input.JournalID == uuid.Nil || input.EntryDate.IsZero() ||
		strings.TrimSpace(input.CreatedBy) == "" ||
		input.Accounts.Expense == uuid.Nil || input.Accounts.DTA == uuid.Nil || input.Accounts.DTL == uuid.Nil ||
		input.Accounts.Expense == input.Accounts.DTA || input.Accounts.Expense == input.Accounts.DTL ||
		input.Accounts.DTA == input.Accounts.DTL {
		return posting.JournalEntry{}, ErrInvalidInput
	}
	for _, amount := range []financialtypes.Money{
		input.Previous.DTA, input.Previous.DTL, input.Closing.DTA, input.Closing.DTL,
	} {
		if err := validateNonnegativeMoney(amount); err != nil {
			return posting.JournalEntry{}, err
		}
	}
	currency := input.Previous.DTA.Currency()
	for _, amount := range []financialtypes.Money{
		input.Previous.DTL, input.Closing.DTA, input.Closing.DTL,
	} {
		if amount.Currency() != currency {
			return posting.JournalEntry{}, ErrCurrencyMismatch
		}
	}

	dtaChange := input.Closing.DTA.Amount().Sub(input.Previous.DTA.Amount())
	dtlChange := input.Closing.DTL.Amount().Sub(input.Previous.DTL.Amount())
	lines := make([]posting.JournalEntryLine, 0, 4)
	appendMovement := func(account uuid.UUID, change decimal.Decimal, asset bool) error {
		amount, err := financialtypes.NewMoney(change.Abs(), currency)
		if err != nil {
			return err
		}
		amount = amount.RoundForPosting()
		if amount.Amount().IsZero() {
			return nil
		}
		debitAccount, creditAccount := input.Accounts.Expense, account
		if asset && change.IsPositive() {
			debitAccount, creditAccount = account, input.Accounts.Expense
		} else if !asset && change.IsNegative() {
			debitAccount, creditAccount = account, input.Accounts.Expense
		}
		debitLine := posting.JournalEntryLine{
			CompanyID: input.CompanyID, AccountID: debitAccount, Debit: amount,
		}
		creditLine := posting.JournalEntryLine{
			CompanyID: input.CompanyID, AccountID: creditAccount, Credit: amount,
		}
		lines = append(lines, debitLine, creditLine)
		return nil
	}
	if err := appendMovement(input.Accounts.DTA, dtaChange, true); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := appendMovement(input.Accounts.DTL, dtlChange, false); err != nil {
		return posting.JournalEntry{}, err
	}
	if len(lines) == 0 {
		return posting.JournalEntry{}, ErrNoDeferredTaxMovement
	}

	entry, err := posting.NewJournalEntry(input.JournalID, input.EntryDate, input.CreatedBy, lines)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	entry.CompanyID = input.CompanyID
	entry.Classification = posting.EntryClassAdjusting
	entry.Reference = "Deferred tax measurement adjustment"
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}
