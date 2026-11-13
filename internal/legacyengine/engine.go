package accounting_engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/consolidation"
	"github.com/osmai4603-collab/accounting_engine/internal/governance"
	"github.com/osmai4603-collab/accounting_engine/internal/ias12"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs16"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrsautomation"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/observability"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
)

var ErrUnsupportedStorageDriver = errors.New("unsupported storage driver")
var ErrStoragePortRequired = errors.New("required storage port is missing")
var ErrStandardsJobsRequired = errors.New("at least one standards close job is required")

type Config struct {
	// StrictGovernance denies posting until the company has configured an approval policy.
	StrictGovernance bool
	Standards        StandardsConfig
	Policies         PolicyConfig
	// Observability optionally supplies the structured logger and metrics
	// registry used by the engine. A default metrics registry is always
	// created so /metrics is never empty.
	Observability *ObservabilityConfig
}

// ObservabilityConfig carries the deployment's logging and metrics handles.
type ObservabilityConfig struct {
	Logger  *slog.Logger
	Metrics *observability.Metrics
}

// PolicyConfig enables policy enforcement using the optional policy storage
// port and supports deployment-specific approval adapters.
type PolicyConfig struct {
	Enabled                       bool
	AccountingManagerAuthorizer   policy.AccountingManagerAuthorizer
	BackdatingExceptionAuthorizer policy.BackdatingExceptionAuthorizer
}

// StandardsConfig enables standard units by injecting only the units licensed
// and configured for the current deployment.
type StandardsConfig struct {
	IFRS9         *ifrs9.Unit
	IFRS15        *ifrs15.Unit
	IFRS16        *ifrs16.Unit
	IAS12         *ias12.Unit
	Consolidation *consolidation.Unit
}

func DefaultConfig() Config {
	return Config{}
}

type PostingStoragePort interface {
	posting.BulkRepository
	posting.FinancialDimensionPolicy
}

type ReportingStoragePort interface {
	reporting.Repository
}

type MasterDataStoragePort interface {
	masterdata.ContextStorageRepository
	masterdata.FinancialDimensionRepository
}

type PolicyStoragePort interface {
	policy.Repository
}

type SubledgerStoragePort interface {
	subledgers.Repository
}

type GovernanceStoragePort interface {
	governance.Repository
}

type StorageDriver interface {
	PostingStoragePort
	ReportingStoragePort
	MasterDataStoragePort
	SubledgerStoragePort
	GovernanceStoragePort
	valuation.RateProviderSPI
}

type StoragePorts struct {
	Posting            PostingStoragePort
	Reporting          ReportingStoragePort
	MasterData         MasterDataStoragePort
	Subledger          SubledgerStoragePort
	Governance         GovernanceStoragePort
	Valuation          valuation.RateProviderSPI
	MakerCheckerPolicy posting.MakerCheckerPolicy
	Policy             PolicyStoragePort
}

type postingEntryReader struct {
	engine *posting.Engine
}

func (r postingEntryReader) GetEntryByIDForCompany(
	ctx context.Context,
	companyID, entryID uuid.UUID,
) (posting.JournalEntry, error) {
	return r.engine.GetEntry(ctx, companyID, entryID)
}

func New(driver StorageDriver, config Config) (*AccountingEngine, error) {
	if driver == nil {
		return nil, ErrUnsupportedStorageDriver
	}
	return NewWithPorts(StoragePorts{
		Posting:            driver,
		Reporting:          driver,
		MasterData:         driver,
		Subledger:          driver,
		Governance:         driver,
		Valuation:          driver,
		MakerCheckerPolicy: optionalMakerCheckerPolicy(driver),
		Policy:             optionalPolicyStorage(driver, config.Policies.Enabled),
	}, config)
}

func optionalPolicyStorage(driver StorageDriver, enabled bool) PolicyStoragePort {
	if !enabled {
		return nil
	}
	repository, _ := driver.(PolicyStoragePort)
	return repository
}

func optionalMakerCheckerPolicy(driver StorageDriver) posting.MakerCheckerPolicy {
	policy, _ := driver.(posting.MakerCheckerPolicy)
	return policy
}

type AccountingEngine struct {
	MasterData    *masterdata.Service
	Posting       *posting.Engine
	Subledgers    *subledgers.Service
	Valuation     *valuation.Engine
	Reporting     *reporting.Engine
	Governance    *governance.Engine
	IFRS9         *ifrs9.Unit
	IFRS15        *ifrs15.Unit
	IFRS16        *ifrs16.Unit
	IAS12         *ias12.Unit
	Consolidation *consolidation.Unit
	Policies      *policy.Unit
	Dimensions    masterdata.FinancialDimensionRepository
	Config        Config
	Telemetry     *observability.Unit
}

func (e *AccountingEngine) GeneralLedger(
	ctx context.Context,
	companyID, accountID uuid.UUID,
	start, end time.Time,
	currency string,
) (reporting.GeneralLedgerAccountReport, error) {
	resolved, err := e.Reporting.ResolvePresentationCurrency(ctx, companyID, end, currency)
	if err != nil {
		return reporting.GeneralLedgerAccountReport{}, err
	}
	return e.Reporting.GeneralLedger.GenerateForCompanyContext(
		ctx, companyID, accountID, start, end, resolved,
	)
}

func (e *AccountingEngine) FinancialRatios(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) (reporting.FinancialRatios, error) {
	resolved, err := e.Reporting.ResolvePresentationCurrency(ctx, companyID, end, currency)
	if err != nil {
		return reporting.FinancialRatios{}, err
	}
	return e.Reporting.Ratios.GenerateForCompanyContext(ctx, companyID, start, end, resolved)
}

func (e *AccountingEngine) StatementBundle(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) (reporting.StatementBundle, error) {
	return e.Reporting.GenerateBundleForCompanyContext(ctx, companyID, start, end, currency)
}

// PostStandardsEntryContext sends a journal draft produced by a standards unit
// through the same central draft and posting validations as other entries.
func (e *AccountingEngine) PostStandardsEntryContext(
	ctx context.Context,
	companyID uuid.UUID,
	entry posting.JournalEntry,
	posterID string,
) (posting.JournalEntry, error) {
	if e == nil || e.Posting == nil || companyID == uuid.Nil || entry.JournalID == uuid.Nil ||
		entry.ID == uuid.Nil || len(entry.Lines) < 2 {
		return posting.JournalEntry{}, posting.ErrInvalidEntry
	}
	if entry.CompanyID != uuid.Nil && entry.CompanyID != companyID {
		return posting.JournalEntry{}, masterdata.ErrCompanyScopeRequired
	}
	entry.CompanyID = companyID
	for i := range entry.Lines {
		if entry.Lines[i].CompanyID != uuid.Nil && entry.Lines[i].CompanyID != companyID {
			return posting.JournalEntry{}, masterdata.ErrCompanyScopeRequired
		}
		entry.Lines[i].CompanyID = companyID
	}
	if err := e.Posting.CreateDraftContext(ctx, entry); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := e.Posting.PostContext(ctx, companyID, entry.ID, posterID); err != nil {
		return entry, err
	}
	entry, err := e.Posting.GetEntry(ctx, companyID, entry.ID)
	if err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

// RunMonthlyStandardsClose executes the configured monthly standards jobs
// through the central posting engine, including stable-reference idempotency.
func (e *AccountingEngine) RunMonthlyStandardsClose(
	ctx context.Context,
	period ifrsautomation.Period,
	posterID string,
	jobs ...ifrsautomation.Job,
) (ifrsautomation.RunResult, error) {
	if e == nil || e.Posting == nil || len(jobs) == 0 {
		return ifrsautomation.RunResult{}, ErrStandardsJobsRequired
	}
	runner, err := ifrsautomation.NewRunner(e.Posting, postingEntryReader{engine: e.Posting}, posterID)
	if err != nil {
		return ifrsautomation.RunResult{}, err
	}
	return runner.RunMonthly(ctx, period, jobs...)
}

// ReconcileBalances runs the continuous balance reconciliation engine for a
// company, fiscal period, and currency. The returned report carries the
// per-account drift (in exact minor units) between the materialized
// account_balances rows and the posted account_move_lines; the returned error
// is non-nil whenever any drift is detected (zero-drift requirement).
func (e *AccountingEngine) ReconcileBalances(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) (*reporting.ReconciliationReport, error) {
	report, err := e.Reporting.Reconciliation.Reconcile(ctx, companyID, fiscalPeriodID, currencyID)
	if err != nil {
		return nil, err
	}
	return &report, nil
}

func (e *AccountingEngine) ForCompany(companyID uuid.UUID) *subledgers.CompanySubledgerService {
	if e == nil || e.Subledgers == nil {
		return nil
	}
	return e.Subledgers.ForCompany(companyID)
}

func (e *AccountingEngine) DimensionProfitAndLossForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) (reporting.DimensionProfitAndLoss, error) {
	resolved, err := e.Reporting.ResolvePresentationCurrency(ctx, companyID, end, currency)
	if err != nil {
		return reporting.DimensionProfitAndLoss{}, err
	}
	return e.Reporting.GenerateDimensionProfitAndLossForCompanyContext(
		ctx, companyID, start, end, resolved,
	)
}

func (e *AccountingEngine) StartDailyAuditExports(
	ctx context.Context,
	exporter *governance.AuditExporter,
	companyIDs []uuid.UUID,
) (<-chan error, error) {
	if exporter == nil || len(companyIDs) == 0 {
		return nil, errors.New("daily audit export requires an exporter and at least one company")
	}
	seen := make(map[uuid.UUID]struct{}, len(companyIDs))
	for _, companyID := range companyIDs {
		if companyID == uuid.Nil {
			return nil, masterdata.ErrCompanyScopeRequired
		}
		if _, exists := seen[companyID]; exists {
			return nil, errors.New("daily audit export company ids must be unique")
		}
		seen[companyID] = struct{}{}
	}

	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		results := make(chan error, len(companyIDs))
		for _, companyID := range companyIDs {
			go func(companyID uuid.UUID) {
				results <- exporter.RunDaily(runCtx, companyID)
			}(companyID)
		}
		var firstErr error
		for range companyIDs {
			if err := <-results; err != nil && !errors.Is(err, context.Canceled) && firstErr == nil {
				firstErr = err
				cancel()
			}
		}
		if firstErr != nil {
			errCh <- firstErr
		} else if err := ctx.Err(); err != nil {
			errCh <- err
		}
	}()
	return errCh, nil
}

func NewEngine(driver StorageDriver, config Config) (*AccountingEngine, error) {
	if driver == nil {
		return nil, ErrUnsupportedStorageDriver
	}
	return New(driver, config)
}

func NewWithPorts(ports StoragePorts, config Config) (*AccountingEngine, error) {
	for _, port := range []struct {
		name      string
		isMissing bool
	}{
		{name: "posting", isMissing: ports.Posting == nil},
		{name: "reporting", isMissing: ports.Reporting == nil},
		{name: "master data", isMissing: ports.MasterData == nil},
		{name: "subledger", isMissing: ports.Subledger == nil},
		{name: "governance", isMissing: ports.Governance == nil},
		{name: "valuation", isMissing: ports.Valuation == nil},
	} {
		if port.isMissing {
			return nil, fmt.Errorf("%w: %s", ErrStoragePortRequired, port.name)
		}
	}

	masterService := masterdata.NewService(masterdata.NewRepositoryAdapter(ports.MasterData))
	telemetry := observability.NewUnit(observability.Config{})
	if config.Observability != nil {
		telemetry = observability.NewUnit(observability.Config{
			Logger:  config.Observability.Logger,
			Metrics: config.Observability.Metrics,
		})
	}
	postingEngine := posting.NewEngine(ports.Posting, masterService)
	postingEngine.SetObservability(telemetry.Logger, telemetry.Metrics)
	postingEngine.SetFinancialDimensionsEngine(posting.NewFinancialDimensionsEngine(ports.Posting))
	reportingEngine := reporting.NewEngine(ports.Reporting)
	reportingEngine.Reconciliation.SetMetrics(telemetry.Metrics)
	governanceEngine := governance.NewEngine(ports.Governance)
	governanceEngine.SetMetrics(telemetry.Metrics)
	approvalGate := governanceEngine.ApprovalGate()
	if approvalGate == nil && config.StrictGovernance {
		approvalGate = &governance.ApprovalGate{}
	}
	if gate, ok := approvalGate.(*governance.ApprovalGate); ok {
		gate.StrictGovernance = config.StrictGovernance
	}
	postingEngine.SetApprovalGate(approvalGate)
	if ports.MakerCheckerPolicy != nil {
		postingEngine.SetMakerCheckerPolicy(ports.MakerCheckerPolicy)
	}
	var policyUnit *policy.Unit
	if ports.Policy != nil || config.Policies.Enabled {
		if ports.Policy == nil {
			return nil, fmt.Errorf("%w: policy", policy.ErrPolicyStorageUnavailable)
		}
		policyUnit = policy.NewUnit(ports.Policy)
		managerAuthorizer := config.Policies.AccountingManagerAuthorizer
		if managerAuthorizer == nil {
			if roleRepository, ok := ports.Governance.(policy.AccountingManagerRoleRepository); ok {
				managerAuthorizer = policy.NewGovernanceManagerAuthorizer(roleRepository)
			}
		}
		policyUnit.SetAccountingManagerAuthorizer(managerAuthorizer)
		backdatingAuthorizer := config.Policies.BackdatingExceptionAuthorizer
		if backdatingAuthorizer == nil && governanceEngine.Approvals != nil {
			backdatingAuthorizer = policy.NewApprovalBackdatingExceptionAuthorizer(governanceEngine.Approvals)
		}
		policyUnit.SetBackdatingExceptionAuthorizer(backdatingAuthorizer)
		postingEngine.SetPolicyUnit(policyUnit)
		reportingEngine.SetPolicyUnit(policyUnit)
	}
	subledgerService := subledgers.NewService(ports.Subledger, postingEngine)
	return &AccountingEngine{
		MasterData:    masterService,
		Posting:       postingEngine,
		Subledgers:    subledgerService,
		Valuation:     valuation.NewEngine(ports.Valuation),
		Reporting:     reportingEngine,
		Governance:    governanceEngine,
		IFRS9:         config.Standards.IFRS9,
		IFRS15:        config.Standards.IFRS15,
		IFRS16:        config.Standards.IFRS16,
		IAS12:         config.Standards.IAS12,
		Consolidation: config.Standards.Consolidation,
		Policies:      policyUnit,
		Dimensions:    ports.MasterData,
		Config:        config,
		Telemetry:     telemetry,
	}, nil
}

func (e *AccountingEngine) SaveCostCenter(ctx context.Context, center masterdata.CostCenter) error {
	return e.Dimensions.SaveCostCenter(ctx, center)
}

func (e *AccountingEngine) SaveAnalyticalDimension(ctx context.Context, value masterdata.AnalyticalDimensionValue) error {
	return e.Dimensions.SaveAnalyticalDimension(ctx, value)
}
