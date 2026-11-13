package sdkapplication

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/portbridge"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

var (
	ErrActorRequired   = errors.New("actor id is required")
	ErrReasonRequired  = errors.New("reversal reason is required")
	ErrEntryIDRequired = errors.New("journal entry id is required")
)

type ledgerApplication struct {
	engine *posting.Engine
}

func New(core storage.CoreLedgerStore) (storage.LedgerApplication, error) {
	postingRepository, ok := core.(posting.Repository)
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

// NewLegacy builds the legacy adapter surface from a core store.
func NewLegacy(core storage.CoreLedgerStore) (storage.LedgerApplication, error) { return New(core) }

// Application is the extended application surface used by the SDK. It exposes
// explicit actor/reason reversal while remaining a stable-port consumer.
type Application struct {
	engine *posting.Engine
}

// NewApplicationFromPorts builds the application from public storage ports.
func NewApplicationFromPorts(ports storage.Ports) (*Application, error) {
	if err := ports.Validate(); err != nil {
		return nil, err
	}
	return &Application{engine: posting.NewEngine(&portbridge.Repository{Ports: ports}, portbridge.NewMasterDataService(ports))}, nil
}

// NewApplicationWithEngine wraps an already configured engine. It is used only
// by the composition root.
func NewApplicationWithEngine(engine *posting.Engine) *Application { return &Application{engine: engine} }

func (a *Application) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	return postEntry(ctx, a.engine, entry)
}

func (a *Application) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	return getEntry(ctx, a.engine, companyID, entryID)
}

func (a *Application) ReverseEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, reason string) (ledger.JournalEntry, error) {
	actor := strings.TrimSpace(reason)
	if actor == "" {
		return ledger.JournalEntry{}, ErrActorRequired
	}
	return a.ReverseEntryWithIdentity(ctx, companyID, entryID, actor, reason, time.Now().UTC())
}

func (a *Application) ReverseEntryWithIdentity(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, actorID, reason string, effectiveDate time.Time) (ledger.JournalEntry, error) {
	return reverseWithIdentity(ctx, a.engine, companyID, entryID, actorID, reason, effectiveDate)
}

func (a *ledgerApplication) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	return postEntry(ctx, a.engine, entry)
}

func (a *ledgerApplication) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	return getEntry(ctx, a.engine, companyID, entryID)
}

// ReverseEntry keeps the legacy public signature for compatibility. New code
// must use ReverseEntryWithIdentity so the actor and the reason stay separate.
func (a *ledgerApplication) ReverseEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, reason string) (ledger.JournalEntry, error) {
	actor := strings.TrimSpace(reason)
	if actor == "" {
		return ledger.JournalEntry{}, ErrActorRequired
	}
	return reverseWithIdentity(ctx, a.engine, companyID, entryID, actor, reason, time.Now().UTC())
}

func postEntry(ctx context.Context, engine *posting.Engine, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	internalEntry, err := toInternalEntry(entry)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	internalEntry.Status = posting.EntryDraft
	if err := engine.CreateDraftContext(ctx, internalEntry); err != nil {
		return ledger.JournalEntry{}, err
	}
	posterID := strings.TrimSpace(internalEntry.CreatedBy)
	if posterID == "" {
		return ledger.JournalEntry{}, ErrActorRequired
	}
	if err := engine.PostEntryContext(ctx, &internalEntry, posterID); err != nil {
		return ledger.JournalEntry{}, err
	}
	posted, err := engine.GetEntry(ctx, internalEntry.CompanyID, internalEntry.ID)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return fromInternalEntry(posted)
}

func getEntry(ctx context.Context, engine *posting.Engine, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	entry, err := engine.GetEntry(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return fromInternalEntry(entry)
}

func reverseWithIdentity(ctx context.Context, engine *posting.Engine, companyID types.CompanyID, entryID types.JournalEntryID, actorID, reason string, effectiveDate time.Time) (ledger.JournalEntry, error) {
	if uuid.UUID(entryID) == uuid.Nil {
		return ledger.JournalEntry{}, ErrEntryIDRequired
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return ledger.JournalEntry{}, ErrActorRequired
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ledger.JournalEntry{}, ErrReasonRequired
	}
	if effectiveDate.IsZero() {
		effectiveDate = time.Now().UTC()
	}
	entry, err := engine.ReverseWithReasonContext(ctx, uuid.UUID(companyID), uuid.UUID(entryID), actorID, actorID, reason, effectiveDate)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return fromInternalEntry(entry)
}

var _ storage.LedgerApplication = (*ledgerApplication)(nil)
var _ storage.LedgerApplication = (*Application)(nil)
