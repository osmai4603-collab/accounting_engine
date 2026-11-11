package accounting_engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/reporting"
	"accounting_engine/internal/subledgers"
	"accounting_engine/internal/valuation"
	"github.com/google/uuid"
)

var ErrUnsupportedStorageDriver = errors.New("unsupported storage driver")
var ErrStoragePortRequired = errors.New("required storage port is missing")

type Config struct{}

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
	}, config)
}

func optionalMakerCheckerPolicy(driver StorageDriver) posting.MakerCheckerPolicy {
	policy, _ := driver.(posting.MakerCheckerPolicy)
	return policy
}

type AccountingEngine struct {
	MasterData *masterdata.Service
	Posting    *posting.Engine
	Subledgers *subledgers.Service
	Valuation  *valuation.Engine
	Reporting  *reporting.Engine
	Governance *governance.Engine
	Dimensions masterdata.FinancialDimensionRepository
	Config     Config
}

func (e *AccountingEngine) GeneralLedger(
	ctx context.Context,
	companyID, accountID uuid.UUID,
	start, end time.Time,
	currency string,
) (reporting.GeneralLedgerAccountReport, error) {
	return e.Reporting.GeneralLedger.GenerateForCompanyContext(
		ctx, companyID, accountID, start, end, currency,
	)
}

func (e *AccountingEngine) FinancialRatios(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) (reporting.FinancialRatios, error) {
	return e.Reporting.Ratios.GenerateForCompanyContext(ctx, companyID, start, end, currency)
}

func (e *AccountingEngine) StatementBundle(
	ctx context.Context,
	companyID uuid.UUID,
	start, end time.Time,
	currency string,
) (reporting.StatementBundle, error) {
	return e.Reporting.Bundle.GenerateForCompanyContext(ctx, companyID, start, end, currency)
}

func (e *AccountingEngine) ReconcileBalances(
	ctx context.Context,
	companyID, fiscalPeriodID uuid.UUID,
	currencyID string,
) error {
	return e.Reporting.Reconciliation.Reconcile(ctx, companyID, fiscalPeriodID, currencyID)
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
	return e.Reporting.GenerateDimensionProfitAndLossForCompanyContext(
		ctx, companyID, start, end, currency,
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
	postingEngine := posting.NewEngine(ports.Posting, masterService)
	postingEngine.SetFinancialDimensionsEngine(posting.NewFinancialDimensionsEngine(ports.Posting))
	governanceEngine := governance.NewEngine(ports.Governance)
	postingEngine.SetApprovalGate(governanceEngine.ApprovalGate())
	if ports.MakerCheckerPolicy != nil {
		postingEngine.SetMakerCheckerPolicy(ports.MakerCheckerPolicy)
	}
	subledgerService := subledgers.NewService(ports.Subledger, postingEngine)
	return &AccountingEngine{
		MasterData: masterService,
		Posting:    postingEngine,
		Subledgers: subledgerService,
		Valuation:  valuation.NewEngine(ports.Valuation),
		Reporting:  reporting.NewEngine(ports.Reporting),
		Governance: governanceEngine,
		Dimensions: ports.MasterData,
		Config:     config,
	}, nil
}

func (e *AccountingEngine) SaveCostCenter(ctx context.Context, center masterdata.CostCenter) error {
	return e.Dimensions.SaveCostCenter(ctx, center)
}

func (e *AccountingEngine) SaveAnalyticalDimension(ctx context.Context, value masterdata.AnalyticalDimensionValue) error {
	return e.Dimensions.SaveAnalyticalDimension(ctx, value)
}
