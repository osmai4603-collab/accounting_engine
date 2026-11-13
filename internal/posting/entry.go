package posting

import (
	"errors"
	"fmt"
	"strings"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidEntry               = errors.New("invalid journal entry")
	ErrMinimumLines               = errors.New("journal entry must contain at least two lines")
	ErrLineMustHaveOneSide        = errors.New("journal entry line cannot have debit and credit amounts")
	ErrEntryNotDraft              = errors.New("journal entry is not a draft")
	ErrEntryNotPosted             = errors.New("journal entry is not posted")
	ErrEntryAlreadyReversed       = errors.New("journal entry already reversed")
	ErrInvalidReversal            = errors.New("invalid reversal entry")
	ErrMissingTransactionCurrency = errors.New("transaction currency is required")
	ErrMissingFunctionalCurrency  = errors.New("functional currency is required")
	ErrInvalidExchangeRate        = errors.New("exchange rate must be strictly positive")
	ErrFunctionalBalanceMismatch  = errors.New("functional debit and credit totals must balance")
	ErrAtomicReversalRequired     = errors.New("repository does not support atomic reversal")
	ErrAtomicBulkPostingRequired  = errors.New("repository does not support atomic bulk posting")
)

type EntryStatus string

type EntryClassification string

const (
	EntryDraft    EntryStatus = "draft"
	EntryPosted   EntryStatus = "posted"
	EntryReversed EntryStatus = "reversed"
)

const (
	EntryClassRegular   EntryClassification = "regular"
	EntryClassAdjusting EntryClassification = "adjusting"
	EntryClassClosing   EntryClassification = "closing"
	EntryClassOpening   EntryClassification = "opening"
	EntryClassReversing EntryClassification = "reversing"
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
	SegmentID        *uuid.UUID
	BusinessUnitID   *uuid.UUID
	MatchingNumber   string
	ReconciledAmount decimal.Decimal
	// Deprecated: use FunctionalDebit and FunctionalCredit.
	Debit financialtypes.Money
	// Deprecated: use FunctionalDebit and FunctionalCredit.
	Credit              financialtypes.Money
	TransactionAmount   financialtypes.Money
	TransactionCurrency string
	ExchangeRate        decimal.Decimal
	FunctionalDebit     financialtypes.Money
	FunctionalCredit    financialtypes.Money
	// Deprecated: use FunctionalDebit and FunctionalCredit.
	AmountCurrency financialtypes.Money
	// Deprecated: use TransactionCurrency.
	Currency string
	// Deprecated: use TransactionAmount.
	ForeignAmount decimal.Decimal
}

func (l JournalEntryLine) Validate() error {
	return l.ValidateWithRounding(nil)
}

func (l JournalEntryLine) ValidateWithRounding(round func(financialtypes.Money) (financialtypes.Money, error)) error {
	resolved, err := normalizeLineCurrencies(l)
	if err != nil {
		return err
	}
	if resolved.TransactionAmount.IsNegative() || resolved.FunctionalDebit.IsNegative() ||
		resolved.FunctionalCredit.IsNegative() {
		return financialtypes.ErrNegativeAmountProhibited
	}
	if resolved.TransactionCurrency == "" {
		return ErrMissingTransactionCurrency
	}
	if resolved.TransactionAmount.Amount().IsZero() {
		return financialtypes.ErrZeroAmountLine
	}
	if resolved.FunctionalDebit.Currency() == "" || resolved.FunctionalCredit.Currency() == "" {
		return ErrMissingFunctionalCurrency
	}
	if resolved.FunctionalDebit.Currency() != resolved.FunctionalCredit.Currency() {
		return financialtypes.ErrCurrencyMismatch
	}
	if resolved.FunctionalDebit.Amount().IsZero() == resolved.FunctionalCredit.Amount().IsZero() {
		if resolved.FunctionalDebit.Amount().IsZero() {
			return financialtypes.ErrZeroAmountLine
		}
		return ErrLineMustHaveOneSide
	}
	if !resolved.ExchangeRate.IsPositive() {
		return ErrInvalidExchangeRate
	}
	expected, err := financialtypes.NewMoney(
		resolved.TransactionAmount.Amount().Mul(resolved.ExchangeRate),
		resolved.FunctionalDebit.Currency(),
	)
	if err != nil {
		return err
	}
	if round != nil {
		expected, err = round(expected)
	} else {
		expected, err = expected.Rounded()
	}
	if err != nil {
		return err
	}
	actual := resolved.FunctionalDebit
	if resolved.FunctionalCredit.Amount().IsPositive() {
		actual = resolved.FunctionalCredit
	}
	if round != nil {
		actual, err = round(actual)
	} else {
		actual, err = actual.Rounded()
	}
	if err != nil {
		return err
	}
	if !expected.Equal(actual) {
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
	SourceModule         string
	DocumentReference    string
	Reference            string
	TransactionCurrency  string
	ExchangeRate         decimal.Decimal
	ApprovalFingerprint  string
	CreatedBy            string
	PostedBy             string
	PostedAt             *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ReversedEntryID      *uuid.UUID
	OriginalEntryID      *uuid.UUID
	SourceEventID        *uuid.UUID
	TargetPeriodNumber   int
	AdjustmentAuthorized bool
	Classification       EntryClassification
	Lines                []JournalEntryLine
}

type AccountPeriodBalance struct {
	AccountID       uuid.UUID
	BeginningDebit  decimal.Decimal
	BeginningCredit decimal.Decimal
	PeriodDebit     decimal.Decimal
	PeriodCredit    decimal.Decimal
	EndingDebit     decimal.Decimal
	EndingCredit    decimal.Decimal
}

func LineFunctionalAmounts(line JournalEntryLine) (decimal.Decimal, decimal.Decimal, string) {
	resolved, err := normalizeLineCurrencies(line)
	if err == nil {
		return resolved.FunctionalDebit.Amount(), resolved.FunctionalCredit.Amount(),
			resolved.FunctionalDebit.Currency()
	}
	debit, credit, currency, present := lineFunctionalAmounts(line)
	if present {
		return debit, credit, currency
	}
	if !line.Debit.Amount().IsZero() {
		return line.Debit.Amount(), decimal.Zero, line.Debit.Currency()
	}
	return decimal.Zero, line.Credit.Amount(), line.Credit.Currency()
}

func NewJournalEntry(journalID uuid.UUID, date time.Time, createdBy string, lines []JournalEntryLine) (JournalEntry, error) {
	entry := JournalEntry{
		ID:             uuid.New(),
		JournalID:      journalID,
		EntryDate:      date.UTC(),
		Date:           date.UTC(),
		Status:         EntryDraft,
		CreatedBy:      createdBy,
		CreatedAt:      time.Now().UTC(),
		Classification: EntryClassRegular,
		Lines:          append([]JournalEntryLine(nil), lines...),
	}
	if err := entry.Validate(); err != nil {
		return JournalEntry{}, err
	}
	return entry, nil
}

func (e *JournalEntry) Validate() error {
	return e.ValidateWithRounding(nil)
}

func (e *JournalEntry) ValidateWithRounding(round func(financialtypes.Money) (financialtypes.Money, error)) error {
	if len(e.Lines) < 2 {
		return ErrMinimumLines
	}
	if e.SourceEventID != nil && *e.SourceEventID == uuid.Nil {
		return ErrInvalidEntry
	}
	if e.Classification != "" {
		switch e.Classification {
		case EntryClassRegular, EntryClassAdjusting, EntryClassClosing, EntryClassOpening, EntryClassReversing:
		default:
			return ErrInvalidEntry
		}
	}

	totals := make(map[string]decimal.Decimal)
	hasDebit, hasCredit, layered := false, false, false
	for _, line := range e.Lines {
		if err := line.ValidateWithRounding(round); err != nil {
			return err
		}
		resolved, err := normalizeLineCurrencies(line)
		if err != nil {
			return err
		}
		if e.CompanyID != uuid.Nil && line.CompanyID != uuid.Nil && line.CompanyID != e.CompanyID {
			return ErrInvalidEntry
		}
		if line.AccountID == uuid.Nil {
			return ErrInvalidEntry
		}
		hasDebit = hasDebit || resolved.FunctionalDebit.Amount().IsPositive()
		hasCredit = hasCredit || resolved.FunctionalCredit.Amount().IsPositive()
		currency := resolved.FunctionalDebit.Currency()
		totals[currency] = totals[currency].Add(
			resolved.FunctionalDebit.Amount().Sub(resolved.FunctionalCredit.Amount()),
		)
		layered = layered || hasExplicitCurrencyLayers(line)
	}
	if !hasDebit || !hasCredit {
		return ErrMinimumLines
	}
	if len(totals) != 1 {
		return financialtypes.ErrCurrencyMismatch
	}
	for _, total := range totals {
		if !total.IsZero() {
			if layered {
				return fmt.Errorf("%w: %w", ErrFunctionalBalanceMismatch, financialtypes.ErrUnbalancedMove)
			}
			return financialtypes.ErrUnbalancedMove
		}
	}
	return nil
}

func NormalizeLineCurrencies(line JournalEntryLine) (JournalEntryLine, error) {
	return normalizeLineCurrencies(line)
}

func normalizeLineCurrencies(line JournalEntryLine) (JournalEntryLine, error) {
	resolved := line
	legacySide := line.Debit
	if legacySide.Amount().IsZero() {
		legacySide = line.Credit
	}
	transactionCurrency := strings.ToUpper(strings.TrimSpace(line.TransactionCurrency))
	if transactionCurrency == "" {
		transactionCurrency = strings.ToUpper(strings.TrimSpace(line.Currency))
	}
	if transactionCurrency == "" && line.TransactionAmount.Currency() != "" {
		transactionCurrency = line.TransactionAmount.Currency()
	}
	if transactionCurrency == "" && legacySide.Currency() != "" {
		transactionCurrency = legacySide.Currency()
	}
	if transactionCurrency == "" && !line.ForeignAmount.IsZero() {
		transactionCurrency = line.TransactionCurrency
	}
	if line.Currency != "" && transactionCurrency != "" &&
		!strings.EqualFold(strings.TrimSpace(line.Currency), transactionCurrency) {
		return JournalEntryLine{}, financialtypes.ErrCurrencyMismatch
	}
	if _, ok := financialtypes.LookupCurrency(transactionCurrency); !ok {
		if transactionCurrency == "" {
			return JournalEntryLine{}, ErrMissingTransactionCurrency
		}
		return JournalEntryLine{}, financialtypes.ErrInvalidCurrency
	}

	transactionAmount := line.TransactionAmount
	if transactionAmount.Currency() == "" {
		switch {
		case !legacySide.Amount().IsZero():
			transactionAmount = legacySide
		case !line.ForeignAmount.IsZero():
			var err error
			transactionAmount, err = financialtypes.NewMoney(line.ForeignAmount.Abs(), transactionCurrency)
			if err != nil {
				return JournalEntryLine{}, err
			}
		}
	}
	if transactionAmount.Currency() == "" || transactionAmount.Amount().IsZero() {
		return JournalEntryLine{}, financialtypes.ErrZeroAmountLine
	}
	if transactionAmount.Currency() != transactionCurrency {
		return JournalEntryLine{}, financialtypes.ErrCurrencyMismatch
	}
	if !line.ForeignAmount.IsZero() && !line.ForeignAmount.Abs().Equal(transactionAmount.Amount()) {
		return JournalEntryLine{}, financialtypes.ErrCurrencyMismatch
	}

	functionalDebit, functionalCredit := line.FunctionalDebit, line.FunctionalCredit
	if functionalDebit.Currency() == "" && functionalCredit.Currency() == "" {
		switch {
		case line.AmountCurrency.Currency() != "":
			if !legacySide.Amount().IsZero() && line.Debit.Amount().IsPositive() {
				functionalDebit = line.AmountCurrency
			} else {
				functionalCredit = line.AmountCurrency
			}
		case !hasExplicitCurrencyLayers(line):
			functionalDebit, functionalCredit = line.Debit, line.Credit
		default:
			return JournalEntryLine{}, ErrMissingFunctionalCurrency
		}
	}
	functionalCurrency := functionalDebit.Currency()
	if functionalCurrency == "" {
		functionalCurrency = functionalCredit.Currency()
	}
	if functionalCurrency == "" {
		return JournalEntryLine{}, ErrMissingFunctionalCurrency
	}
	if functionalDebit.Currency() == "" {
		var err error
		functionalDebit, err = financialtypes.Zero(functionalCurrency)
		if err != nil {
			return JournalEntryLine{}, err
		}
	}
	if functionalCredit.Currency() == "" {
		var err error
		functionalCredit, err = financialtypes.Zero(functionalCurrency)
		if err != nil {
			return JournalEntryLine{}, err
		}
	}

	exchangeRate := line.ExchangeRate
	if exchangeRate.IsZero() {
		if hasExplicitCurrencyLayers(line) {
			return JournalEntryLine{}, ErrInvalidExchangeRate
		}
		functionalAmount := functionalDebit.Amount()
		if functionalAmount.IsZero() {
			functionalAmount = functionalCredit.Amount()
		}
		exchangeRate = functionalAmount.Div(transactionAmount.Amount())
	}

	resolved.TransactionCurrency = transactionCurrency
	resolved.TransactionAmount = transactionAmount
	resolved.ExchangeRate = exchangeRate
	resolved.FunctionalDebit = functionalDebit
	resolved.FunctionalCredit = functionalCredit
	return resolved, nil
}

func hasExplicitCurrencyLayers(line JournalEntryLine) bool {
	return line.TransactionCurrency != "" || line.TransactionAmount.Currency() != "" ||
		!line.ExchangeRate.IsZero() || line.FunctionalDebit.Currency() != "" ||
		line.FunctionalCredit.Currency() != ""
}

func lineFunctionalAmounts(line JournalEntryLine) (debit, credit decimal.Decimal, currency string, present bool) {
	resolved, err := normalizeLineCurrencies(line)
	if err == nil {
		return resolved.FunctionalDebit.Amount(), resolved.FunctionalCredit.Amount(),
			resolved.FunctionalDebit.Currency(), true
	}
	return decimal.Zero, decimal.Zero, "", false
}

func (e *JournalEntry) EnsureImmutable() error {
	if e.Status == EntryPosted || e.Status == EntryReversed {
		return financialtypes.ErrCannotModifyPostedMove
	}
	return nil
}

func equalUUIDPtr(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (l JournalEntryLine) Equal(other JournalEntryLine) bool {
	return (l.CompanyID == other.CompanyID || l.CompanyID == uuid.Nil || other.CompanyID == uuid.Nil) &&
		l.ID == other.ID &&
		l.AccountID == other.AccountID &&
		equalUUIDPtr(l.PartnerID, other.PartnerID) &&
		equalUUIDPtr(l.CostCenterID, other.CostCenterID) &&
		equalUUIDPtr(l.ProjectID, other.ProjectID) &&
		equalUUIDPtr(l.SegmentID, other.SegmentID) &&
		equalUUIDPtr(l.BusinessUnitID, other.BusinessUnitID) &&
		l.MatchingNumber == other.MatchingNumber &&
		l.ReconciledAmount.Equal(other.ReconciledAmount) &&
		l.Debit.Equal(other.Debit) &&
		l.Credit.Equal(other.Credit) &&
		l.TransactionAmount.Equal(other.TransactionAmount) &&
		l.TransactionCurrency == other.TransactionCurrency &&
		(l.ExchangeRate.Equal(other.ExchangeRate) || (l.ExchangeRate.IsZero() && other.ExchangeRate.IsZero())) &&
		l.FunctionalDebit.Equal(other.FunctionalDebit) &&
		l.FunctionalCredit.Equal(other.FunctionalCredit) &&
		l.AmountCurrency.Equal(other.AmountCurrency) &&
		l.Currency == other.Currency &&
		l.ForeignAmount.Equal(other.ForeignAmount)
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
	if saved.ID != candidate.ID ||
		saved.CompanyID != candidate.CompanyID ||
		saved.JournalID != candidate.JournalID ||
		(saved.PeriodID != uuid.Nil && candidate.PeriodID != uuid.Nil && saved.PeriodID != candidate.PeriodID) ||
		!savedDate.Equal(candidateDate) ||
		saved.CreatedBy != candidate.CreatedBy ||
		saved.SourceModule != candidate.SourceModule ||
		saved.DocumentReference != candidate.DocumentReference ||
		saved.Reference != candidate.Reference ||
		saved.TransactionCurrency != candidate.TransactionCurrency ||
		!(saved.ExchangeRate.Equal(candidate.ExchangeRate) ||
			(saved.ExchangeRate.IsZero() && candidate.ExchangeRate.IsZero())) ||
		!equalUUIDPointer(saved.SourceEventID, candidate.SourceEventID) ||
		!equalUUIDPointer(saved.OriginalEntryID, candidate.OriginalEntryID) ||
		saved.TargetPeriodNumber != candidate.TargetPeriodNumber ||
		saved.AdjustmentAuthorized != candidate.AdjustmentAuthorized ||
		normalizedEntryClassification(saved.Classification) != normalizedEntryClassification(candidate.Classification) ||
		len(saved.Lines) != len(candidate.Lines) {
		return false
	}

	for i := range saved.Lines {
		if !saved.Lines[i].Equal(candidate.Lines[i]) {
			return false
		}
	}
	return true
}

func equalUUIDPointer(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func normalizedEntryClassification(classification EntryClassification) EntryClassification {
	if classification == "" {
		return EntryClassRegular
	}
	return classification
}
