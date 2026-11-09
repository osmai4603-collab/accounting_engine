package posting

import (
	"errors"
	"sync"
	"time"

	"accounting_engine/internal/masterdata"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrEntryNotFound                         = errors.New("journal entry not found")
	ErrInvalidEntryState                     = errors.New("invalid journal entry state")
	ErrAdjustmentPeriodAuthorizationRequired = errors.New("adjustment period requires explicit authorization")
	ErrFunctionalCurrencyMismatch            = errors.New("journal entry functional currency does not match company")
)

var (
	ErrMoveAlreadyPosted      = financialtypes.ErrMoveAlreadyPosted
	ErrCannotModifyPostedMove = financialtypes.ErrCannotModifyPostedMove
	ErrMakerCheckerViolation  = financialtypes.ErrMakerCheckerViolation
)

type Engine struct {
	repo       Repository
	masterdata *masterdata.Service
	mu         sync.Mutex

	MakerCheckerThreshold decimal.Decimal
}

func NewEngine(repo Repository, masterData *masterdata.Service) *Engine {
	return &Engine{repo: repo, masterdata: masterData}
}

func (e *Engine) CreateDraft(entry JournalEntry) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	entry.Status = EntryDraft
	if entry.CompanyID != uuid.Nil {
		for i := range entry.Lines {
			if entry.Lines[i].CompanyID == uuid.Nil {
				entry.Lines[i].CompanyID = entry.CompanyID
			}
		}
	}
	if entry.EntryDate.IsZero() {
		entry.EntryDate = entry.Date
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.UpdatedAt = entry.CreatedAt
	if err := entry.Validate(); err != nil {
		return err
	}
	return e.repo.SaveDraft(entry)
}

func (e *Engine) SaveDraft(entry JournalEntry) error {
	return e.CreateDraft(entry)
}

func (e *Engine) Post(entryID uuid.UUID, posterID string) error {
	entry, err := e.repo.GetEntryByID(entryID)
	if err != nil {
		return err
	}
	return e.PostEntry(&entry, posterID)
}

func (e *Engine) PostEntry(entry *JournalEntry, posterID string) error {
	if entry.Status != EntryDraft {
		if entry.Status == EntryPosted {
			return financialtypes.ErrMoveAlreadyPosted
		}
		return ErrEntryNotDraft
	}
	if err := e.prepareForPosting(entry, posterID); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now().UTC()
	entry.PostedBy = posterID
	entry.PostedAt = &now
	entry.Status = EntryPosted
	entry.UpdatedAt = now
	if repository, ok := e.repo.(AtomicPostingRepository); ok {
		posted, err := repository.PostAndSequence(*entry)
		if err != nil {
			entry.Status = EntryDraft
			entry.PostedBy = ""
			entry.PostedAt = nil
			return err
		}
		*entry = posted
		return nil
	}
	sequence, err := e.repo.GetNextSequence(entry.JournalID)
	if err != nil {
		entry.Status = EntryDraft
		entry.PostedBy = ""
		entry.PostedAt = nil
		return err
	}
	entry.SequenceNumber = sequence
	if err := e.repo.PostAtomic(*entry); err != nil {
		entry.Status = EntryDraft
		entry.PostedBy = ""
		entry.PostedAt = nil
		entry.SequenceNumber = 0
		return err
	}
	return nil
}

func (e *Engine) prepareForPosting(entry *JournalEntry, posterID string) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	if e.masterdata != nil {
		if entry.CompanyID != uuid.Nil {
			company, err := e.masterdata.GetCompanyByID(entry.CompanyID)
			if err != nil {
				return err
			}
			if !company.Active {
				return masterdata.ErrCompanyInactive
			}
			if err := validateFunctionalCurrency(*entry, company.FunctionalCurrency); err != nil {
				return err
			}
		}
		if entry.TargetPeriodNumber == 13 || entry.TargetPeriodNumber == 14 {
			if !entry.AdjustmentAuthorized {
				return ErrAdjustmentPeriodAuthorizationRequired
			}
			var period masterdata.FiscalPeriod
			var err error
			if entry.CompanyID != uuid.Nil {
				period, err = e.masterdata.GetOpenAdjustmentPeriodForCompanyDate(entry.CompanyID, effectiveDate(entry), entry.TargetPeriodNumber)
			} else {
				period, err = e.masterdata.GetOpenAdjustmentPeriodForDate(effectiveDate(entry), entry.TargetPeriodNumber)
			}
			if err != nil {
				return err
			}
			entry.PeriodID = period.ID
		} else if entry.TargetPeriodNumber != 0 {
			return masterdata.ErrFiscalPeriodNotFound
		} else {
			var period masterdata.FiscalPeriod
			var err error
			if entry.CompanyID != uuid.Nil {
				period, err = e.masterdata.GetOpenPeriodForCompanyDate(entry.CompanyID, effectiveDate(entry))
			} else {
				period, err = e.masterdata.GetOpenPeriodForDate(effectiveDate(entry))
			}
			if err != nil {
				return err
			}
			entry.PeriodID = period.ID
		}
		for _, line := range entry.Lines {
			var err error
			if entry.CompanyID != uuid.Nil {
				err = e.masterdata.ValidateCanPostToAccountForCompany(entry.CompanyID, line.AccountID)
			} else {
				err = e.masterdata.ValidateCanPostToAccount(line.AccountID)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := e.checkMakerChecker(entry, posterID); err != nil {
		return err
	}
	return nil
}

func validateFunctionalCurrency(entry JournalEntry, expectedCurrency string) error {
	for _, line := range entry.Lines {
		functionalCurrency := line.FunctionalDebit.Currency()
		if functionalCurrency == "" {
			functionalCurrency = line.FunctionalCredit.Currency()
		}
		if functionalCurrency == "" {
			functionalCurrency = line.AmountCurrency.Currency()
		}
		if functionalCurrency == "" {
			transactionCurrency := line.TransactionCurrency
			if transactionCurrency == "" {
				transactionCurrency = line.TransactionAmount.Currency()
			}
			if transactionCurrency == "" {
				transactionCurrency = line.Debit.Currency()
				if transactionCurrency == "" || line.Debit.Amount().IsZero() {
					transactionCurrency = line.Credit.Currency()
				}
			}
			functionalCurrency = transactionCurrency
		}
		if functionalCurrency != expectedCurrency {
			return ErrFunctionalCurrencyMismatch
		}
	}
	return nil
}

func effectiveDate(entry *JournalEntry) time.Time {
	if !entry.EntryDate.IsZero() {
		return entry.EntryDate
	}
	return entry.Date
}

func (e *Engine) checkMakerChecker(entry *JournalEntry, posterID string) error {
	if e.MakerCheckerThreshold.IsZero() || posterID == "" {
		return nil
	}
	total := decimal.Zero
	currency := ""
	for _, line := range entry.Lines {
		if line.Debit.Currency() != "" {
			currency = line.Debit.Currency()
			total = total.Add(line.Debit.Amount())
		}
		if line.Credit.Currency() != "" && currency == "" {
			currency = line.Credit.Currency()
		}
	}
	if !total.GreaterThan(e.MakerCheckerThreshold) {
		return nil
	}
	if entry.CreatedBy == posterID {
		return financialtypes.ErrMakerCheckerViolation
	}
	return nil
}

func (e *Engine) SetMakerCheckerThreshold(threshold decimal.Decimal) {
	e.MakerCheckerThreshold = threshold
}

func (e *Engine) GetEntry(entryID uuid.UUID) (JournalEntry, error) {
	return e.repo.GetEntryByID(entryID)
}
