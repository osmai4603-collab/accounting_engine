package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/reporting"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/spi/standards"
	"accounting_engine/pkg/spi/storage"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type EngineBuilder struct {
	coreStore       storage.CoreLedgerStore
	reportingStore  storage.ReportingStore
	governanceStore storage.GovernanceStore
	standards       []standards.StandardModule
	optsErrs        []error
}

type Option func(*EngineBuilder) error

func WithCoreStorage(store storage.CoreLedgerStore) Option {
	return func(b *EngineBuilder) error {
		if store == nil {
			return errors.New("core ledger store is nil")
		}
		b.coreStore = store
		return nil
	}
}

func WithReporting(store storage.ReportingStore) Option {
	return func(b *EngineBuilder) error {
		b.reportingStore = store
		return nil
	}
}

func WithGovernance(store storage.GovernanceStore) Option {
	return func(b *EngineBuilder) error {
		b.governanceStore = store
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

func (b *EngineBuilder) Build(ctx context.Context) (Engine, error) {
	var errs []error
	errs = append(errs, b.optsErrs...)
	if b.coreStore == nil {
		errs = append(errs, errors.New("core ledger store is required but missing"))
	}

	seenModules := map[string]standards.StandardModule{}
	for _, mod := range b.standards {
		if mod == nil {
			errs = append(errs, errors.New("standard module is nil"))
			continue
		}
		name := mod.Name()
		if _, exists := seenModules[name]; exists {
			errs = append(errs, fmt.Errorf("duplicate standard module: %s", name))
			continue
		}
		seenModules[name] = mod
	}

	router := events.NewRouter()
	for _, mod := range b.standards {
		name := mod.Name()
		for _, dep := range mod.Dependencies() {
			if _, ok := seenModules[dep]; !ok {
				errs = append(errs, fmt.Errorf("module %s requires dependency module %s which is not loaded", name, dep))
			}
		}
		if b.coreStore != nil {
			if err := mod.ValidateDependencies(b.coreStore); err != nil {
				errs = append(errs, fmt.Errorf("standard module %s dependency validation failed: %w", name, err))
			}
			if err := mod.Initialize(ctx, b.coreStore); err != nil {
				errs = append(errs, fmt.Errorf("standard module %s initialization failed: %w", name, err))
			}
		}
		if err := mod.RegisterRules(router); err != nil {
			errs = append(errs, fmt.Errorf("standard module %s rule registration failed: %w", name, err))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	inst := &engineInstance{
		coreStore:       b.coreStore,
		reportingStore:  b.reportingStore,
		governanceStore: b.governanceStore,
		standards:       b.standards,
		eventRouter:     router,
		eventService:    &defaultEventService{}, // initialized below
	}

	// Prefer real posting engine path when storage driver implements internal ports (P0-1).
	if postingRepo, ok := b.coreStore.(posting.BulkRepository); ok {
		if masterRepo, ok := b.coreStore.(masterdata.ContextStorageRepository); ok {
			masterSvc := masterdata.NewService(masterdata.NewRepositoryAdapter(masterRepo))
			inst.postingEngine = posting.NewEngine(postingRepo, masterSvc)
			inst.masterService = masterSvc
		}
	}

	inst.eventService = &defaultEventService{
		store:         b.coreStore,
		router:        router,
		postingEngine: inst.postingEngine,
		masterService: inst.masterService,
	}

	return inst, nil
}

type engineInstance struct {
	coreStore       storage.CoreLedgerStore
	reportingStore  storage.ReportingStore
	governanceStore storage.GovernanceStore
	standards       []standards.StandardModule
	eventRouter     *events.Router
	eventService    *defaultEventService
	postingEngine   *posting.Engine
	masterService   *masterdata.Service
}

func (e *engineInstance) Ledger() LedgerService {
	return &defaultLedgerService{
		store:         e.coreStore,
		postingEngine: e.postingEngine,
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

func (e *engineInstance) Close() error {
	var errs []error
	for _, mod := range e.standards {
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
	store         storage.CoreLedgerStore
	postingEngine *posting.Engine
}

func (s *defaultLedgerService) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	if s.postingEngine != nil {
		internalEntry, err := toInternalEntry(entry)
		if err != nil {
			return ledger.JournalEntry{}, err
		}
		// Application path: draft -> validate/prepare/post via posting engine (P0-1).
		internalEntry.Status = posting.EntryDraft
		if err := s.postingEngine.CreateDraftContext(ctx, internalEntry); err != nil {
			return ledger.JournalEntry{}, err
		}
		posterID := strings.TrimSpace(internalEntry.CreatedBy)
		if posterID == "" {
			posterID = "system"
		}
		if err := s.postingEngine.PostEntryContext(ctx, &internalEntry, posterID); err != nil {
			return fromInternalEntry(internalEntry), err
		}
		// Return canonical posted state.
		posted, err := s.postingEngine.GetEntry(ctx, internalEntry.CompanyID, internalEntry.ID)
		if err != nil {
			return fromInternalEntry(internalEntry), err
		}
		return fromInternalEntry(posted), nil
	}
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	err := s.store.PostEntry(ctx, entry)
	return entry, err
}

func (s *defaultLedgerService) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	if s.postingEngine != nil {
		e, err := s.postingEngine.GetEntry(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
		if err != nil {
			return ledger.JournalEntry{}, err
		}
		return fromInternalEntry(e), nil
	}
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	return s.store.GetEntry(ctx, companyID, entryID)
}

func (s *defaultLedgerService) ReverseEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, reason string) (ledger.JournalEntry, error) {
	if s.postingEngine != nil {
		// Use posting engine reversal path (P0-2): atomic, validates state/period, swaps lines,
		// marks original reversed, prevents double reversal.
		creator := strings.TrimSpace(reason)
		if creator == "" {
			creator = "system-reversal"
		}
		rev, err := s.postingEngine.ReverseDualContext(ctx, uuid.UUID(companyID), uuid.UUID(entryID), creator, creator, time.Now().UTC())
		if err != nil {
			return ledger.JournalEntry{}, err
		}
		// Persist reason in reference/description field on returned DTO.
		out := fromInternalEntry(rev)
		if reason != "" {
			out.Description = reason
			if out.ReferenceID == "" {
				out.ReferenceID = "reversal:" + uuid.UUID(entryID).String()
			}
		}
		return out, nil
	}
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	original, err := s.store.GetEntry(ctx, companyID, entryID)
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	reversal := original
	reversal.ID = types.NewJournalEntryID()
	reversal.Status = ledger.EntryPosted
	reversal.Classification = ledger.EntryClassReversing
	reversal.EffectiveDate = time.Now().UTC()
	reversal.ReversalOfID = &entryID

	revLines := make([]ledger.EntryLine, len(original.Lines))
	for i, l := range original.Lines {
		revLines[i] = l
		revLines[i].ID = types.NewJournalEntryLineID()
		revLines[i].FunctionalDebit = l.FunctionalCredit
		revLines[i].FunctionalCredit = l.FunctionalDebit
	}
	reversal.Lines = revLines

	err = s.store.PostEntry(ctx, reversal)
	return reversal, err
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
	store         storage.CoreLedgerStore
	router        *events.Router
	postingEngine *posting.Engine
	masterService *masterdata.Service
	mu            sync.Mutex
	keys          map[string]ledger.JournalEntry
	inflight      map[string]struct{}
}

func (s *defaultEventService) ProcessEvent(ctx context.Context, event events.AccountingEvent) (ledger.JournalEntry, error) {
	if s.store == nil && s.postingEngine == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	if strings.TrimSpace(event.EventType) == "" {
		return ledger.JournalEntry{}, errors.New("event type is required")
	}
	if event.JournalDraft == nil && (event.Amount.IsNegative() || event.Amount.IsZero()) {
		return ledger.JournalEntry{}, errors.New("event amount must be positive")
	}

	key := strings.TrimSpace(event.IdempotencyKey)
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
			return ledger.JournalEntry{}, errors.New("journal draft company mismatch")
		}
		if strings.TrimSpace(draft.SourceModule) == "" {
			draft.SourceModule = event.EventType
		}
		if strings.TrimSpace(draft.ReferenceID) == "" {
			draft.ReferenceID = event.EventID
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
			return ledger.JournalEntry{}, err
		}
	}
	normalizeLedgerEntryIDs(&entry)

	amountStr := ""
	amountCur := ""
	if event.Amount.Currency() != "" {
		amountStr = event.Amount.Amount().String()
		amountCur = event.Amount.Currency()
	}
	fp := publicEventFingerprint(event.EventType, event.CompanyID, event.LedgerID, func() *ledger.JournalEntry {
		if event.JournalDraft != nil {
			return &entry
		}
		return nil
	}(), amountCur, amountStr, ruleUsed.DebitAccountCode, ruleUsed.CreditAccountCode)

	// B.2: durable idempotency when key+store available.
	if key != "" && idemStore != nil && companyUUID != uuid.Nil {
		rec := PublicEventIngestion{
			CompanyID: companyUUID, IdempotencyKey: key, EventType: event.EventType,
			PayloadHash: fp, EntryID: uuid.UUID(entry.ID), Status: "processing",
		}
		existing, claimed, cErr := idemStore.ClaimPublicEventIngestion(ctx, rec)
		if cErr != nil {
			return ledger.JournalEntry{}, cErr
		}
		if !claimed {
			if existing.Status == "processed" && existing.EntryID != uuid.Nil {
				if existing.PayloadHash != "" && existing.PayloadHash != fp {
					return ledger.JournalEntry{}, ErrIdempotencyKeyConflict
				}
				if uuid.UUID(existing.Entry.ID) != uuid.Nil || len(existing.Entry.Lines) > 0 {
					return existing.Entry, nil
				}
				// Fetch posted entry by id if snapshot missing.
				if posted, gErr := s.fetchPostedEntry(ctx, event.CompanyID, types.JournalEntryID(existing.EntryID)); gErr == nil {
					return posted, nil
				}
				return ledger.JournalEntry{}, ErrIdempotencyInProgress
			}
			if existing.PayloadHash != "" && existing.PayloadHash != fp {
				return ledger.JournalEntry{}, ErrIdempotencyKeyConflict
			}
			if existing.Status == "processing" {
				return ledger.JournalEntry{}, ErrIdempotencyInProgress
			}
			return ledger.JournalEntry{}, errors.New("idempotency key is in an invalid state")
		}
		posted, pErr := s.postEventEntry(ctx, entry)
		if pErr != nil {
			_ = idemStore.FailPublicEventIngestion(ctx, companyUUID, key, pErr.Error())
			return ledger.JournalEntry{}, pErr
		}
		if err := idemStore.CompletePublicEventIngestion(ctx, companyUUID, key, posted); err != nil {
			// Posting succeeded and key is durably claimed; return posted result.
			return posted, nil
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

	posted, err := s.postEventEntry(ctx, entry)
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

func (s *defaultEventService) postEventEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	if s.postingEngine != nil {
		ledgerSvc := &defaultLedgerService{store: s.store, postingEngine: s.postingEngine}
		return ledgerSvc.PostEntry(ctx, entry)
	}
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	return entry, s.store.PostEntry(ctx, entry)
}

func (s *defaultEventService) fetchPostedEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	ledgerSvc := &defaultLedgerService{store: s.store, postingEngine: s.postingEngine}
	return ledgerSvc.GetEntry(ctx, companyID, entryID)
}

func (s *defaultEventService) constructEventEntry(ctx context.Context, rule events.PostingRule, event events.AccountingEvent) (ledger.JournalEntry, error) {
	amt := event.Amount
	if amt.Currency() == "" {
		return ledger.JournalEntry{}, errors.New("event amount currency is required")
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
	desc := rule.DescriptionTemplate
	if strings.TrimSpace(desc) == "" {
		desc = fmt.Sprintf("Event: %s (%s)", event.EventType, event.EventID)
	}
	effective := event.EffectiveDate
	if effective.IsZero() {
		effective = time.Now().UTC()
	}
	return ledger.JournalEntry{
		ID:             types.NewJournalEntryID(),
		CompanyID:      event.CompanyID,
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

func (s *defaultEventService) resolveRuleAccount(ctx context.Context, companyID types.CompanyID, code string) (types.AccountID, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return types.AccountID{}, errors.New("account code is required")
	}
	if s.masterService != nil {
		acc, err := s.masterService.GetAccountByCodeForCompanyContext(ctx, uuid.UUID(companyID), code)
		if err != nil {
			return types.AccountID{}, err
		}
		return types.AccountID(acc.ID), nil
	}
	// Fallback: try core store lookup by code if it exposes internal-style repo? no direct API.
	// Without masterdata we cannot safely resolve account codes; fail explicitly (P0-3).
	return types.AccountID{}, errors.New("account code resolution requires master data service")
}
