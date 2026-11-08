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
	ErrEntryNotFound     = errors.New("journal entry not found")
	ErrInvalidEntryState = errors.New("invalid journal entry state")
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
	if err := entry.Validate(); err != nil {
		return err
	}
	if e.masterdata != nil {
		period, err := e.masterdata.GetOpenPeriodForDate(effectiveDate(entry))
		if err != nil {
			return err
		}
		entry.PeriodID = period.ID
		for _, line := range entry.Lines {
			if err := e.masterdata.ValidateCanPostToAccount(line.AccountID); err != nil {
				return err
			}
		}
	}
	if err := e.checkMakerChecker(entry, posterID); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	sequence, err := e.repo.GetNextSequence(entry.JournalID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	entry.SequenceNumber = sequence
	entry.PostedBy = posterID
	entry.PostedAt = &now
	entry.Status = EntryPosted
	entry.UpdatedAt = now
	if err := e.repo.PostAtomic(*entry); err != nil {
		entry.Status = EntryDraft
		entry.PostedBy = ""
		entry.PostedAt = nil
		entry.SequenceNumber = 0
		return err
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
