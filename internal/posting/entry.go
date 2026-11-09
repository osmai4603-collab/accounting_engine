package posting

import (
	"errors"
	"reflect"
	"strings"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidEntry         = errors.New("invalid journal entry")
	ErrMinimumLines         = errors.New("journal entry must contain at least two lines")
	ErrLineMustHaveOneSide  = errors.New("journal entry line cannot have debit and credit amounts")
	ErrEntryNotDraft        = errors.New("journal entry is not a draft")
	ErrEntryNotPosted       = errors.New("journal entry is not posted")
	ErrEntryAlreadyReversed = errors.New("journal entry already reversed")
	ErrInvalidReversal      = errors.New("invalid reversal entry")
)

type EntryStatus string

const (
	EntryDraft    EntryStatus = "draft"
	EntryPosted   EntryStatus = "posted"
	EntryReversed EntryStatus = "reversed"
)

const (
	StatusDraft    = EntryDraft
	StatusPosted   = EntryPosted
	StatusReversed = EntryReversed
)

type JournalEntryLine struct {
	CompanyID           uuid.UUID
	ID                  uuid.UUID
	AccountID           uuid.UUID
	PartnerID           *uuid.UUID
	CostCenterID        *uuid.UUID
	ProjectID           *uuid.UUID
	SegmentID           *uuid.UUID
	BusinessUnitID      *uuid.UUID
	MatchingNumber      string
	ReconciledAmount    decimal.Decimal
	Debit               financialtypes.Money
	Credit              financialtypes.Money
	TransactionAmount   financialtypes.Money
	TransactionCurrency string
	ExchangeRate        decimal.Decimal
	FunctionalDebit     financialtypes.Money
	FunctionalCredit    financialtypes.Money
	AmountCurrency      financialtypes.Money
	Currency            string
	ForeignAmount       decimal.Decimal
}

func (l JournalEntryLine) Validate() error {
	debitSet := l.Debit.Currency() != "" && !l.Debit.Amount().IsZero()
	creditSet := l.Credit.Currency() != "" && !l.Credit.Amount().IsZero()
	debitPresent := l.Debit.Currency() != ""
	creditPresent := l.Credit.Currency() != ""

	if debitPresent && l.Debit.IsNegative() {
		return financialtypes.ErrNegativeAmountProhibited
	}
	if creditPresent && l.Credit.IsNegative() {
		return financialtypes.ErrNegativeAmountProhibited
	}
	if debitSet && creditSet {
		return ErrLineMustHaveOneSide
	}
	if !debitSet && !creditSet {
		return financialtypes.ErrZeroAmountLine
	}
	if l.Debit.Currency() != "" && l.Credit.Currency() != "" && l.Debit.Currency() != l.Credit.Currency() {
		return financialtypes.ErrCurrencyMismatch
	}
	if l.AmountCurrency.IsNegative() || l.FunctionalDebit.IsNegative() || l.FunctionalCredit.IsNegative() ||
		l.TransactionAmount.IsNegative() || l.ExchangeRate.IsNegative() {
		return financialtypes.ErrNegativeAmountProhibited
	}
	if (l.FunctionalDebit.Currency() != "" || l.FunctionalCredit.Currency() != "") &&
		(l.FunctionalDebit.Currency() == "" || l.FunctionalCredit.Currency() == "" ||
			l.FunctionalDebit.Currency() != l.FunctionalCredit.Currency()) {
		return financialtypes.ErrCurrencyMismatch
	}
	if (l.FunctionalDebit.Currency() != "" || l.FunctionalCredit.Currency() != "") &&
		l.FunctionalDebit.Amount().IsZero() && l.FunctionalCredit.Amount().IsZero() {
		return financialtypes.ErrZeroAmountLine
	}
	functionalDebitSet := l.FunctionalDebit.Currency() != "" && !l.FunctionalDebit.Amount().IsZero()
	functionalCreditSet := l.FunctionalCredit.Currency() != "" && !l.FunctionalCredit.Amount().IsZero()
	amountCurrencyDebitSet := l.AmountCurrency.Currency() != "" && debitSet
	amountCurrencyCreditSet := l.AmountCurrency.Currency() != "" && creditSet
	if (functionalDebitSet && functionalCreditSet) ||
		(functionalDebitSet && !debitSet) || (functionalCreditSet && !creditSet) ||
		(amountCurrencyDebitSet && amountCurrencyCreditSet) {
		return ErrLineMustHaveOneSide
	}
	if l.AmountCurrency.Currency() != "" && l.AmountCurrency.Amount().IsZero() {
		return financialtypes.ErrZeroAmountLine
	}
	if l.TransactionAmount.Currency() != "" {
		if l.TransactionAmount.Amount().IsZero() {
			return financialtypes.ErrZeroAmountLine
		}
		if l.TransactionCurrency != "" && !strings.EqualFold(l.TransactionCurrency, l.TransactionAmount.Currency()) {
			return financialtypes.ErrCurrencyMismatch
		}
		if l.Debit.Currency() != "" && !l.Debit.Amount().IsZero() &&
			(!strings.EqualFold(l.Debit.Currency(), l.TransactionAmount.Currency()) ||
				!l.Debit.Amount().Equal(l.TransactionAmount.Amount())) {
			return financialtypes.ErrCurrencyMismatch
		}
		if l.Credit.Currency() != "" && !l.Credit.Amount().IsZero() &&
			(!strings.EqualFold(l.Credit.Currency(), l.TransactionAmount.Currency()) ||
				!l.Credit.Amount().Equal(l.TransactionAmount.Amount())) {
			return financialtypes.ErrCurrencyMismatch
		}
		if l.ExchangeRate.IsZero() && (l.FunctionalDebit.Currency() != "" || l.AmountCurrency.Currency() != "") {
			return financialtypes.ErrInvalidAmount
		}
		if (l.FunctionalDebit.Currency() != "" || l.AmountCurrency.Currency() != "") &&
			!l.ExchangeRate.IsPositive() {
			return financialtypes.ErrInvalidAmount
		}
		if l.FunctionalDebit.Currency() != "" {
			functionalDebit, functionalCredit, functionalCurrency, _ := lineFunctionalAmounts(l)
			expected, err := financialtypes.NewMoney(
				l.TransactionAmount.Amount().Mul(l.ExchangeRate), functionalCurrency,
			)
			if err != nil {
				return err
			}
			expected, err = expected.Rounded()
			if err != nil {
				return err
			}
			actualAmount := functionalDebit
			if functionalCreditSet {
				actualAmount = functionalCredit
			}
			actual, err := financialtypes.NewMoney(actualAmount, functionalCurrency)
			if err != nil {
				return err
			}
			actual, err = actual.Rounded()
			if err != nil {
				return err
			}
			if !expected.Equal(actual) {
				return financialtypes.ErrInvalidAmount
			}
		}
	}
	if (l.FunctionalDebit.Currency() != "" || l.FunctionalCredit.Currency() != "") &&
		l.TransactionAmount.Currency() == "" {
		return financialtypes.ErrInvalidAmount
	}
	return nil
}

type JournalEntry struct {
	CompanyID            uuid.UUID
	ID                   uuid.UUID
	SequenceNumber       int64
	LegalNumber          string
	JournalID            uuid.UUID
	PeriodID             uuid.UUID
	EntryDate            time.Time
	Date                 time.Time
	Status               EntryStatus
	DocumentReference    string
	Reference            string
	CreatedBy            string
	PostedBy             string
	PostedAt             *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ReversedEntryID      *uuid.UUID
	OriginalEntryID      *uuid.UUID
	TargetPeriodNumber   int
	AdjustmentAuthorized bool
	Lines                []JournalEntryLine
}

func NewJournalEntry(journalID uuid.UUID, date time.Time, createdBy string, lines []JournalEntryLine) (JournalEntry, error) {
	entry := JournalEntry{
		ID:        uuid.New(),
		JournalID: journalID,
		EntryDate: date.UTC(),
		Date:      date.UTC(),
		Status:    EntryDraft,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
		Lines:     append([]JournalEntryLine(nil), lines...),
	}
	if err := entry.Validate(); err != nil {
		return JournalEntry{}, err
	}
	return entry, nil
}

func (e *JournalEntry) Validate() error {
	if len(e.Lines) < 2 {
		return ErrMinimumLines
	}

	totals := make(map[string]decimal.Decimal)
	currencies := make(map[string]struct{})
	hasDebit, hasCredit := false, false
	hasFunctionalAmounts := false
	linesWithFunctionalAmounts := 0
	for _, line := range e.Lines {
		if err := line.Validate(); err != nil {
			return err
		}
		if e.CompanyID != uuid.Nil && line.CompanyID != uuid.Nil && line.CompanyID != e.CompanyID {
			return ErrInvalidEntry
		}
		if line.AccountID == uuid.Nil {
			return ErrInvalidEntry
		}
		if line.Debit.Currency() != "" {
			currency := line.Debit.Currency()
			currencies[currency] = struct{}{}
			hasDebit = true
		}
		if line.Credit.Currency() != "" {
			currency := line.Credit.Currency()
			currencies[currency] = struct{}{}
			hasCredit = true
		}
		functionalDebit, functionalCredit, functionalCurrency, hasFunctional := lineFunctionalAmounts(line)
		if hasFunctional {
			hasFunctionalAmounts = true
			linesWithFunctionalAmounts++
			totals[functionalCurrency] = totals[functionalCurrency].Add(functionalDebit).Sub(functionalCredit)
		} else {
			currency := line.Debit.Currency()
			if line.Credit.Currency() != "" {
				currency = line.Credit.Currency()
			}
			totals[currency] = totals[currency].Add(line.Debit.Amount()).Sub(line.Credit.Amount())
		}
	}
	if !hasDebit || !hasCredit {
		return ErrMinimumLines
	}

	if hasFunctionalAmounts {
		if linesWithFunctionalAmounts != len(e.Lines) || len(totals) != 1 {
			return financialtypes.ErrCurrencyMismatch
		}
	} else if len(currencies) != 1 {
		return financialtypes.ErrCurrencyMismatch
	}
	for _, total := range totals {
		if !total.IsZero() {
			return financialtypes.ErrUnbalancedMove
		}
	}
	return nil
}

func lineFunctionalAmounts(line JournalEntryLine) (debit, credit decimal.Decimal, currency string, present bool) {
	if line.FunctionalDebit.Currency() != "" || line.FunctionalCredit.Currency() != "" {
		currency = line.FunctionalDebit.Currency()
		if currency == "" {
			currency = line.FunctionalCredit.Currency()
		}
		return line.FunctionalDebit.Amount(), line.FunctionalCredit.Amount(), currency, true
	}
	if line.AmountCurrency.Currency() != "" {
		currency = line.AmountCurrency.Currency()
		if line.Debit.Currency() != "" && !line.Debit.Amount().IsZero() {
			return line.AmountCurrency.Amount(), decimal.Zero, currency, true
		}
		return decimal.Zero, line.AmountCurrency.Amount(), currency, true
	}
	return decimal.Zero, decimal.Zero, "", false
}

func (e *JournalEntry) EnsureImmutable() error {
	if e.Status == EntryPosted || e.Status == EntryReversed {
		return financialtypes.ErrCannotModifyPostedMove
	}
	return nil
}

func DraftContentMatches(saved, candidate JournalEntry) bool {
	savedDate := saved.EntryDate
	if savedDate.IsZero() {
		savedDate = saved.Date
	}
	candidateDate := candidate.EntryDate
	if candidateDate.IsZero() {
		candidateDate = candidate.Date
	}
	return saved.ID == candidate.ID &&
		saved.CompanyID == candidate.CompanyID &&
		saved.JournalID == candidate.JournalID &&
		savedDate.Equal(candidateDate) &&
		saved.CreatedBy == candidate.CreatedBy &&
		saved.DocumentReference == candidate.DocumentReference &&
		saved.Reference == candidate.Reference &&
		reflect.DeepEqual(saved.Lines, candidate.Lines)
}
