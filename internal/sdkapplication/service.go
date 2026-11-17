package sdkapplication

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

var ErrReasonInvalid = errors.New("reversal reason must be at most 500 characters and contain no control characters")

func (a *Application) CreateDraftEntry(ctx context.Context, entry ledger.JournalEntry, actorID string) (ledger.JournalEntry, error) {
	if entry.CompanyID == (types.CompanyID{}) {
		return ledger.JournalEntry{}, errors.New("company id is required")
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return ledger.JournalEntry{}, ErrActorRequired
	}
	if entry.CreatedBy == "" {
		entry.CreatedBy = actorID
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.UpdatedAt = entry.CreatedAt
	if err := a.EnsureJournalID(ctx, &entry); err != nil {
		return ledger.JournalEntry{}, err
	}
	internalEntry, err := toInternalEntry(entry)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	internalEntry.CreatedBy = actorID
	internalEntry.CreatedAt = entry.CreatedAt
	internalEntry.UpdatedAt = entry.CreatedAt
	if err := a.engine.CreateDraftContext(ctx, internalEntry); err != nil {
		return ledger.JournalEntry{}, err
	}
	stored, err := a.engine.GetEntry(ctx, uuid.UUID(entry.CompanyID), internalEntry.ID)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return fromInternalEntry(stored)
}

func (a *Application) ValidateEntryByID(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) error {
	if companyID == (types.CompanyID{}) {
		return errors.New("company id is required")
	}
	if uuid.UUID(entryID) == uuid.Nil {
		return ErrEntryIDRequired
	}
	entry, err := a.engine.GetEntry(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
	if err != nil {
		return err
	}
	if entry.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	return entry.Validate()
}

func (a *Application) PostEntryByID(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, actorID string) (ledger.JournalEntry, error) {
	if companyID == (types.CompanyID{}) {
		return ledger.JournalEntry{}, errors.New("company id is required")
	}
	if uuid.UUID(entryID) == uuid.Nil {
		return ledger.JournalEntry{}, ErrEntryIDRequired
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return ledger.JournalEntry{}, ErrActorRequired
	}
	entry, err := a.GetEntry(ctx, companyID, entryID)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	internalEntry, err := toInternalExistingEntry(entry)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	if err := a.engine.PostEntryContext(ctx, &internalEntry, actorID); err != nil {
		return ledger.JournalEntry{}, err
	}
	posted, err := a.engine.GetEntry(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return fromInternalEntry(posted)
}

func (a *Application) ReverseEntryByID(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, actorID, reason string, effectiveDate time.Time) (ledger.JournalEntry, error) {
	entry, _, err := a.ReverseEntryByIDWithMetadata(ctx, companyID, entryID, actorID, reason, effectiveDate, "", "", "")
	return entry, err
}

func (a *Application) ReverseEntryByIDWithMetadata(
	ctx context.Context,
	companyID types.CompanyID,
	entryID types.JournalEntryID,
	actorID, reason string,
	effectiveDate time.Time,
	idempotencyKey, reference, sourceSystem string,
) (ledger.JournalEntry, bool, error) {
	return a.reverseEntryByIDWithMetadata(ctx, companyID, entryID, actorID, reason, effectiveDate,
		idempotencyKey, reference, sourceSystem, 0)
}

func (a *Application) ReverseYearEndCloseEntryByIDWithMetadata(
	ctx context.Context,
	companyID types.CompanyID,
	entryID types.JournalEntryID,
	actorID, reason string,
	effectiveDate time.Time,
	idempotencyKey, reference, sourceSystem string,
) (ledger.JournalEntry, bool, error) {
	return a.reverseEntryByIDWithMetadata(ctx, companyID, entryID, actorID, reason, effectiveDate,
		idempotencyKey, reference, sourceSystem, 13)
}

func (a *Application) reverseEntryByIDWithMetadata(
	ctx context.Context,
	companyID types.CompanyID,
	entryID types.JournalEntryID,
	actorID, reason string,
	effectiveDate time.Time,
	idempotencyKey, reference, sourceSystem string,
	targetPeriodNumber int,
) (ledger.JournalEntry, bool, error) {
	if companyID == (types.CompanyID{}) {
		return ledger.JournalEntry{}, false, errors.New("company id is required")
	}
	if uuid.UUID(entryID) == uuid.Nil {
		return ledger.JournalEntry{}, false, ErrEntryIDRequired
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return ledger.JournalEntry{}, false, ErrActorRequired
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ledger.JournalEntry{}, false, ErrReasonRequired
	}
	if utf8.RuneCountInString(reason) > 500 {
		return ledger.JournalEntry{}, false, ErrReasonInvalid
	}
	for _, character := range reason {
		if unicode.IsControl(character) {
			return ledger.JournalEntry{}, false, ErrReasonInvalid
		}
	}
	internalEntry, replay, err := a.engine.ReverseWithRequestResultContext(ctx, uuid.UUID(companyID), uuid.UUID(entryID), posting.ReversalRequest{
		ActorID: actorID, Reason: reason, EffectiveDate: effectiveDate,
		IdempotencyKey: strings.TrimSpace(idempotencyKey), Reference: strings.TrimSpace(reference),
		SourceSystem: strings.TrimSpace(sourceSystem), TargetPeriodNumber: targetPeriodNumber,
	}, actorID)
	if err != nil {
		return ledger.JournalEntry{}, false, err
	}
	entry, err := fromInternalEntry(internalEntry)
	if err != nil {
		return ledger.JournalEntry{}, false, err
	}
	return entry, replay, nil
}
