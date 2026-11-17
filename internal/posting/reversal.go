package posting

import (
	"context"
	"errors"
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
	if request.ActorID == "" {
		return JournalEntry{}, false, ErrReversalActorRequired
	}
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
		if request.IdempotencyKey != "" {
			if reader, ok := e.repo.(IdempotentReversalReader); ok {
				if existing, found, checkErr := reader.FindReversalByIdempotencyKeyContext(ctx, companyID, request.IdempotencyKey); checkErr == nil && found {
					if existing.IdempotencyPayloadHash != payloadHash {
						return JournalEntry{}, false, ErrIdempotencyConflict
					}
					return existing, true, nil
				}
			}
		}
		return JournalEntry{}, false, ErrEntryAlreadyReversed
	}
	if original.Status != EntryPosted {
		return JournalEntry{}, false, ErrEntryNotPosted
	}
	if request.TargetPeriodNumber != 0 &&
		(request.TargetPeriodNumber != 13 || original.Classification != EntryClassClosing ||
			original.TargetPeriodNumber != request.TargetPeriodNumber) {
		return JournalEntry{}, false, ErrInvalidReversal
	}

	reversalDate := request.EffectiveDate
	explicitDate := !reversalDate.IsZero()
	if !explicitDate {
		// Default policy: reversal takes effect now. A default date is not
		// compared to the original because a future-dated original may still
		// be reversed as soon as it is posted.
		reversalDate = time.Now().UTC()
	}
	// A reversal may be posted in a later open period, but an explicitly
	// chosen date may never predate the original entry it reverses: that would
	// relocate the correction before the event being corrected. The comparison
	// is by calendar date because accounting effective dates are day-granular.
	reversalDate = reversalDate.UTC()
	if originalDate := effectiveDate(&original); explicitDate && !originalDate.IsZero() &&
		calendarDate(reversalDate).Before(calendarDate(originalDate)) {
		return JournalEntry{}, false, ErrReversalDateBeforeOriginal
	}
	reference := strings.TrimSpace(request.Reference)
	sourceModule := strings.TrimSpace(request.SourceSystem)
	if sourceModule == "" {
		sourceModule = original.SourceModule
	}
	originalID := original.ID
	now := time.Now().UTC()
	// The reversal is a new, independent accounting entry. It deliberately does
	// not copy lifecycle or linkage state that must never be inherited:
	// ApprovalFingerprint, SourceEventID, LegalNumber, SequenceNumber,
	// attachments and reconciliation state. Those are re-established by the
	// reversal workflow (approval) or stay with the original document.
	reversal := JournalEntry{
		CompanyID:            original.CompanyID,
		ID:                   uuid.New(),
		JournalID:            original.JournalID,
		EntryDate:            reversalDate,
		Date:                 reversalDate,
		Status:               EntryPosted,
		Classification:       EntryClassReversing,
		TargetPeriodNumber:   request.TargetPeriodNumber,
		AdjustmentAuthorized: request.TargetPeriodNumber == 13,
		DocumentReference:    original.DocumentReference,
		Reference:            original.Reference,
		ReversalReference:    reference,
		SourceModule:         sourceModule,
		ReversalReason:       strings.TrimSpace(request.Reason),
		// Entry-level currency context mirrors the original so reports can
		// reproduce the same transaction currency and exchange rate; the
		// authoritative amounts remain the per-line functional layers.
		TransactionCurrency:    original.TransactionCurrency,
		ExchangeRate:           original.ExchangeRate,
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
			CompanyID:      line.CompanyID,
			ID:             uuid.New(),
			AccountID:      line.AccountID,
			PartnerID:      line.PartnerID,
			CostCenterID:   line.CostCenterID,
			ProjectID:      line.ProjectID,
			SegmentID:      line.SegmentID,
			BusinessUnitID: line.BusinessUnitID,
			// MatchingNumber is a documentary linking reference and is
			// preserved on the reversal line so the pair stays matchable.
			MatchingNumber: line.MatchingNumber,
			// ReconciledAmount is intentionally reset to zero: a reversal is not
			// a reconciliation event, and reversing an open item must be handled
			// explicitly by the reconciliation/subledger workflow.
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
	if err := e.prepareForPosting(ctx, &reversal, reversal.PostedBy, now); err != nil {
		return JournalEntry{}, false, err
	}
	reversal.PostingDate = calendarDate(now)
	reversal.Status = EntryPosted
	reversal.PostedAt = &now
	reversal.UpdatedAt = now

	repository, ok := e.repo.(AtomicReversalCreationRepository)
	if !ok {
		return JournalEntry{}, false, ErrAtomicReversalRequired
	}
	var (
		postedResult JournalEntry
		wasCreated   bool
	)
	err = e.postingLocker().WithinPostingLock(ctx, reversal.CompanyID, reversal.JournalID, func(lockCtx context.Context) error {
		posted, err := repository.CreateAndPostReversalAtomicContext(lockCtx, original, reversal)
		if err != nil {
			if errors.Is(err, ErrEntryAlreadyReversed) && request.IdempotencyKey != "" {
				if reader, ok := e.repo.(IdempotentReversalReader); ok {
					if existing, found, checkErr := reader.FindReversalByIdempotencyKeyContext(lockCtx, companyID, request.IdempotencyKey); checkErr == nil && found {
						if existing.IdempotencyPayloadHash != payloadHash {
							return ErrIdempotencyConflict
						}
						postedResult = existing
						wasCreated = true
						return nil
					}
				}
			}
			return err
		}
		postedResult = posted
		wasCreated = posted.ID != reversal.ID
		return nil
	})
	if err != nil {
		return JournalEntry{}, false, err
	}
	return postedResult, wasCreated, nil
}
