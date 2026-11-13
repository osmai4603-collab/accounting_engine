package posting

import (
	"context"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
)

func (e *Engine) Reverse(companyID, entryID uuid.UUID, posterID string) (JournalEntry, error) {
	return e.ReverseDual(companyID, entryID, "system-reversal", posterID, time.Now().UTC())
}

func (e *Engine) ReverseAt(companyID, entryID uuid.UUID, creatorID string, reversalDate time.Time) (JournalEntry, error) {
	return e.ReverseDual(companyID, entryID, creatorID, creatorID, reversalDate)
}

func (e *Engine) ReverseDual(companyID, entryID uuid.UUID, creatorID, posterID string, reversalDate time.Time) (JournalEntry, error) {
	return e.ReverseDualContext(context.Background(), companyID, entryID, creatorID, posterID, reversalDate)
}

func (e *Engine) ReverseDualContext(ctx context.Context, companyID, entryID uuid.UUID, creatorID, posterID string, reversalDate time.Time) (JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return JournalEntry{}, err
	}
	original, err := e.repo.GetEntryByIDForCompany(ctx, companyID, entryID)
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
		Classification:    EntryClassReversing,
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
	contextRepository, hasContextRepository := e.repo.(ContextAtomicReversalRepository)
	atomicRepository, hasAtomicRepository := e.repo.(AtomicReversalRepository)
	if !hasContextRepository && !hasAtomicRepository {
		return JournalEntry{}, ErrAtomicReversalRequired
	}
	if err := e.prepareForPosting(ctx, &reversal, posterID); err != nil {
		return JournalEntry{}, err
	}
	if err := e.repo.SaveDraftContext(ctx, reversal); err != nil {
		return JournalEntry{}, err
	}
	now := time.Now().UTC()
	reversal.Status = EntryPosted
	reversal.PostedBy = posterID
	reversal.PostedAt = &now
	reversal.UpdatedAt = now
	e.mu.Lock()
	defer e.mu.Unlock()
	if hasContextRepository {
		return contextRepository.PostReversalAtomicContext(ctx, original, reversal)
	}
	return atomicRepository.PostReversalAtomic(original, reversal)
}

var _ = financialtypes.ErrCannotModifyPostedMove
