package posting

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (e *Engine) Reverse(companyID, entryID uuid.UUID, posterID string) (JournalEntry, error) {
	return e.ReverseDual(companyID, entryID, posterID, posterID, time.Now().UTC())
}

func (e *Engine) ReverseAt(companyID, entryID uuid.UUID, creatorID string, reversalDate time.Time) (JournalEntry, error) {
	return e.ReverseDual(companyID, entryID, creatorID, creatorID, reversalDate)
}

func (e *Engine) ReverseDual(companyID, entryID uuid.UUID, creatorID, posterID string, reversalDate time.Time) (JournalEntry, error) {
	return e.ReverseDualContext(context.Background(), companyID, entryID, creatorID, posterID, reversalDate)
}

func (e *Engine) ReverseDualContext(ctx context.Context, companyID, entryID uuid.UUID, creatorID, posterID string, reversalDate time.Time) (JournalEntry, error) {
	return e.ReverseWithRequestContext(ctx, companyID, entryID, ReversalRequest{
		ActorID: creatorID, Reason: "", EffectiveDate: reversalDate,
	}, posterID)
}

// ReverseWithReasonContext preserves the legacy API while keeping the actor
// identity separate from the business reason.
func (e *Engine) ReverseWithReasonContext(ctx context.Context, companyID, entryID uuid.UUID, creatorID, posterID, reason string, reversalDate time.Time) (JournalEntry, error) {
	return e.ReverseWithRequestContext(ctx, companyID, entryID, ReversalRequest{
		ActorID: creatorID, Reason: reason, EffectiveDate: reversalDate,
	}, posterID)
}

// ReverseWithRequestContext atomically creates and posts a reversal. The
// repository owns the complete write transaction; no reversal draft is
// persisted before that transaction starts.
func (e *Engine) ReverseWithRequestContext(ctx context.Context, companyID, entryID uuid.UUID, request ReversalRequest, posterID string) (JournalEntry, error) {
	entry, _, err := e.ReverseWithRequestResultContext(ctx, companyID, entryID, request, posterID)
	return entry, err
}

func (e *Engine) ReverseWithRequestResultContext(ctx context.Context, companyID, entryID uuid.UUID, request ReversalRequest, posterID string) (JournalEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return JournalEntry{}, false, err
	}
	request.ActorID = strings.TrimSpace(request.ActorID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	payloadHash, err := ReversalRequestHash(companyID, entryID, request)
	if err != nil {
		return JournalEntry{}, false, err
	}

	if request.IdempotencyKey != "" {
		reader, ok := e.repo.(IdempotentReversalReader)
		if !ok {
			return JournalEntry{}, false, ErrAtomicReversalRequired
		}
		existing, found, err := reader.FindReversalByIdempotencyKeyContext(ctx, companyID, request.IdempotencyKey)
		if err != nil {
			return JournalEntry{}, false, err
		}
		if found {
			if existing.IdempotencyPayloadHash != payloadHash {
				return JournalEntry{}, false, ErrIdempotencyConflict
			}
			return existing, true, nil
		}
	}

	original, err := e.repo.GetEntryByIDForCompany(ctx, companyID, entryID)
	if err != nil {
		return JournalEntry{}, false, err
	}
	if original.ReversedEntryID != nil {
		return JournalEntry{}, false, ErrEntryAlreadyReversed
	}
	if original.Status != EntryPosted {
		return JournalEntry{}, false, ErrEntryNotPosted
	}

	reversalDate := request.EffectiveDate
	if reversalDate.IsZero() {
		reversalDate = time.Now().UTC()
	}
	reference := strings.TrimSpace(request.Reference)
	sourceModule := strings.TrimSpace(request.SourceSystem)
	if sourceModule == "" {
		sourceModule = original.SourceModule
	}
	originalID := original.ID
	now := time.Now().UTC()
	reversal := JournalEntry{
		CompanyID:              original.CompanyID,
		ID:                     uuid.New(),
		JournalID:              original.JournalID,
		EntryDate:              reversalDate.UTC(),
		Date:                   reversalDate.UTC(),
		Status:                 EntryPosted,
		Classification:         EntryClassReversing,
		DocumentReference:      original.DocumentReference,
		Reference:              original.Reference,
		ReversalReference:      reference,
		SourceModule:           sourceModule,
		ReversalReason:         strings.TrimSpace(request.Reason),
		CreatedBy:              request.ActorID,
		CreatedAt:              now,
		UpdatedAt:              now,
		PostedBy:               strings.TrimSpace(posterID),
		OriginalEntryID:        &originalID,
		IdempotencyKey:         request.IdempotencyKey,
		IdempotencyPayloadHash: payloadHash,
		Lines:                  make([]JournalEntryLine, len(original.Lines)),
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
		return JournalEntry{}, false, err
	}
	if err := e.prepareForPosting(ctx, &reversal, reversal.PostedBy); err != nil {
		return JournalEntry{}, false, err
	}
	reversal.Status = EntryPosted
	reversal.PostedAt = &now
	reversal.UpdatedAt = now

	repository, ok := e.repo.(AtomicReversalCreationRepository)
	if !ok {
		return JournalEntry{}, false, ErrAtomicReversalRequired
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	posted, err := repository.CreateAndPostReversalAtomicContext(ctx, original, reversal)
	if err != nil {
		return JournalEntry{}, false, err
	}
	return posted, posted.ID != reversal.ID, nil
}
