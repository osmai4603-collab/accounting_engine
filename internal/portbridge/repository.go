package portbridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// Repository adapts the stable storage ports to the internal posting
// repository. It performs translation only; validation and accounting policy
// stay in the domain/application layers.
type Repository struct {
	Ports storage.Ports
}

var _ posting.Repository = (*Repository)(nil)

func (r *Repository) SaveDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	if r.Ports.Drafts == nil {
		return errors.New("portbridge: draft store is not configured")
	}
	publicEntry, err := ToPublicEntry(entry)
	if err != nil {
		return err
	}
	return r.Ports.Drafts.SaveDraft(ctx, publicEntry)
}

func (r *Repository) GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	if r.Ports.Entries == nil {
		return posting.JournalEntry{}, errors.New("portbridge: entry reader is not configured")
	}
	entry, err := r.Ports.Entries.GetEntry(ctx, types.CompanyID(companyID), types.JournalEntryID(entryID))
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return ToInternalEntry(entry, EntryOptions{})
}

func (r *Repository) PostAtomicContext(ctx context.Context, entry posting.JournalEntry) error {
	if r.Ports.Posting == nil {
		return errors.New("portbridge: posting writer is not configured")
	}
	publicEntry, err := ToPublicEntry(entry)
	if err != nil {
		return err
	}
	operation := storage.OperationPost
	if entry.Classification == posting.EntryClassReversing && entry.OriginalEntryID != nil {
		operation = storage.OperationReverse
	}
	commit := storage.PostingCommit{
		Entry:         publicEntry,
		Operation:     operation,
		ActorID:       entry.PostedBy,
		Reason:        entry.ReversalReason,
		CorrelationID: correlationFromContext(ctx),
	}
	if entry.OriginalEntryID != nil {
		original := types.JournalEntryID(*entry.OriginalEntryID)
		commit.OriginalEntryID = &original
	}
	result, err := r.Ports.Posting.CommitPosting(ctx, commit)
	if err != nil {
		return err
	}
	if result.ID == (types.JournalEntryID{}) {
		return errors.New("portbridge: posting writer returned an empty entry")
	}
	if result.CompanyID != publicEntry.CompanyID || result.ID != publicEntry.ID {
		return fmt.Errorf("portbridge: posting writer returned a different entry (%s/%s)", result.CompanyID, result.ID)
	}
	return nil
}

func (r *Repository) GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	if r.Ports.Sequences == nil {
		return 0, errors.New("portbridge: sequence allocator is not configured")
	}
	return r.Ports.Sequences.NextSequence(ctx, types.CompanyID(companyID), types.JournalID(journalID))
}

// PostBulk is optional; the core SDK path does not require it.
func (r *Repository) PostBulk(context.Context, []posting.JournalEntry) error {
	return types.ErrCapabilityNotImplemented
}

// PostReversalAtomicContext is implemented by delegating to the same atomic
// CommitPosting port with OperationReverse.
func (r *Repository) PostReversalAtomicContext(ctx context.Context, original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	if r.Ports.Posting == nil {
		return posting.JournalEntry{}, errors.New("portbridge: posting writer is not configured")
	}
	if original.ID == uuid.Nil || reversal.OriginalEntryID == nil || *reversal.OriginalEntryID != original.ID {
		return posting.JournalEntry{}, posting.ErrInvalidReversal
	}
	publicReversal, err := ToPublicEntry(reversal)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	originalID := types.JournalEntryID(original.ID)
	commit := storage.PostingCommit{
		Entry:           publicReversal,
		Operation:       storage.OperationReverse,
		OriginalEntryID: &originalID,
		ActorID:         reversal.PostedBy,
		Reason:          reversal.ReversalReason,
		CorrelationID:   correlationFromContext(ctx),
	}
	result, err := r.Ports.Posting.CommitPosting(ctx, commit)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return ToInternalEntry(result, EntryOptions{})
}

type atomicReversalPostingPort interface {
	FindReversalByIdempotencyKeyContext(
		context.Context,
		uuid.UUID,
		string,
	) (posting.JournalEntry, bool, error)
	CreateAndPostReversalAtomicContext(
		context.Context,
		posting.JournalEntry,
		posting.JournalEntry,
	) (posting.JournalEntry, error)
}

func (r *Repository) FindReversalByIdempotencyKeyContext(
	ctx context.Context,
	companyID uuid.UUID,
	key string,
) (posting.JournalEntry, bool, error) {
	port, ok := r.Ports.Posting.(atomicReversalPostingPort)
	if !ok {
		return posting.JournalEntry{}, false, posting.ErrAtomicReversalRequired
	}
	return port.FindReversalByIdempotencyKeyContext(ctx, companyID, key)
}

func (r *Repository) CreateAndPostReversalAtomicContext(
	ctx context.Context,
	original, reversal posting.JournalEntry,
) (posting.JournalEntry, error) {
	port, ok := r.Ports.Posting.(atomicReversalPostingPort)
	if !ok {
		return posting.JournalEntry{}, posting.ErrAtomicReversalRequired
	}
	return port.CreateAndPostReversalAtomicContext(ctx, original, reversal)
}

// Ensure the adapter never silently accepts a legacy core store contract.
var _ = ledger.EntryPosted

type correlationKey struct{}

// WithCorrelationID attaches a correlation identifier for adapters to persist.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

func correlationFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(correlationKey{}).(string)
	return value
}
