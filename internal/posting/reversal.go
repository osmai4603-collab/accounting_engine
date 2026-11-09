package posting

import (
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func (e *Engine) Reverse(entryID uuid.UUID, posterID string) (JournalEntry, error) {
	return e.ReverseDual(entryID, "system-reversal", posterID, time.Now().UTC())
}

func (e *Engine) ReverseAt(entryID uuid.UUID, creatorID string, reversalDate time.Time) (JournalEntry, error) {
	return e.ReverseDual(entryID, creatorID, creatorID, reversalDate)
}

func (e *Engine) ReverseDual(entryID uuid.UUID, creatorID, posterID string, reversalDate time.Time) (JournalEntry, error) {
	original, err := e.repo.GetEntryByID(entryID)
	if err != nil {
		return JournalEntry{}, err
	}
	if original.Status != EntryPosted {
		return JournalEntry{}, ErrEntryNotPosted
	}
	if original.ReversedEntryID != nil {
		return JournalEntry{}, ErrEntryAlreadyReversed
	}

	reversal := JournalEntry{
		CompanyID:         original.CompanyID,
		ID:                uuid.New(),
		JournalID:         original.JournalID,
		EntryDate:         reversalDate.UTC(),
		Date:              reversalDate.UTC(),
		Status:            EntryDraft,
		DocumentReference: original.DocumentReference,
		Reference:         original.Reference,
		CreatedBy:         creatorID,
		CreatedAt:         time.Now().UTC(),
		OriginalEntryID:   &original.ID,
		Lines:             make([]JournalEntryLine, len(original.Lines)),
	}
	for i, line := range original.Lines {
		reversal.Lines[i] = JournalEntryLine{
			CompanyID:           line.CompanyID,
			ID:                  uuid.New(),
			AccountID:           line.AccountID,
			PartnerID:           line.PartnerID,
			CostCenterID:        line.CostCenterID,
			ProjectID:           line.ProjectID,
			SegmentID:           line.SegmentID,
			BusinessUnitID:      line.BusinessUnitID,
			Debit:               line.Credit,
			Credit:              line.Debit,
			TransactionAmount:   line.TransactionAmount,
			TransactionCurrency: line.TransactionCurrency,
			ExchangeRate:        line.ExchangeRate,
			FunctionalDebit:     line.FunctionalCredit,
			FunctionalCredit:    line.FunctionalDebit,
			AmountCurrency:      line.AmountCurrency,
			Currency:            line.Currency,
			ForeignAmount:       line.ForeignAmount,
		}
	}
	if err := reversal.Validate(); err != nil {
		return JournalEntry{}, err
	}
	if repository, ok := e.repo.(AtomicReversalRepository); ok {
		if err := e.prepareForPosting(&reversal, posterID); err != nil {
			return JournalEntry{}, err
		}
		if err := e.repo.SaveDraft(reversal); err != nil {
			return JournalEntry{}, err
		}
		now := time.Now().UTC()
		reversal.Status = EntryPosted
		reversal.PostedBy = posterID
		reversal.PostedAt = &now
		reversal.UpdatedAt = now
		e.mu.Lock()
		defer e.mu.Unlock()
		return repository.PostReversalAtomic(original, reversal)
	}
	if err := e.repo.SaveDraft(reversal); err != nil {
		return JournalEntry{}, err
	}
	if err := e.PostEntry(&reversal, posterID); err != nil {
		return JournalEntry{}, err
	}

	reversalID := reversal.ID
	original.ReversedEntryID = &reversalID
	original.Status = EntryReversed
	original.UpdatedAt = time.Now().UTC()
	if err := e.repo.PostAtomic(original); err != nil {
		return JournalEntry{}, err
	}
	return reversal, nil
}

var _ = financialtypes.ErrCannotModifyPostedMove
