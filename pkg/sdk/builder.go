package sdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/sdkapplication"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// EngineBuilder composes the engine from exactly one storage dependency
// source:
//
//   - WithStoragePorts: the recommended, implementation-neutral path. The SDK
//     builds the application layer from public ports and never asks the adapter
//     to construct application services.
//   - WithCoreStorage: the deprecated monolithic store path, kept for
//     compatibility until the next MAJOR release. The store must expose the
//     capabilities needed to build the same application layer as WithStoragePorts.
//
// Mixing both sources is rejected during preflight.
type EngineBuilder struct {
	coreStore       legacy.CoreLedgerStore
	ports           storage.Ports
	portsConfigured bool
	reportingStore  storage.ReportingStore
	governanceStore storage.GovernanceStore
	authorizer      publicapplication.Authorizer
	standards         []standards.StandardModule
	standardsRegistry *standards.Registry
	postingConfig     PostingConfig
	postingCaps     storage.PostingCapabilities
	optsErrs        []error
}

type Option func(*EngineBuilder) error

// WithStoragePorts configures the engine from the stable public storage ports.
// All required ports are validated before any standard module is initialized.
func WithStoragePorts(ports storage.Ports) Option {
	return func(b *EngineBuilder) error {
		b.ports = ports
		b.portsConfigured = true
		return nil
	}
}

// WithCoreStorage configures the engine from the legacy monolithic store.
//
// Deprecated: use WithStoragePorts with the adapter's Ports() entry point.
// WithCoreStorage requires the adapter to expose the posting and master-data
// capabilities needed by the SDK-built application layer. It will be removed
// in the next MAJOR release.
func WithCoreStorage(store legacy.CoreLedgerStore) Option {
	return func(b *EngineBuilder) error {
		if store == nil {
			return errors.New("core ledger store is nil")
		}
		b.coreStore = store
		return nil
	}
}

// WithReporting enables reporting capabilities. Reporting is optional; when it
// is not configured, reporting calls fail explicitly instead of being
// silently emulated.
func WithReporting(store storage.ReportingStore) Option {
	return func(b *EngineBuilder) error {
		if store == nil {
			return errors.New("reporting store is nil")
		}
		b.reportingStore = store
		return nil
	}
}

// WithGovernance enables governance capabilities. Governance is optional; when
// it is not configured, governance calls fail explicitly.
func WithGovernance(store storage.GovernanceStore) Option {
	return func(b *EngineBuilder) error {
		if store == nil {
			return errors.New("governance store is nil")
		}
		b.governanceStore = store
		return nil
	}
}

func WithAuthorizer(authorizer publicapplication.Authorizer) Option {
	return func(b *EngineBuilder) error {
		if authorizer == nil {
			return errors.New("authorizer is nil")
		}
		b.authorizer = authorizer
		return nil
	}
}

// WithPostingConfig declares posting controls that must be available before
// the engine can be built.
func WithPostingConfig(config PostingConfig) Option {
	return func(b *EngineBuilder) error {
		b.postingConfig = config
		return nil
	}
}

// WithPostingCapabilities supplies optional controls used by PostingConfig.
func WithPostingCapabilities(capabilities storage.PostingCapabilities) Option {
	return func(b *EngineBuilder) error {
		b.postingCaps = capabilities
		return nil
	}
}

func WithStandard(module standards.StandardModule) Option {
	return func(b *EngineBuilder) error {
		if module == nil {
			return errors.New("standard module is nil")
		}
		b.standards = append(b.standards, module)
		return nil
	}
}

func NewBuilder(opts ...Option) *EngineBuilder {
	b := &EngineBuilder{}
	for _, opt := range opts {
		if opt == nil {
			b.optsErrs = append(b.optsErrs, errors.New("builder option is nil"))
			continue
		}
		if err := opt(b); err != nil {
			b.optsErrs = append(b.optsErrs, err)
		}
	}
	return b
}

// Build validates every dependency before mutating module state, then
// initializes the configured standard modules and returns the engine.
func (b *EngineBuilder) Build(ctx context.Context) (Engine, error) {
	errs := b.preflight()
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	router := events.NewRouter()
	var publicMasterData publicmasterdata.Store
	var useCases publicapplication.Service

	if b.portsConfigured {
		app, err := sdkapplication.NewApplicationFromPortsWithPostingCapabilities(
			b.ports, b.postingConfig.internal(), b.postingCaps,
		)
		if err != nil {
			return nil, fmt.Errorf("application layer: %w", err)
		}
		useCases = sdkapplication.NewPublicService(app, b.authorizer)
	} else {
		app, err := sdkapplication.NewApplicationFromCoreWithPostingCapabilities(
			b.coreStore, b.postingConfig.internal(), b.postingCaps,
		)
		if err != nil {
			return nil, fmt.Errorf("application layer: %w", err)
		}
		useCases = sdkapplication.NewPublicService(app, b.authorizer)
		if provider, ok := b.coreStore.(interface {
			MasterDataStore() publicmasterdata.Store
		}); ok {
			publicMasterData = provider.MasterDataStore()
		}
	}

	moduleErrs := b.initializeStandards(ctx, router)
	if len(moduleErrs) > 0 {
		return nil, errors.Join(moduleErrs...)
	}

	inst := &engineInstance{
		coreStore:         b.coreStore,
		reportingStore:    b.reportingStore,
		governanceStore:   b.governanceStore,
		standards:         b.standards,
		standardsRegistry: b.standardsRegistry,
		eventRouter:       router,
		eventService:      &defaultEventService{}, // initialized below
		publicMasterData:  publicMasterData,
		useCases:          useCases,
		requirePrincipal:  b.authorizer != nil,
	}

	inst.eventService = &defaultEventService{
		store:            b.coreStore,
		router:           router,
		publicMasterData: inst.publicMasterData,
		useCases:         inst.useCases,
		requirePrincipal: inst.requirePrincipal,
	}

	return inst, nil
}

// preflight collects every missing or conflicting dependency without
// initializing a module or touching adapter state.
func (b *EngineBuilder) preflight() []error {
	errs := append([]error(nil), b.optsErrs...)

	switch {
	case b.portsConfigured && b.coreStore != nil:
		errs = append(errs, errors.New("storage: configure exactly one of WithStoragePorts or WithCoreStorage"))
	case !b.portsConfigured && b.coreStore == nil:
		errs = append(errs, errors.New("storage: WithStoragePorts or WithCoreStorage is required"))
	case b.portsConfigured:
		if err := b.ports.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("storage ports: %w", err))
		}
	}

	if len(b.standards) > 0 {
		registry := standards.NewRegistry()
		for _, mod := range b.standards {
			if mod == nil {
				errs = append(errs, errors.New("standard module is nil"))
				continue
			}
			if err := registry.Register(mod); err != nil {
				errs = append(errs, err)
			}
		}

		resolvedMods, resErr := registry.Resolve()
		if resErr != nil {
			errs = append(errs, resErr)
		} else {
			b.standards = resolvedMods
			b.standardsRegistry = registry
		}

		for _, mod := range b.standards {
			if mod == nil {
				continue
			}
			name := mod.Name()
			if b.coreStore == nil {
				errs = append(errs, fmt.Errorf("standard module %s: standard modules require WithCoreStorage in this release; storage ports do not expose the standards repository", name))
				continue
			}
			if err := mod.ValidateDependencies(b.coreStore); err != nil {
				errs = append(errs, fmt.Errorf("standard module %s: dependency validation failed: %w", name, err))
			}
		}
	}

	return errs
}

// initializeStandards runs Initialize and RegisterRules only after preflight
// succeeded.
func (b *EngineBuilder) initializeStandards(ctx context.Context, router *events.Router) []error {
	var errs []error
	for _, mod := range b.standards {
		if mod == nil {
			continue
		}
		name := mod.Name()
		if err := mod.Initialize(ctx, b.coreStore); err != nil {
			errs = append(errs, fmt.Errorf("standard module %s: initialization failed: %w", name, err))
			continue
		}
		if err := mod.RegisterRules(router); err != nil {
			errs = append(errs, fmt.Errorf("standard module %s: rule registration failed: %w", name, err))
		}
	}
	return errs
}

type engineInstance struct {
	coreStore         legacy.CoreLedgerStore
	reportingStore    storage.ReportingStore
	governanceStore   storage.GovernanceStore
	standards         []standards.StandardModule
	standardsRegistry *standards.Registry
	eventRouter       *events.Router
	eventService      *defaultEventService
	publicMasterData  publicmasterdata.Store
	useCases          publicapplication.Service
	requirePrincipal  bool
}

func (e *engineInstance) Ledger() LedgerService {
	return &defaultLedgerService{
		useCases:         e.useCases,
		requirePrincipal: e.requirePrincipal,
	}
}

func (e *engineInstance) Reporting() ReportingService {
	return &defaultReportingService{store: e.reportingStore}
}

func (e *engineInstance) Governance() GovernanceService {
	return &defaultGovernanceService{store: e.governanceStore}
}

func (e *engineInstance) Events() EventIngestionService {
	return e.eventService
}

func (e *engineInstance) Standards() StandardsService {
	return &defaultStandardsService{registry: e.standardsRegistry}
}

type defaultStandardsService struct {
	registry *standards.Registry
}

func (s *defaultStandardsService) Registry() *standards.Registry {
	return s.registry
}

func (s *defaultStandardsService) Diagnostics() standards.EngineDiagnostics {
	if s.registry == nil {
		return standards.EngineDiagnostics{}
	}
	return s.registry.Diagnostics()
}

func (s *defaultStandardsService) HasCapability(cap standards.Capability) bool {
	if s.registry == nil {
		return false
	}
	return s.registry.HasCapability(cap)
}

func (e *engineInstance) Close() error {
	var errs []error
	// Close standard modules in reverse topological order so dependents clean up first.
	for i := len(e.standards) - 1; i >= 0; i-- {
		mod := e.standards[i]
		if err := mod.Close(context.Background()); err != nil {
			errs = append(errs, fmt.Errorf("module %s close failed: %w", mod.Name(), err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

type defaultLedgerService struct {
	useCases         publicapplication.Service
	requirePrincipal bool
}

func (s *defaultLedgerService) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	if s.useCases == nil {
		return ledger.JournalEntry{}, types.ErrCapabilityNotImplemented
	}
	if err := validatePostingEntry(entry); err != nil {
		return ledger.JournalEntry{}, err
	}
	command := createDraftCommand(entry, commandContext(ctx, entry.CompanyID, "sdk", "sdk", s.requirePrincipal))
	created, err := createDraftUseCase(ctx, s.useCases, command, entry.ID)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	posted, err := s.useCases.Post(ctx, publicapplication.PostEntryCommand{
		Context:   command.Context,
		CompanyID: entry.CompanyID,
		EntryID:   created.Entry.ID,
	})
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return posted.Entry, nil
}

func (s *defaultLedgerService) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	if s.useCases == nil {
		return ledger.JournalEntry{}, types.ErrCapabilityNotImplemented
	}
	return s.useCases.GetEntry(ctx, publicapplication.GetEntryQuery{
		Context:   commandContext(ctx, companyID, "sdk", "sdk", s.requirePrincipal),
		CompanyID: companyID,
		EntryID:   entryID,
	})
}

func (s *defaultLedgerService) ReverseEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, reason string) (ledger.JournalEntry, error) {
	if s.useCases == nil {
		return ledger.JournalEntry{}, types.ErrCapabilityNotImplemented
	}
	reversed, err := s.useCases.Reverse(ctx, publicapplication.ReverseEntryCommand{
		Context:       commandContext(ctx, companyID, "sdk", "sdk", s.requirePrincipal),
		CompanyID:     companyID,
		EntryID:       entryID,
		Reason:        reason,
		EffectiveDate: time.Now().UTC(),
	})
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return reversed.Entry, nil
}

type defaultReportingService struct {
	store storage.ReportingStore
}

func (s *defaultReportingService) GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	if s.store == nil {
		return reporting.TrialBalance{}, errors.New("reporting store not configured")
	}
	return s.store.GetTrialBalance(ctx, companyID, asOf, currency)
}

func (s *defaultReportingService) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	if s.store == nil {
		return reporting.BalanceSheetReport{}, errors.New("reporting store not configured")
	}
	return s.store.GetBalanceSheet(ctx, companyID, asOf, currency)
}

func (s *defaultReportingService) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	if s.store == nil {
		return reporting.IncomeStatementReport{}, errors.New("reporting store not configured")
	}
	return s.store.GetIncomeStatement(ctx, companyID, start, end, currency)
}

func (s *defaultReportingService) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	if s.store == nil {
		return reporting.GeneralLedgerReport{}, errors.New("reporting store not configured")
	}
	return s.store.GetGeneralLedger(ctx, companyID, accountID, start, end)
}

type defaultGovernanceService struct {
	store storage.GovernanceStore
}

func (s *defaultGovernanceService) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	if s.store == nil {
		return errors.New("governance store not configured")
	}
	return s.store.RecordAuditLog(ctx, eventID, details)
}

func (s *defaultGovernanceService) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	if s.store == nil {
		return "", errors.New("governance store not configured")
	}
	return s.store.GetApprovalFingerprint(ctx, entryID)
}

type defaultEventService struct {
	store            legacy.CoreLedgerStore
	router           *events.Router
	publicMasterData publicmasterdata.Store
	useCases         publicapplication.Service
	requirePrincipal bool
	mu               sync.Mutex
	keys             map[string]ledger.JournalEntry
	inflight         map[string]struct{}
}

func (s *defaultEventService) ProcessEvent(ctx context.Context, event events.AccountingEvent) (ledger.JournalEntry, error) {
	if s.useCases == nil {
		return ledger.JournalEntry{}, publicapplication.NewError(
			publicapplication.ErrDependencyUnavailable, "event ingestion application is unavailable", types.ErrCapabilityNotImplemented,
		)
	}
	if strings.TrimSpace(event.EventType) == "" {
		return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrValidation, "event type is required", nil)
	}
	if uuid.UUID(event.CompanyID) == uuid.Nil {
		return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrValidation, "event company id is required", nil)
	}
	if strings.TrimSpace(event.EventID) == "" {
		return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrValidation, "event id is required", nil)
	}
	if event.JournalDraft == nil && (event.Amount.IsNegative() || event.Amount.IsZero()) {
		return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrValidation, "event amount must be positive", nil)
	}
	if len(event.Dimensions) > 0 {
		return ledger.JournalEntry{}, wrapEventError(publicapplication.ErrValidation, ErrEventDimensionsUnsupported.Error(), ErrEventDimensionsUnsupported)
	}
	if event.JournalDraft != nil && len(event.Payload) > 0 {
		return ledger.JournalEntry{}, wrapEventError(publicapplication.ErrValidation, ErrEventPayloadUnsupported.Error(), ErrEventPayloadUnsupported)
	}
	if event.EffectiveDate.IsZero() && (event.JournalDraft == nil ||
		(event.JournalDraft.EffectiveDate.IsZero() && event.JournalDraft.PostingDate.IsZero())) {
		return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrValidation, "event effective date is required", nil)
	}

	requestContext := eventCommandContext(ctx, event, s.requirePrincipal)
	key := requestContext.IdempotencyKey
	companyUUID := uuid.UUID(event.CompanyID)
	idemStore, _ := s.store.(PublicEventIdempotencyStore)
	mapStore, _ := s.store.(PublicEventMappingStore)

	// B.4: resolve mapping (configurable per company/ledger) or accept ready draft.
	var (
		entry    ledger.JournalEntry
		err      error
		ruleUsed events.PostingRule
		hasRule  bool
	)
	if event.JournalDraft != nil {
		// Ready Journal Draft from adapter: canonical ingestion path.
		draft := *event.JournalDraft
		if draft.CompanyID == (types.CompanyID{}) || draft.CompanyID == types.CompanyID(uuid.Nil) {
			draft.CompanyID = event.CompanyID
		}
		if draft.CompanyID != event.CompanyID {
			return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrValidation, "journal draft company mismatch", nil)
		}
		if strings.TrimSpace(draft.SourceModule) == "" {
			draft.SourceModule = event.EventType
		}
		if strings.TrimSpace(draft.ReferenceID) == "" {
			draft.ReferenceID = event.EventID
		}
		if draft.EffectiveDate.IsZero() {
			draft.EffectiveDate = event.EffectiveDate
		}
		if draft.PostingDate.IsZero() {
			draft.PostingDate = draft.EffectiveDate
		}
		if draft.Status != "" && draft.Status != ledger.EntryDraft {
			return ledger.JournalEntry{}, publicapplication.NewError(
				publicapplication.ErrValidation, "event journal draft must be in draft status", nil,
			)
		}
		normalizeLedgerEntryIDs(&draft)
		entry = draft
	} else {
		if mapStore != nil && companyUUID != uuid.Nil {
			m, ok, mErr := mapStore.GetPublicEventAccountMapping(ctx, companyUUID, event.EventType, strings.TrimSpace(event.LedgerID))
			if mErr != nil {
				return ledger.JournalEntry{}, mErr
			}
			if !ok && strings.TrimSpace(event.LedgerID) != "" {
				m, ok, mErr = mapStore.GetPublicEventAccountMapping(ctx, companyUUID, event.EventType, "")
				if mErr != nil {
					return ledger.JournalEntry{}, mErr
				}
			}
			if ok {
				ruleUsed = events.PostingRule{
					EventType:           event.EventType,
					JournalCode:         m.JournalCode,
					DebitAccountCode:    m.DebitAccountCode,
					CreditAccountCode:   m.CreditAccountCode,
					DescriptionTemplate: m.DescriptionTemplate,
				}
				hasRule = true
			}
		}
		if !hasRule {
			rules := s.router.GetRules(event.EventType)
			if len(rules) == 0 {
				// B.3/B.4: no default guessing; require explicit mapping/rule or ready draft.
				return ledger.JournalEntry{}, fmt.Errorf("no account mapping/rule registered for event type %q", event.EventType)
			}
			ruleUsed = rules[0]
			hasRule = true
		}
		entry, err = s.constructEventEntry(ctx, ruleUsed, event)
		if err != nil {
			if errors.Is(err, ErrEventPayloadUnsupported) {
				return ledger.JournalEntry{}, wrapEventError(publicapplication.ErrValidation, ErrEventPayloadUnsupported.Error(), ErrEventPayloadUnsupported)
			}
			if errors.Is(err, ErrExchangeRateUnavailable) {
				return ledger.JournalEntry{}, wrapEventError(publicapplication.ErrDependencyUnavailable, ErrExchangeRateUnavailable.Error(), ErrExchangeRateUnavailable)
			}
			return ledger.JournalEntry{}, err
		}
	}
	normalizeLedgerEntryIDs(&entry)
	if err := validatePostingEntry(entry); err != nil {
		return ledger.JournalEntry{}, err
	}

	fp, err := publicEventFingerprint(event, ruleUsed)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("fingerprint accounting event: %w", err)
	}

	// B.2: durable idempotency when key+store available.
	if key != "" && idemStore != nil && companyUUID != uuid.Nil {
		rec := PublicEventIngestion{
			CompanyID: companyUUID, IdempotencyKey: key, EventType: event.EventType,
			PayloadHash: fp, EntryID: uuid.UUID(entry.ID), Entry: entry, Status: "processing",
		}
		existing, claimed, cErr := idemStore.ClaimPublicEventIngestion(ctx, rec)
		if cErr != nil {
			return ledger.JournalEntry{}, cErr
		}
		if claimed && existing.EntryID != uuid.Nil {
			if uuid.UUID(existing.Entry.ID) != uuid.Nil {
				entry = existing.Entry
			} else if existing.EntryID != uuid.UUID(entry.ID) {
				recovered, gErr := s.fetchPostedEntry(ctx, event.CompanyID, types.JournalEntryID(existing.EntryID), requestContext)
				if gErr != nil {
					return ledger.JournalEntry{}, fmt.Errorf("load event idempotency retry entry: %w", gErr)
				}
				entry = recovered
			}
		}
		if !claimed {
			if existing.Status == "processed" && existing.EntryID != uuid.Nil {
				if existing.PayloadHash != "" && existing.PayloadHash != fp {
					return ledger.JournalEntry{}, publicapplication.NewError(
						publicapplication.ErrConflict, "idempotency key conflicts with a different event", ErrIdempotencyKeyConflict,
					)
				}
				posted, gErr := s.fetchPostedEntry(ctx, event.CompanyID, types.JournalEntryID(existing.EntryID), requestContext)
				if gErr != nil {
					return ledger.JournalEntry{}, fmt.Errorf("load completed event idempotency result: %w", gErr)
				}
				if posted.Status != ledger.EntryPosted {
					return ledger.JournalEntry{}, publicapplication.NewError(
						publicapplication.ErrConflict, "completed event entry is not posted", ErrIdempotencyInProgress,
					)
				}
				return posted, nil
			}
			if existing.PayloadHash != "" && existing.PayloadHash != fp {
				return ledger.JournalEntry{}, publicapplication.NewError(
					publicapplication.ErrConflict, "idempotency key conflicts with a different event", ErrIdempotencyKeyConflict,
				)
			}
			if existing.Status == "processing" {
				recoveryEntry := existing.Entry
				if uuid.UUID(recoveryEntry.ID) == uuid.Nil && existing.EntryID != uuid.Nil {
					recovered, gErr := s.fetchPostedEntry(ctx, event.CompanyID, types.JournalEntryID(existing.EntryID), requestContext)
					if gErr != nil {
						return ledger.JournalEntry{}, errors.Join(ErrIdempotencyInProgress, gErr)
					}
					if recovered.Status != ledger.EntryPosted {
						return ledger.JournalEntry{}, publicapplication.NewError(
							publicapplication.ErrConflict, "event ingestion is still in progress", ErrIdempotencyInProgress,
						)
					}
					recoveryEntry = recovered
				}
				if uuid.UUID(recoveryEntry.ID) == uuid.Nil {
					return ledger.JournalEntry{}, publicapplication.NewError(
						publicapplication.ErrConflict, "event ingestion is still in progress", ErrIdempotencyInProgress,
					)
				}
				posted, pErr := s.postEventEntry(ctx, recoveryEntry, requestContext)
				if pErr != nil {
					recovered, gErr := s.fetchPostedEntry(ctx, event.CompanyID, types.JournalEntryID(recoveryEntry.ID), requestContext)
					if gErr != nil || recovered.Status != ledger.EntryPosted {
						return ledger.JournalEntry{}, publicapplication.NewError(
							publicapplication.ErrConflict, "event ingestion is still in progress",
							errors.Join(ErrIdempotencyInProgress, pErr, gErr),
						)
					}
					posted = recovered
				}
				if err := idemStore.CompletePublicEventIngestion(ctx, companyUUID, key, posted); err != nil {
					return posted, fmt.Errorf("complete recovered event idempotency record: %w", err)
				}
				return posted, nil
			}
			return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrConflict, "idempotency key is in an invalid state", nil)
		}
		posted, pErr := s.postEventEntry(ctx, entry, requestContext)
		if pErr != nil {
			if failErr := idemStore.FailPublicEventIngestion(ctx, companyUUID, key, pErr.Error()); failErr != nil {
				return ledger.JournalEntry{}, errors.Join(pErr, fmt.Errorf("mark event idempotency record failed: %w", failErr))
			}
			return ledger.JournalEntry{}, pErr
		}
		if err := idemStore.CompletePublicEventIngestion(ctx, companyUUID, key, posted); err != nil {
			return posted, fmt.Errorf("complete event idempotency record after posting: %w", err)
		}
		return posted, nil
	}

	// In-process idempotency fallback (non-durable backends).
	if key != "" {
		s.mu.Lock()
		if s.keys == nil {
			s.keys = make(map[string]ledger.JournalEntry)
		}
		if s.inflight == nil {
			s.inflight = make(map[string]struct{})
		}
		if existing, ok := s.keys[key]; ok {
			s.mu.Unlock()
			return existing, nil
		}
		if _, busy := s.inflight[key]; busy {
			s.mu.Unlock()
			return ledger.JournalEntry{}, errors.New("duplicate idempotency key is being processed")
		}
		s.inflight[key] = struct{}{}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, key)
			s.mu.Unlock()
		}()
	}

	posted, err := s.postEventEntry(ctx, entry, requestContext)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	if key != "" {
		s.mu.Lock()
		s.keys[key] = posted
		s.mu.Unlock()
	}
	return posted, nil
}

func wrapEventError(kind error, message string, cause error) error {
	return fmt.Errorf("%w: %w", publicapplication.NewError(kind, message, nil), cause)
}

func validatePostingEntry(entry ledger.JournalEntry) error {
	switch {
	case uuid.UUID(entry.CompanyID) == uuid.Nil:
		return publicapplication.NewError(publicapplication.ErrValidation, "company id is required", nil)
	case uuid.UUID(entry.JournalID) == uuid.Nil:
		return publicapplication.NewError(publicapplication.ErrValidation, "journal id is required", nil)
	case len(entry.Lines) < 2:
		return publicapplication.NewError(publicapplication.ErrValidation, "at least two journal lines are required", nil)
	case entry.EffectiveDate.IsZero() && entry.PostingDate.IsZero():
		return publicapplication.NewError(publicapplication.ErrValidation, "effective or posting date is required", nil)
	default:
		return nil
	}
}

func (s *defaultEventService) postEventEntry(
	ctx context.Context,
	entry ledger.JournalEntry,
	requestContext publicapplication.Context,
) (ledger.JournalEntry, error) {
	command := createDraftCommand(entry, requestContext)
	created, err := createDraftUseCase(ctx, s.useCases, command, entry.ID)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	posted, err := s.useCases.Post(ctx, publicapplication.PostEntryCommand{
		Context:   command.Context,
		CompanyID: entry.CompanyID,
		EntryID:   created.Entry.ID,
	})
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return posted.Entry, nil
}

func (s *defaultEventService) fetchPostedEntry(
	ctx context.Context,
	companyID types.CompanyID,
	entryID types.JournalEntryID,
	requestContext publicapplication.Context,
) (ledger.JournalEntry, error) {
	return s.useCases.GetEntry(ctx, publicapplication.GetEntryQuery{
		Context:   requestContext,
		CompanyID: companyID,
		EntryID:   entryID,
	})
}

func eventCommandContext(ctx context.Context, event events.AccountingEvent, requirePrincipal bool) publicapplication.Context {
	principal, hasPrincipal := publicapplication.PrincipalFromContext(ctx)
	if !hasPrincipal && !requirePrincipal {
		principal = publicapplication.Principal{
			ActorID:    "event-ingestion",
			CompanyIDs: []types.CompanyID{event.CompanyID},
		}
	}
	metadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	correlationID := strings.TrimSpace(metadata.CorrelationID)
	if correlationID == "" {
		correlationID = strings.TrimSpace(event.CorrelationID)
	}
	if correlationID == "" {
		correlationID = strings.TrimSpace(event.EventID)
	}
	idempotencyKey := strings.TrimSpace(event.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(metadata.IdempotencyKey)
	}
	sourceSystem := strings.TrimSpace(metadata.SourceSystem)
	if sourceSystem == "" {
		sourceSystem = strings.TrimSpace(event.SourceSystem)
	}
	if sourceSystem == "" {
		sourceSystem = "event"
	}
	return publicapplication.Context{
		Principal:      principal,
		CorrelationID:  correlationID,
		IdempotencyKey: idempotencyKey,
		SourceSystem:   sourceSystem,
	}
}

func commandContext(ctx context.Context, companyID types.CompanyID, fallbackActor, sourceSystem string, requirePrincipal bool) publicapplication.Context {
	metadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	if principal, ok := publicapplication.PrincipalFromContext(ctx); ok {
		return publicapplication.Context{
			Principal: principal, CorrelationID: metadata.CorrelationID,
			IdempotencyKey: metadata.IdempotencyKey, SourceSystem: metadataSource(metadata, sourceSystem),
		}
	}
	if requirePrincipal {
		return publicapplication.Context{
			CorrelationID: metadata.CorrelationID, IdempotencyKey: metadata.IdempotencyKey,
			SourceSystem: metadataSource(metadata, sourceSystem),
		}
	}
	return publicapplication.Context{
		Principal: publicapplication.Principal{
			ActorID:    fallbackActor,
			CompanyIDs: []types.CompanyID{companyID},
		},
		CorrelationID: metadata.CorrelationID, IdempotencyKey: metadata.IdempotencyKey,
		SourceSystem: metadataSource(metadata, sourceSystem),
	}
}

func metadataSource(metadata publicapplication.RequestMetadata, fallback string) string {
	if source := strings.TrimSpace(metadata.SourceSystem); source != "" {
		return source
	}
	return fallback
}

func createDraftCommand(entry ledger.JournalEntry, requestContext publicapplication.Context) publicapplication.CreateDraftCommand {
	exchangeRate := ""
	if !entry.ExchangeRate.IsZero() {
		exchangeRate = entry.ExchangeRate.String()
	}
	return publicapplication.CreateDraftCommand{
		Context:             requestContext,
		CompanyID:           entry.CompanyID,
		JournalID:           entry.JournalID,
		FiscalPeriodID:      entry.FiscalPeriodID,
		EffectiveDate:       entry.EffectiveDate,
		PostingDate:         entry.PostingDate,
		Description:         entry.Description,
		SourceModule:        entry.SourceModule,
		ReferenceID:         entry.ReferenceID,
		TransactionCurrency: entry.TransactionCurrency,
		ExchangeRate:        exchangeRate,
		Classification:      entry.Classification,
		Lines:               entry.Lines,
	}
}

func createDraftUseCase(
	ctx context.Context,
	service publicapplication.Service,
	command publicapplication.CreateDraftCommand,
	entryID types.JournalEntryID,
) (publicapplication.EntryResult, error) {
	if entryID != (types.JournalEntryID{}) {
		if creator, ok := service.(interface {
			CreateDraftWithID(context.Context, publicapplication.CreateDraftCommand, types.JournalEntryID) (publicapplication.EntryResult, error)
		}); ok {
			return creator.CreateDraftWithID(ctx, command, entryID)
		}
	}
	return service.CreateDraft(ctx, command)
}

func resolveDefaultJournalIDForStore(ctx context.Context, masterData publicmasterdata.Store, companyID types.CompanyID, journalCode string) (types.JournalID, error) {
	if masterData == nil {
		return types.JournalID{}, types.ErrCapabilityNotImplemented
	}
	companyID = types.CompanyID(uuid.UUID(companyID))
	if uuid.UUID(companyID) == uuid.Nil {
		return types.JournalID{}, errors.New("company id is required")
	}
	journalCode = strings.TrimSpace(journalCode)
	if journalCode == "" {
		journalCode = "GENERAL"
	}
	journal, err := masterData.GetJournalByCode(ctx, companyID, journalCode)
	if err == nil {
		return types.JournalID(journal.ID), nil
	}
	if !errors.Is(err, publicmasterdata.ErrJournalNotFound) {
		return types.JournalID{}, fmt.Errorf("resolve journal %q: %w", journalCode, err)
	}
	if journalCode != "GENERAL" {
		return types.JournalID{}, fmt.Errorf("resolve journal %q: %w", journalCode, err)
	}
	journalID := publicmasterdata.NewJournalID()
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID:        journalID,
		CompanyID: companyID,
		Code:      journalCode,
		Name:      "General Journal",
	}); err != nil {
		if errors.Is(err, publicmasterdata.ErrDuplicateJournalCode) {
			journal, retryErr := masterData.GetJournalByCode(ctx, companyID, journalCode)
			if retryErr == nil {
				return types.JournalID(journal.ID), nil
			}
			return types.JournalID{}, fmt.Errorf("resolve journal %q: %w", journalCode, retryErr)
		}
		return types.JournalID{}, fmt.Errorf("create journal %q: %w", journalCode, err)
	}
	return types.JournalID(journalID), nil
}

func (s *defaultEventService) constructEventEntry(ctx context.Context, rule events.PostingRule, event events.AccountingEvent) (ledger.JournalEntry, error) {
	amt := event.Amount
	if amt.Currency() == "" {
		return ledger.JournalEntry{}, errors.New("event amount currency is required")
	}
	if s.publicMasterData == nil {
		return ledger.JournalEntry{}, types.ErrCapabilityNotImplemented
	}
	company, err := s.publicMasterData.GetCompany(ctx, event.CompanyID)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	if !strings.EqualFold(amt.Currency(), company.FunctionalCurrency) {
		return ledger.JournalEntry{}, ErrExchangeRateUnavailable
	}
	journalID, err := resolveDefaultJournalIDForStore(ctx, s.publicMasterData, event.CompanyID, rule.JournalCode)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	if len(event.Payload) > 0 && !strings.Contains(rule.DescriptionTemplate, ".Payload") {
		return ledger.JournalEntry{}, publicapplication.NewError(
			publicapplication.ErrValidation, ErrEventPayloadUnsupported.Error(), ErrEventPayloadUnsupported,
		)
	}
	zeroAmt, err := types.NewMoney(amt.Amount().Sub(amt.Amount()), amt.Currency())
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	debitAccountID, err := s.resolveRuleAccount(ctx, event.CompanyID, rule.DebitAccountCode)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("resolve debit account %q: %w", rule.DebitAccountCode, err)
	}
	creditAccountID, err := s.resolveRuleAccount(ctx, event.CompanyID, rule.CreditAccountCode)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("resolve credit account %q: %w", rule.CreditAccountCode, err)
	}
	desc, err := renderEventDescription(rule.DescriptionTemplate, event)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	effective := event.EffectiveDate
	if effective.IsZero() {
		effective = time.Now().UTC()
	}
	return ledger.JournalEntry{
		ID:             types.NewJournalEntryID(),
		CompanyID:      event.CompanyID,
		JournalID:      journalID,
		Status:         ledger.EntryDraft,
		Classification: ledger.EntryClassRegular,
		EffectiveDate:  effective,
		PostingDate:    effective,
		Description:    desc,
		SourceModule:   event.EventType,
		ReferenceID:    event.EventID,
		Lines: []ledger.EntryLine{
			{
				ID:                  types.NewJournalEntryLineID(),
				AccountID:           debitAccountID,
				TransactionAmount:   amt,
				TransactionCurrency: amt.Currency(),
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     amt,
				FunctionalCredit:    zeroAmt,
			},
			{
				ID:                  types.NewJournalEntryLineID(),
				AccountID:           creditAccountID,
				TransactionAmount:   amt,
				TransactionCurrency: amt.Currency(),
				ExchangeRate:        decimal.NewFromInt(1),
				FunctionalDebit:     zeroAmt,
				FunctionalCredit:    amt,
			},
		},
		CreatedBy: "event:" + event.EventType,
	}, nil
}

func renderEventDescription(descriptionTemplate string, event events.AccountingEvent) (string, error) {
	if strings.TrimSpace(descriptionTemplate) == "" {
		return fmt.Sprintf("Event: %s (%s)", event.EventType, event.EventID), nil
	}
	parsed, err := template.New("event-description").Option("missingkey=error").Parse(descriptionTemplate)
	if err != nil {
		return "", fmt.Errorf("parse event description template: %w", err)
	}
	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, struct {
		EventID       string
		EventType     string
		CompanyID     types.CompanyID
		LedgerID      string
		EffectiveDate time.Time
		Amount        types.Money
		Payload       map[string]any
	}{
		EventID: event.EventID, EventType: event.EventType, CompanyID: event.CompanyID,
		LedgerID: event.LedgerID, EffectiveDate: event.EffectiveDate,
		Amount: event.Amount, Payload: event.Payload,
	}); err != nil {
		return "", fmt.Errorf("render event description template: %w", err)
	}
	description := strings.TrimSpace(rendered.String())
	if !strings.Contains(descriptionTemplate, "{{") {
		description = fmt.Sprintf("%s: %s (%s)", description, event.EventType, event.EventID)
	}
	if description == "" {
		return "", errors.New("event description template rendered an empty description")
	}
	return description, nil
}

func (s *defaultEventService) resolveRuleAccount(ctx context.Context, companyID types.CompanyID, code string) (types.AccountID, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return types.AccountID{}, errors.New("account code is required")
	}
	if s.publicMasterData != nil {
		acc, err := s.publicMasterData.GetAccountByCode(ctx, companyID, code)
		if err != nil {
			return types.AccountID{}, err
		}
		return acc.ID, nil
	}
	// Fallback: try core store lookup by code if it exposes internal-style repo? no direct API.
	// Without masterdata we cannot safely resolve account codes; fail explicitly (P0-3).
	return types.AccountID{}, errors.New("account code resolution requires master data service")
}
