package sdkapplication

import (
	"context"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/spi/storage"

	"github.com/google/uuid"
)

type ledgerApplication struct {
	engine *posting.Engine
}

func New(core storage.CoreLedgerStore) (storage.LedgerApplication, error) {
	postingRepository, ok := core.(posting.BulkRepository)
	if !ok {
		return nil, types.ErrCapabilityNotImplemented
	}
	masterRepository, ok := core.(masterdata.ContextStorageRepository)
	if !ok {
		return nil, types.ErrCapabilityNotImplemented
	}
	masterService := masterdata.NewService(masterdata.NewRepositoryAdapter(masterRepository))
	return &ledgerApplication{engine: posting.NewEngine(postingRepository, masterService)}, nil
}

func (a *ledgerApplication) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	internalEntry, err := toInternalEntry(entry)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	internalEntry.Status = posting.EntryDraft
	if err := a.engine.CreateDraftContext(ctx, internalEntry); err != nil {
		return ledger.JournalEntry{}, err
	}
	posterID := strings.TrimSpace(internalEntry.CreatedBy)
	if posterID == "" {
		posterID = "system"
	}
	if err := a.engine.PostEntryContext(ctx, &internalEntry, posterID); err != nil {
		return fromInternalEntry(internalEntry), err
	}
	posted, err := a.engine.GetEntry(ctx, internalEntry.CompanyID, internalEntry.ID)
	if err != nil {
		return fromInternalEntry(internalEntry), err
	}
	return fromInternalEntry(posted), nil
}

func (a *ledgerApplication) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	entry, err := a.engine.GetEntry(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return fromInternalEntry(entry), nil
}

func (a *ledgerApplication) ReverseEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, reason string) (ledger.JournalEntry, error) {
	creator := strings.TrimSpace(reason)
	if creator == "" {
		creator = "system-reversal"
	}
	entry, err := a.engine.ReverseDualContext(ctx, uuid.UUID(companyID), uuid.UUID(entryID), creator, creator, time.Now().UTC())
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	result := fromInternalEntry(entry)
	if reason != "" {
		result.Description = reason
		if result.ReferenceID == "" {
			result.ReferenceID = "reversal:" + uuid.UUID(entryID).String()
		}
	}
	return result, nil
}

var _ storage.LedgerApplication = (*ledgerApplication)(nil)
