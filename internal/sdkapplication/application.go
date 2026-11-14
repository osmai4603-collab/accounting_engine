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
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

var (
	ErrActorRequired   = errors.New("actor id is required")
	ErrReasonRequired  = errors.New("reversal reason is required")
	ErrEntryIDRequired = errors.New("journal entry id is required")
)

type ledgerApplication struct {
	engine           *posting.Engine
	publicMasterData publicmasterdata.Store
}

func New(core legacy.CoreLedgerStore) (legacy.LedgerApplication, error) {
	return NewApplicationFromCore(core)
}

func NewApplicationFromCore(core legacy.CoreLedgerStore) (*Application, error) {
	return NewApplicationFromCoreWithPostingConfig(core, PostingConfig{})
}

func NewApplicationFromCoreWithPostingConfig(core legacy.CoreLedgerStore, config PostingConfig) (*Application, error) {
	return NewApplicationFromCoreWithPostingCapabilities(core, config, storage.PostingCapabilities{})
}

func NewApplicationFromCoreWithPostingCapabilities(
	core legacy.CoreLedgerStore,
	config PostingConfig,
	publicCapabilities storage.PostingCapabilities,
) (*Application, error) {
	postingRepository, ok := core.(posting.Repository)
	if !ok {
		return nil, types.ErrCapabilityNotImplemented
	}
	masterRepository, ok := core.(masterdata.ContextStorageRepository)
	if !ok {
		return nil, types.ErrCapabilityNotImplemented
	}
	masterService := masterdata.NewService(masterdata.NewRepositoryAdapter(masterRepository))
	var publicMasterData publicmasterdata.Store
	if provider, ok := core.(interface{ MasterDataStore() publicmasterdata.Store }); ok {
		publicMasterData = provider.MasterDataStore()
	}
	capabilities := capabilitiesFromCore(core, config)
	capabilities = mergeCapabilities(capabilities, capabilitiesFromPublic(publicCapabilities))
	engine, err := createPostingEngine(postingRepository, masterService, config, capabilities)
	if err != nil {
		return nil, err
	}
	return &Application{
		engine:           engine,
		publicMasterData: publicMasterData,
	}, nil
}

// NewLegacy builds the legacy adapter surface from a core store.
func NewLegacy(core legacy.CoreLedgerStore) (legacy.LedgerApplication, error) { return New(core) }

// Application is the extended application surface used by the SDK. It exposes
// explicit actor/reason reversal while remaining a stable-port consumer.
type Application struct {
	engine           *posting.Engine
	publicMasterData publicmasterdata.Store
}

// NewApplicationFromPorts builds the application from public storage ports.
func NewApplicationFromPorts(ports storage.Ports) (*Application, error) {
	return NewApplicationFromPortsWithPostingConfig(ports, PostingConfig{})
}

func NewApplicationFromPortsWithPostingConfig(ports storage.Ports, config PostingConfig) (*Application, error) {
	return NewApplicationFromPortsWithPostingCapabilities(ports, config, storage.PostingCapabilities{})
}

func NewApplicationFromPortsWithPostingCapabilities(
	ports storage.Ports,
	config PostingConfig,
	publicCapabilities storage.PostingCapabilities,
) (*Application, error) {
	if err := ports.Validate(); err != nil {
		return nil, err
	}
	capabilities := posting.Capabilities{}
	if supplied, ok := ports.Private[PostingCapabilitiesExtension].(posting.Capabilities); ok {
		capabilities = supplied
	}
	capabilities = mergeCapabilities(capabilities, capabilitiesFromPublic(publicCapabilities))
	engine, err := createPostingEngine(&portbridge.Repository{Ports: ports}, portbridge.NewMasterDataService(ports), config, capabilities)
	if err != nil {
		return nil, err
	}
	return &Application{engine: engine}, nil
}

// NewApplicationWithEngine wraps an already configured engine. It is used only
// by the composition root.
func NewApplicationWithEngine(engine *posting.Engine) *Application {
	return &Application{engine: engine}
}

func resolveDefaultJournalID(ctx context.Context, masterData publicmasterdata.Store, companyID types.CompanyID) (types.JournalID, error) {
	if masterData == nil {
		return types.JournalID{}, types.ErrCapabilityNotImplemented
	}
	companyID = types.CompanyID(uuid.UUID(companyID))
	if uuid.UUID(companyID) == uuid.Nil {
		return types.JournalID{}, errors.New("company id is required")
	}
	const defaultJournalCode = "GENERAL"
	journal, err := masterData.GetJournalByCode(ctx, companyID, defaultJournalCode)
	if err == nil {
		return types.JournalID(journal.ID), nil
	}
	if !errors.Is(err, publicmasterdata.ErrJournalNotFound) {
		return types.JournalID{}, err
	}
	journalID := publicmasterdata.NewJournalID()
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID:        journalID,
		CompanyID: companyID,
		Code:      defaultJournalCode,
		Name:      "General Journal",
	}); err != nil {
		if errors.Is(err, publicmasterdata.ErrDuplicateJournalCode) {
			journal, retryErr := masterData.GetJournalByCode(ctx, companyID, defaultJournalCode)
			if retryErr == nil {
				return types.JournalID(journal.ID), nil
			}
			return types.JournalID{}, retryErr
		}
		return types.JournalID{}, err
	}
	return types.JournalID(journalID), nil
}

func (a *Application) EnsureJournalID(ctx context.Context, entry *ledger.JournalEntry) error {
	if entry == nil {
		return errors.New("entry is required")
	}
	if entry.JournalID != (types.JournalID{}) && uuid.UUID(entry.JournalID) != uuid.Nil {
		return nil
	}
	journalID, err := resolveDefaultJournalID(ctx, a.publicMasterData, entry.CompanyID)
	if err != nil {
		return err
	}
	entry.JournalID = journalID
	return nil
}

func (a *ledgerApplication) EnsureJournalID(ctx context.Context, entry *ledger.JournalEntry) error {
	if entry == nil {
		return errors.New("entry is required")
	}
	if entry.JournalID != (types.JournalID{}) && uuid.UUID(entry.JournalID) != uuid.Nil {
		return nil
	}
	journalID, err := resolveDefaultJournalID(ctx, a.publicMasterData, entry.CompanyID)
	if err != nil {
		return err
	}
	entry.JournalID = journalID
	return nil
}

func (a *Application) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	if err := a.EnsureJournalID(ctx, &entry); err != nil {
		return ledger.JournalEntry{}, err
	}
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
	if err := a.EnsureJournalID(ctx, &entry); err != nil {
		return ledger.JournalEntry{}, err
	}
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

var _ legacy.LedgerApplication = (*ledgerApplication)(nil)
var _ legacy.LedgerApplication = (*Application)(nil)
