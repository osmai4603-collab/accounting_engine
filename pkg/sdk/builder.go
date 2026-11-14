package sdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
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
	publicevents "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"
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
	coreStore         legacy.CoreLedgerStore
	ports             storage.Ports
	portsConfigured   bool
	reportingStore    storage.ReportingStore
	governanceStore   storage.GovernanceStore
	authorizer        publicapplication.Authorizer
	standards         []standards.StandardModule
	standardsRegistry *standards.Registry
	postingConfig     PostingConfig
	postingCaps       storage.PostingCapabilities
	eventWaitTimeout  time.Duration
	eventWaitInterval time.Duration
	optsErrs          []error
}

const (
	defaultEventIdempotencyWaitTimeout  = 2 * time.Second
	defaultEventIdempotencyWaitInterval = 25 * time.Millisecond
)

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

// WithEventIdempotencyWaitPolicy bounds how long ProcessEvent waits for a
// concurrent owner of the same idempotency key to finish before it either
// recovers the committed result or reports ErrIdempotencyInProgress. A zero
// timeout uses the conservative default.
func WithEventIdempotencyWaitPolicy(timeout, interval time.Duration) Option {
	return func(b *EngineBuilder) error {
		if timeout < 0 {
			return errors.New("event idempotency wait timeout must not be negative")
		}
		if interval < 0 {
			return errors.New("event idempotency wait interval must not be negative")
		}
		b.eventWaitTimeout = timeout
		b.eventWaitInterval = interval
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
	var idempotencyStore PublicEventIdempotencyStore
	var mappingStore PublicEventMappingStore

	if b.portsConfigured {
		app, err := sdkapplication.NewApplicationFromPortsWithPostingCapabilities(
			b.ports, b.postingConfig.internal(), b.postingCaps,
		)
		if err != nil {
			return nil, fmt.Errorf("application layer: %w", err)
		}
		useCases = sdkapplication.NewPublicService(app, b.authorizer)
		idempotencyStore = b.ports.Events.Idempotency
		mappingStore = b.ports.Events.Mappings
		if store, ok := b.ports.MasterData.(publicmasterdata.Store); ok {
			publicMasterData = store
		}
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
		if store, ok := b.coreStore.(PublicEventIdempotencyStore); ok {
			// The legacy core-store path predates the canonical lifecycle and
			// source scoping. Narrow the capability so this path keeps writing
			// its historical status vocabulary instead of silently upgrading
			// stored records to the new canonical values.
			idempotencyStore = legacyPublicEventStore{PublicEventIdempotencyStore: store}
		}
		mappingStore, _ = b.coreStore.(PublicEventMappingStore)
	}

	scheduler := standards.NewDefaultScheduler()

	moduleErrs := b.initializeStandards(ctx, router)
	if len(moduleErrs) > 0 {
		return nil, errors.Join(moduleErrs...)
	}

	// Mark scheduler ready after successful initialization of core dependencies and modules
	scheduler.SetReady(true)

	// Register scheduled jobs from active modules
	for _, mod := range b.standards {
		if mod == nil {
			continue
		}
		if err := mod.RunScheduledJobs(ctx, scheduler); err != nil {
			if b.standardsRegistry != nil {
				b.standardsRegistry.SetDisabledReason(fmt.Sprintf("module:%s:jobs", mod.Name()), err.Error())
			}
		}
	}

	inst := &engineInstance{
		coreStore:         b.coreStore,
		reportingStore:    b.reportingStore,
		governanceStore:   b.governanceStore,
		standards:         b.standards,
		standardsRegistry: b.standardsRegistry,
		scheduler:         scheduler,
		eventRouter:       router,
		eventService:      &defaultEventService{}, // initialized below
		publicMasterData:  publicMasterData,
		useCases:          useCases,
		requirePrincipal:  b.authorizer != nil,
	}

	inst.eventService = &defaultEventService{
		store:            b.coreStore,
		idempotencyStore: idempotencyStore,
		mappingStore:     mappingStore,
		router:           router,
		publicMasterData: inst.publicMasterData,
		useCases:         inst.useCases,
		requirePrincipal: inst.requirePrincipal,
		waitTimeout:      b.eventWaitTimeout,
		waitInterval:     b.eventWaitInterval,
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

	if b.standardsRegistry == nil {
		b.standardsRegistry = standards.NewRegistry()
	}

	// Core Capabilities
	b.standardsRegistry.RegisterCapability(standards.CapLedgerPosting, "engine_core")
	b.standardsRegistry.RegisterCapability(standards.CapMasterData, "engine_core")

	// Preflight validation for Reporting capability dependencies
	if b.reportingStore != nil || (b.portsConfigured && b.ports.Balance != nil && b.ports.MasterData != nil && b.ports.Entries != nil) {
		b.standardsRegistry.RegisterCapability(standards.CapReporting, "reporting_service")
	} else {
		b.standardsRegistry.SetDisabledReason("capability:reporting", "reporting data sources missing (BalanceReader, MasterDataReader, EntryReader or ReportingStore)")
	}

	// Preflight validation for Governance capability dependencies
	if b.governanceStore != nil || (b.portsConfigured && b.ports.Audit != nil) {
		b.standardsRegistry.RegisterCapability(standards.CapGovernance, "governance_service")
		b.standardsRegistry.RegisterCapability(standards.CapAuditTrail, "governance_service")
		b.standardsRegistry.RegisterCapability(standards.CapApproval, "governance_service")
	} else {
		b.standardsRegistry.SetDisabledReason("capability:governance", "governance required stores missing (AuditWriter or GovernanceStore)")
	}

	if len(b.standards) > 0 {
		registry := b.standardsRegistry
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
// succeeded. If initialization fails on any module, all previously initialized
// modules are rolled back in reverse topological order.
func (b *EngineBuilder) initializeStandards(ctx context.Context, router *events.Router) []error {
	var initializedMods []standards.StandardModule

	rollback := func(initErr error) []error {
		var combinedErrs []error
		combinedErrs = append(combinedErrs, initErr)

		// Rollback initialized modules in reverse topological order
		for i := len(initializedMods) - 1; i >= 0; i-- {
			mod := initializedMods[i]
			if closeErr := mod.Close(ctx); closeErr != nil {
				combinedErrs = append(combinedErrs, fmt.Errorf("module %s rollback close failed: %w", mod.Name(), closeErr))
			}
		}
		return combinedErrs
	}

	for _, mod := range b.standards {
		if mod == nil {
			continue
		}
		name := mod.Name()
		if err := mod.Initialize(ctx, b.coreStore); err != nil {
			initErr := fmt.Errorf("standard module %s: initialization failed: %w", name, err)
			return rollback(initErr)
		}
		initializedMods = append(initializedMods, mod)

		if err := mod.RegisterRules(router); err != nil {
			ruleErr := fmt.Errorf("standard module %s: rule registration failed: %w", name, err)
			return rollback(ruleErr)
		}
	}
	return nil
}

type engineInstance struct {
	coreStore         legacy.CoreLedgerStore
	reportingStore    storage.ReportingStore
	governanceStore   storage.GovernanceStore
	standards         []standards.StandardModule
	standardsRegistry *standards.Registry
	scheduler         standards.SchedulerPort
	eventRouter       *events.Router
	eventService      *defaultEventService
	publicMasterData  publicmasterdata.Store
	useCases          publicapplication.Service
	requirePrincipal  bool

	closeOnce sync.Once
	closeErr  error
}

func (e *engineInstance) Ledger() LedgerService {
	return &defaultLedgerService{
		useCases:         e.useCases,
		requirePrincipal: e.requirePrincipal,
	}
}

func (e *engineInstance) Reporting() ReportingService {
	if e.reportingStore == nil {
		return &disabledReportingService{}
	}
	return &defaultReportingService{store: e.reportingStore}
}

func (e *engineInstance) Governance() GovernanceService {
	if e.governanceStore == nil {
		return &disabledGovernanceService{}
	}
	return &defaultGovernanceService{store: e.governanceStore}
}

func (e *engineInstance) Events() EventIngestionService {
	return e.eventService
}

func (e *engineInstance) Scheduler() standards.SchedulerPort {
	return e.scheduler
}

func (e *engineInstance) Standards() StandardsService {
	return &defaultStandardsService{
		registry:  e.standardsRegistry,
		scheduler: e.scheduler,
	}
}

type defaultStandardsService struct {
	registry  *standards.Registry
	scheduler standards.SchedulerPort
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

func (s *defaultStandardsService) Scheduler() standards.SchedulerPort {
	return s.scheduler
}

func (e *engineInstance) Close() error {
	return e.CloseWithContext(context.Background())
}

func (e *engineInstance) CloseWithContext(ctx context.Context) error {
	e.closeOnce.Do(func() {
		var errs []error
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
		}
		// Close standard modules in reverse topological order so dependents clean up first.
		for i := len(e.standards) - 1; i >= 0; i-- {
			mod := e.standards[i]
			if err := mod.Close(ctx); err != nil {
				errs = append(errs, fmt.Errorf("module %s close failed: %w", mod.Name(), err))
			}
		}
		if len(errs) > 0 {
			e.closeErr = errors.Join(errs...)
		}
	})
	return e.closeErr
}

type disabledReportingService struct{}

func (d *disabledReportingService) GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	return reporting.TrialBalance{}, types.ErrCapabilityNotImplemented
}
func (d *disabledReportingService) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	return reporting.BalanceSheetReport{}, types.ErrCapabilityNotImplemented
}
func (d *disabledReportingService) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	return reporting.IncomeStatementReport{}, types.ErrCapabilityNotImplemented
}
func (d *disabledReportingService) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	return reporting.GeneralLedgerReport{}, types.ErrCapabilityNotImplemented
}

type disabledGovernanceService struct{}

func (d *disabledGovernanceService) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	return types.ErrCapabilityNotImplemented
}
func (d *disabledGovernanceService) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	return "", types.ErrCapabilityNotImplemented
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
		return reporting.TrialBalance{}, fmt.Errorf("%w: reporting store not configured", types.ErrCapabilityNotImplemented)
	}
	return s.store.GetTrialBalance(ctx, companyID, asOf, currency)
}

func (s *defaultReportingService) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	if s.store == nil {
		return reporting.BalanceSheetReport{}, fmt.Errorf("%w: reporting store not configured", types.ErrCapabilityNotImplemented)
	}
	return s.store.GetBalanceSheet(ctx, companyID, asOf, currency)
}

func (s *defaultReportingService) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	if s.store == nil {
		return reporting.IncomeStatementReport{}, fmt.Errorf("%w: reporting store not configured", types.ErrCapabilityNotImplemented)
	}
	return s.store.GetIncomeStatement(ctx, companyID, start, end, currency)
}

func (s *defaultReportingService) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	if s.store == nil {
		return reporting.GeneralLedgerReport{}, fmt.Errorf("%w: reporting store not configured", types.ErrCapabilityNotImplemented)
	}
	return s.store.GetGeneralLedger(ctx, companyID, accountID, start, end)
}

type defaultGovernanceService struct {
	store storage.GovernanceStore
}

func (s *defaultGovernanceService) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	if s.store == nil {
		return fmt.Errorf("%w: governance store not configured", types.ErrCapabilityNotImplemented)
	}
	return s.store.RecordAuditLog(ctx, eventID, details)
}

func (s *defaultGovernanceService) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("%w: governance store not configured", types.ErrCapabilityNotImplemented)
	}
	return s.store.GetApprovalFingerprint(ctx, entryID)
}

type defaultEventService struct {
	store legacy.CoreLedgerStore
	// idempotencyStore/mappingStore are resolved once by the builder. Keeping
	// them explicit makes WithStoragePorts use the adapter's durable stores
	// instead of silently relying on process-local state.
	idempotencyStore PublicEventIdempotencyStore
	mappingStore     PublicEventMappingStore
	router           *events.Router
	publicMasterData publicmasterdata.Store
	useCases         publicapplication.Service
	requirePrincipal bool
	waitTimeout      time.Duration
	waitInterval     time.Duration
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
	idemStore, mapStore := s.eventStores()

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

	if key == "" {
		// No caller-supplied idempotency key: this is a single posting request.
		return s.postEventEntry(ctx, entry, requestContext)
	}

	// Idempotent event ingestion is durable-only. Falling back to a local map
	// would silently lose the guarantee across restarts and instances.
	if idemStore == nil {
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrDependencyUnavailable,
			"durable event idempotency store is not configured",
			ErrIdempotencyStateUnavailable,
		)
	}
	sourceSystem := publicevents.NormalizeSourceSystem(requestContext.SourceSystem)
	if sourceSystem != publicevents.DefaultSourceSystem {
		if _, ok := idemStore.(PublicEventScopedIdempotencyStore); !ok {
			return ledger.JournalEntry{}, wrapEventError(
				publicapplication.ErrDependencyUnavailable,
				"idempotency store does not support source-system scoping",
				ErrIdempotencyStateUnavailable,
			)
		}
	}

	rec := PublicEventIngestion{
		CompanyID:          companyUUID,
		IdempotencyKey:     key,
		SourceSystem:       sourceSystem,
		EventType:          event.EventType,
		PayloadHash:        fp,
		PayloadHashVersion: publicEventPayloadHashVersion,
		EntryID:            uuid.UUID(entry.ID),
		Entry:              entry,
		Status:             publicevents.StatusStarted,
	}
	existing, claimed, cErr := idemStore.ClaimPublicEventIngestion(ctx, rec)
	if cErr != nil {
		if errors.Is(cErr, storage.ErrUniqueConstraint) {
			return ledger.JournalEntry{}, wrapEventError(
				publicapplication.ErrConflict, "idempotency key conflicts with a different event", ErrIdempotencyKeyConflict,
			)
		}
		return ledger.JournalEntry{}, cErr
	}
	if !claimed {
		return s.handleExistingIngestion(ctx, idemStore, companyUUID, sourceSystem, key, fp, existing, entry, requestContext)
	}
	return s.executeClaimedIngestion(ctx, idemStore, companyUUID, sourceSystem, key, fp, existing, entry, requestContext)
}

// eventStores resolves the durable event stores, falling back to a legacy core
// store capability for WithCoreStorage callers.
// legacyPublicEventStore exposes only the stable idempotency surface of a
// WithCoreStorage implementation. Hiding optional scoped/classified
// capabilities keeps the legacy path byte-compatible with pre-P3-04 status
// values ("processing", "processed", "failed").
type legacyPublicEventStore struct {
	PublicEventIdempotencyStore
}

func (s *defaultEventService) eventStores() (PublicEventIdempotencyStore, PublicEventMappingStore) {
	idemStore := s.idempotencyStore
	mappingStore := s.mappingStore
	if idemStore == nil && s.store != nil {
		idemStore, _ = s.store.(PublicEventIdempotencyStore)
	}
	if mappingStore == nil && s.store != nil {
		mappingStore, _ = s.store.(PublicEventMappingStore)
	}
	return idemStore, mappingStore
}

func (s *defaultEventService) waitPolicy() (time.Duration, time.Duration) {
	timeout := s.waitTimeout
	if timeout <= 0 {
		timeout = defaultEventIdempotencyWaitTimeout
	}
	interval := s.waitInterval
	if interval <= 0 {
		interval = defaultEventIdempotencyWaitInterval
	}
	return timeout, interval
}

func (s *defaultEventService) executeClaimedIngestion(
	ctx context.Context,
	idemStore PublicEventIdempotencyStore,
	companyID uuid.UUID,
	sourceSystem, key, payloadHash string,
	record PublicEventIngestion,
	entry ledger.JournalEntry,
	requestContext publicapplication.Context,
) (ledger.JournalEntry, error) {
	working := entry
	if record.EntryID != uuid.Nil {
		working.ID = types.JournalEntryID(record.EntryID)
	}
	if uuid.UUID(record.Entry.ID) != uuid.Nil {
		// Prefer the exact snapshot reserved by the claim. For a reclaimed
		// record this preserves the original draft/entry identity.
		working = record.Entry
	}
	if uuid.UUID(working.ID) != uuid.Nil {
		// Re-check before doing anything: a previous attempt (or another
		// process) may already have committed the entry.
		current, getErr := s.fetchPostedEntry(ctx, entry.CompanyID, working.ID, requestContext)
		if getErr == nil && uuid.UUID(current.ID) != uuid.Nil {
			if current.Status == ledger.EntryPosted {
				if err := s.completeIngestion(ctx, idemStore, companyID, sourceSystem, key, payloadHash, current); err != nil {
					return ledger.JournalEntry{}, fmt.Errorf("complete recovered event idempotency record: %w", err)
				}
				return current, nil
			}
			if current.Status == ledger.EntryDraft {
				working = current
			}
		} else if getErr != nil && !errors.Is(getErr, publicapplication.ErrNotFound) {
			return ledger.JournalEntry{}, fmt.Errorf("check event entry before retry: %w", getErr)
		}
	}

	posted, postErr := s.postEventEntry(ctx, working, requestContext)
	if postErr != nil {
		if recovered, ok := s.recoverPostedEntry(ctx, entry.CompanyID, working.ID, requestContext); ok {
			if err := s.completeIngestion(ctx, idemStore, companyID, sourceSystem, key, payloadHash, recovered); err != nil {
				return ledger.JournalEntry{}, fmt.Errorf("complete committed event idempotency record: %w", err)
			}
			return recovered, nil
		}
		failureClass := classifyEventFailure(postErr)
		if failErr := s.failIngestion(ctx, idemStore, companyID, sourceSystem, key, failureClass, postErr.Error()); failErr != nil {
			return ledger.JournalEntry{}, errors.Join(postErr, fmt.Errorf("mark event idempotency record failed: %w", failErr))
		}
		return ledger.JournalEntry{}, postErr
	}
	if err := s.completeIngestion(ctx, idemStore, companyID, sourceSystem, key, payloadHash, posted); err != nil {
		// The entry is committed but the record could not be completed. Leave
		// the record in started state so a retry verifies and completes it
		// instead of posting again.
		return ledger.JournalEntry{}, fmt.Errorf("complete event idempotency record after posting: %w", err)
	}
	return posted, nil
}

func (s *defaultEventService) handleExistingIngestion(
	ctx context.Context,
	idemStore PublicEventIdempotencyStore,
	companyID uuid.UUID,
	sourceSystem, key, payloadHash string,
	existing PublicEventIngestion,
	entry ledger.JournalEntry,
	requestContext publicapplication.Context,
) (ledger.JournalEntry, error) {
	if existing.PayloadHash != "" && existing.PayloadHash != payloadHash {
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrConflict, "idempotency key conflicts with a different event", ErrIdempotencyKeyConflict,
		)
	}
	switch publicevents.NormalizeStatus(existing.Status) {
	case publicevents.StatusCompleted:
		return s.completedIngestionResult(ctx, existing, requestContext)
	case publicevents.StatusTerminalFailure:
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrConflict,
			"idempotency key previously failed terminally",
			ErrIdempotencyTerminalFailure,
		)
	case publicevents.StatusRetryableFailure:
		// The adapter normally reclaims retryable records during Claim. If it
		// does not, the caller may retry after the recorded transient failure.
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrDependencyUnavailable,
			"previous event ingestion attempt failed transiently; retry the event",
			errors.Join(ErrIdempotencyInProgress, failureReasonError(existing)),
		)
	case publicevents.StatusStarted:
		rec, terminal, waitErr := s.waitForIngestion(ctx, idemStore, companyID, sourceSystem, key)
		if waitErr != nil {
			return ledger.JournalEntry{}, waitErr
		}
		if terminal {
			if rec.PayloadHash != "" && rec.PayloadHash != payloadHash {
				return ledger.JournalEntry{}, wrapEventError(
					publicapplication.ErrConflict, "idempotency key conflicts with a different event", ErrIdempotencyKeyConflict,
				)
			}
			switch publicevents.NormalizeStatus(rec.Status) {
			case publicevents.StatusCompleted:
				return s.completedIngestionResult(ctx, rec, requestContext)
			case publicevents.StatusTerminalFailure:
				return ledger.JournalEntry{}, wrapEventError(
					publicapplication.ErrConflict,
					"idempotency key previously failed terminally",
					ErrIdempotencyTerminalFailure,
				)
			}
		}
		// The wait policy expired: take over recovery only when the record has
		// a concrete entry identity, so at most one entry can ever exist.
		return s.recoverStartedIngestion(ctx, idemStore, companyID, sourceSystem, key, payloadHash, existing, entry, requestContext)
	default:
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrConflict, "idempotency key is in an invalid state", ErrIdempotencyStateConflict,
		)
	}
}

// waitForIngestion polls the durable record until it reaches a terminal state
// or the configured wait budget expires. It returns terminal=true only when the
// record reached a state that can be answered without taking over recovery.
func (s *defaultEventService) waitForIngestion(
	ctx context.Context,
	idemStore PublicEventIdempotencyStore,
	companyID uuid.UUID,
	sourceSystem, key string,
) (PublicEventIngestion, bool, error) {
	timeout, interval := s.waitPolicy()
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return PublicEventIngestion{}, false, nil
		}
		select {
		case <-ctx.Done():
			return PublicEventIngestion{}, false, ctx.Err()
		case <-time.After(interval):
		}
		rec, found, err := s.getIngestion(ctx, idemStore, companyID, sourceSystem, key)
		if err != nil {
			return PublicEventIngestion{}, false, err
		}
		if !found {
			return PublicEventIngestion{}, false, nil
		}
		switch publicevents.NormalizeStatus(rec.Status) {
		case publicevents.StatusCompleted, publicevents.StatusTerminalFailure,
			publicevents.StatusRetryableFailure:
			return rec, true, nil
		}
	}
}

func (s *defaultEventService) recoverStartedIngestion(
	ctx context.Context,
	idemStore PublicEventIdempotencyStore,
	companyID uuid.UUID,
	sourceSystem, key, payloadHash string,
	existing PublicEventIngestion,
	entry ledger.JournalEntry,
	requestContext publicapplication.Context,
) (ledger.JournalEntry, error) {
	// Recovery must use only the identity reserved by the durable claim. The
	// incoming event belongs to this caller and may describe a different draft;
	// taking over an in-flight record without a reserved identity could create
	// a second entry for the same key.
	recovery := ledger.JournalEntry{}
	if existing.EntryID != uuid.Nil {
		recovery.ID = types.JournalEntryID(existing.EntryID)
	}
	if uuid.UUID(existing.Entry.ID) != uuid.Nil {
		recovery = existing.Entry
	}
	if uuid.UUID(recovery.ID) == uuid.Nil {
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrConflict, "event ingestion is still in progress", ErrIdempotencyInProgress,
		)
	}
	if posted, ok := s.recoverPostedEntry(ctx, entry.CompanyID, recovery.ID, requestContext); ok {
		if err := s.completeIngestion(ctx, idemStore, companyID, sourceSystem, key, payloadHash, posted); err != nil {
			return ledger.JournalEntry{}, fmt.Errorf("complete recovered event idempotency record: %w", err)
		}
		return posted, nil
	}
	posted, postErr := s.postEventEntry(ctx, recovery, requestContext)
	if postErr != nil {
		if recovered, ok := s.recoverPostedEntry(ctx, entry.CompanyID, recovery.ID, requestContext); ok {
			if err := s.completeIngestion(ctx, idemStore, companyID, sourceSystem, key, payloadHash, recovered); err != nil {
				return ledger.JournalEntry{}, fmt.Errorf("complete recovered event idempotency record: %w", err)
			}
			return recovered, nil
		}
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrConflict, "event ingestion is still in progress",
			errors.Join(ErrIdempotencyInProgress, postErr),
		)
	}
	if err := s.completeIngestion(ctx, idemStore, companyID, sourceSystem, key, payloadHash, posted); err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("complete recovered event idempotency record: %w", err)
	}
	return posted, nil
}

func (s *defaultEventService) completedIngestionResult(
	ctx context.Context,
	record PublicEventIngestion,
	requestContext publicapplication.Context,
) (ledger.JournalEntry, error) {
	if record.EntryID == uuid.Nil {
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrConflict, "completed event record has no entry id", ErrIdempotencyInProgress,
		)
	}
	posted, err := s.fetchPostedEntry(ctx, types.CompanyID(record.CompanyID), types.JournalEntryID(record.EntryID), requestContext)
	if err != nil {
		return ledger.JournalEntry{}, fmt.Errorf("load completed event idempotency result: %w", err)
	}
	if posted.Status != ledger.EntryPosted {
		return ledger.JournalEntry{}, wrapEventError(
			publicapplication.ErrConflict, "completed event entry is not posted", ErrIdempotencyInProgress,
		)
	}
	return posted, nil
}

func (s *defaultEventService) recoverPostedEntry(
	ctx context.Context,
	companyID types.CompanyID,
	entryID types.JournalEntryID,
	requestContext publicapplication.Context,
) (ledger.JournalEntry, bool) {
	if uuid.UUID(entryID) == uuid.Nil {
		return ledger.JournalEntry{}, false
	}
	entry, err := s.fetchPostedEntry(ctx, companyID, entryID, requestContext)
	if err != nil || uuid.UUID(entry.ID) == uuid.Nil || entry.Status != ledger.EntryPosted {
		return ledger.JournalEntry{}, false
	}
	return entry, true
}

func (s *defaultEventService) getIngestion(
	ctx context.Context,
	idemStore PublicEventIdempotencyStore,
	companyID uuid.UUID,
	sourceSystem, key string,
) (PublicEventIngestion, bool, error) {
	if scoped, ok := idemStore.(PublicEventScopedIdempotencyStore); ok {
		return scoped.GetPublicEventIngestionForSource(ctx, companyID, sourceSystem, key)
	}
	return idemStore.GetPublicEventIngestion(ctx, companyID, key)
}

func (s *defaultEventService) completeIngestion(
	ctx context.Context,
	idemStore PublicEventIdempotencyStore,
	companyID uuid.UUID,
	sourceSystem, key, payloadHash string,
	entry ledger.JournalEntry,
) error {
	if scoped, ok := idemStore.(PublicEventScopedIdempotencyStore); ok {
		return scoped.CompletePublicEventIngestionForSource(ctx, companyID, sourceSystem, key, payloadHash, entry)
	}
	return idemStore.CompletePublicEventIngestion(ctx, companyID, key, entry)
}

func (s *defaultEventService) failIngestion(
	ctx context.Context,
	idemStore PublicEventIdempotencyStore,
	companyID uuid.UUID,
	sourceSystem, key, failureClass, reason string,
) error {
	if classified, ok := idemStore.(publicevents.ClassifiedIdempotencyStore); ok {
		return classified.FailPublicEventIngestionClassified(ctx, companyID, sourceSystem, key, failureClass, reason)
	}
	if scoped, ok := idemStore.(PublicEventScopedIdempotencyStore); ok {
		return scoped.FailPublicEventIngestionForSource(ctx, companyID, sourceSystem, key, failureClass, reason)
	}
	return idemStore.FailPublicEventIngestion(ctx, companyID, key, reason)
}

func classifyEventFailure(err error) string {
	if err == nil {
		return ""
	}
	if storage.IsRetryable(err) ||
		errors.Is(err, storage.ErrTransactionAborted) ||
		errors.Is(err, publicapplication.ErrDependencyUnavailable) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return publicevents.FailureClassTransient
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return publicevents.FailureClassTransient
	}
	// Default to terminal: never re-run an operation whose failure is not
	// known to be transient.
	return publicevents.FailureClassTerminal
}

func failureReasonError(record PublicEventIngestion) error {
	if strings.TrimSpace(record.FailureReason) == "" {
		return nil
	}
	return errors.New(record.FailureReason)
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
