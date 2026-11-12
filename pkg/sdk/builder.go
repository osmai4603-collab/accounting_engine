package sdk

import (
	"context"
	"errors"
	"fmt"
	"time"

	internalEvents "accounting_engine/internal/events"
	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/reporting"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/spi/standards"
	"accounting_engine/pkg/spi/storage"
)

type EngineBuilder struct {
	coreStore       storage.CoreLedgerStore
	reportingStore  storage.ReportingStore
	governanceStore storage.GovernanceStore
	standards       []standards.StandardModule
}

type Option func(*EngineBuilder) error

func WithCoreStorage(store storage.CoreLedgerStore) Option {
	return func(b *EngineBuilder) error {
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
		b.standards = append(b.standards, module)
		return nil
	}
}

func NewBuilder(opts ...Option) *EngineBuilder {
	b := &EngineBuilder{}
	for _, opt := range opts {
		_ = opt(b)
	}
	return b
}

func (b *EngineBuilder) Build(ctx context.Context) (Engine, error) {
	var errs []error
	if b.coreStore == nil {
		errs = append(errs, errors.New("core ledger store is required but missing"))
	}
	router := internalEvents.NewRouter()
	for _, mod := range b.standards {
		if b.coreStore != nil {
			if err := mod.ValidateDependencies(b.coreStore); err != nil {
				errs = append(errs, fmt.Errorf("standard module %s dependency validation failed: %w", mod.Name(), err))
			}
		}
		_ = mod.RegisterRules(router)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &engineInstance{
		coreStore:       b.coreStore,
		reportingStore:  b.reportingStore,
		governanceStore: b.governanceStore,
		standards:       b.standards,
		eventRouter:     router,
	}, nil
}

type engineInstance struct {
	coreStore       storage.CoreLedgerStore
	reportingStore  storage.ReportingStore
	governanceStore storage.GovernanceStore
	standards       []standards.StandardModule
	eventRouter     *internalEvents.Router
}

func (e *engineInstance) Ledger() LedgerService {
	return &defaultLedgerService{store: e.coreStore}
}

func (e *engineInstance) Reporting() ReportingService {
	return &defaultReportingService{store: e.reportingStore}
}

func (e *engineInstance) Governance() GovernanceService {
	return &defaultGovernanceService{store: e.governanceStore}
}

func (e *engineInstance) Events() EventIngestionService {
	return &defaultEventService{store: e.coreStore, router: e.eventRouter}
}

func (e *engineInstance) Close() error {
	return nil
}

type defaultLedgerService struct {
	store storage.CoreLedgerStore
}

func (s *defaultLedgerService) PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error) {
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	err := s.store.PostEntry(ctx, entry)
	return entry, err
}

func (s *defaultLedgerService) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	return s.store.GetEntry(ctx, companyID, entryID)
}

func (s *defaultLedgerService) ReverseEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, reason string) (ledger.JournalEntry, error) {
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
	store  storage.CoreLedgerStore
	router *internalEvents.Router
}

func (s *defaultEventService) ProcessEvent(ctx context.Context, event events.AccountingEvent) (ledger.JournalEntry, error) {
	if s.store == nil {
		return ledger.JournalEntry{}, errors.New("core ledger store not configured")
	}
	svc := internalEvents.NewEventService(s.store, s.router)
	return svc.ProcessEvent(ctx, event)
}
