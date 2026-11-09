package posting

import (
	"errors"
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
	CompanyID        uuid.UUID
	ID               uuid.UUID
	AccountID        uuid.UUID
	PartnerID        *uuid.UUID
	CostCenterID     *uuid.UUID
	ProjectID        *uuid.UUID
	BusinessUnitID   *uuid.UUID
	MatchingNumber   string
	ReconciledAmount decimal.Decimal
	Debit            financialtypes.Money
	Credit           financialtypes.Money
	AmountCurrency   financialtypes.Money
	Currency         string
	ForeignAmount    decimal.Decimal
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
	return nil
}

type JournalEntry struct {
	CompanyID         uuid.UUID
	ID                uuid.UUID
	SequenceNumber    int64
	LegalNumber       string
	JournalID         uuid.UUID
	PeriodID          uuid.UUID
	EntryDate         time.Time
	Date              time.Time
	Status            EntryStatus
	DocumentReference string
	Reference         string
	CreatedBy         string
	PostedBy          string
	PostedAt          *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ReversedEntryID   *uuid.UUID
	OriginalEntryID   *uuid.UUID
	Lines             []JournalEntryLine
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
	for _, line := range e.Lines {
		if err := line.Validate(); err != nil {
			return err
		}
		if line.AccountID == uuid.Nil {
			return ErrInvalidEntry
		}
		if line.Debit.Currency() != "" {
			currency := line.Debit.Currency()
			currencies[currency] = struct{}{}
			totals[currency] = totals[currency].Add(line.Debit.Amount())
			hasDebit = true
		}
		if line.Credit.Currency() != "" {
			currency := line.Credit.Currency()
			currencies[currency] = struct{}{}
			totals[currency] = totals[currency].Sub(line.Credit.Amount())
			hasCredit = true
		}
	}
	if !hasDebit || !hasCredit {
		return ErrMinimumLines
	}
	for _, total := range totals {
		if !total.IsZero() {
			return financialtypes.ErrUnbalancedMove
		}
	}
	return nil
}

func (e *JournalEntry) EnsureImmutable() error {
	if e.Status == EntryPosted || e.Status == EntryReversed {
		return financialtypes.ErrCannotModifyPostedMove
	}
	return nil
}
