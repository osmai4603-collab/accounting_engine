package posting

import (
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func (e *Engine) Reverse(entryID uuid.UUID, posterID string) (JournalEntry, error) {
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
		ID:                uuid.New(),
		JournalID:         original.JournalID,
		EntryDate:         time.Now().UTC(),
		Date:              time.Now().UTC(),
		Status:            EntryDraft,
		DocumentReference: original.DocumentReference,
		Reference:         original.Reference,
		CreatedBy:         posterID,
		CreatedAt:         time.Now().UTC(),
		OriginalEntryID:   &original.ID,
		Lines:             make([]JournalEntryLine, len(original.Lines)),
	}
	for i, line := range original.Lines {
		reversal.Lines[i] = JournalEntryLine{
			AccountID:      line.AccountID,
			PartnerID:      line.PartnerID,
			Debit:          line.Credit,
			Credit:         line.Debit,
			AmountCurrency: line.AmountCurrency,
			Currency:       line.Currency,
			ForeignAmount:  line.ForeignAmount,
		}
	}
	if err := reversal.Validate(); err != nil {
		return JournalEntry{}, err
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
