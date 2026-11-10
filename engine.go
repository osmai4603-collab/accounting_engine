package accounting_engine

import (
	"context"
	"errors"
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

type Config struct {
	BaseCurrency string
}

func DefaultConfig() Config {
	return Config{BaseCurrency: "USD"}
}

func New(first, second any) (*AccountingEngine, error) {
	switch value := first.(type) {
	case Config:
		driver, ok := second.(StorageDriver)
		if !ok {
			return nil, ErrUnsupportedStorageDriver
		}
		return NewEngine(driver, value)
	case StorageDriver:
		config, ok := second.(Config)
		if !ok {
			return nil, ErrUnsupportedStorageDriver
		}
		return NewEngine(value, config)
	default:
		return nil, ErrUnsupportedStorageDriver
	}
}

type StorageDriver interface {
	masterdata.ContextStorageRepository
	masterdata.FinancialDimensionRepository
	posting.Repository
	posting.BulkRepository
	posting.FinancialDimensionPolicy
	subledgers.Repository
	valuation.RateProviderSPI
	reporting.Repository
	governance.Repository
	reporting.AccountRepository
	reporting.LedgerRepository
	reporting.FiscalYearRepository
	reporting.CompanyScopedClosingRepository
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

func (e *AccountingEngine) GeneralLedger(accountID uuid.UUID, start, end time.Time, currency string) (reporting.GeneralLedgerAccountReport, error) {
	return e.Reporting.GeneralLedger.Generate(accountID, start, end, currency)
}

func (e *AccountingEngine) FinancialRatios(start, end time.Time, currency string) (reporting.FinancialRatios, error) {
	return e.Reporting.Ratios.Generate(start, end, currency)
}

func (e *AccountingEngine) StatementBundle(start, end time.Time, currency string) (reporting.StatementBundle, error) {
	return e.Reporting.Bundle.Generate(start, end, currency)
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
	if config.BaseCurrency == "" {
		config.BaseCurrency = DefaultConfig().BaseCurrency
	}

	masterService := masterdata.NewService(masterdata.NewRepositoryAdapter(driver))
	postingEngine := posting.NewEngine(driver, masterService)
	postingEngine.SetFinancialDimensionsEngine(posting.NewFinancialDimensionsEngine(driver))
	governanceEngine := governance.NewEngine(driver)
	postingEngine.SetApprovalGate(governanceEngine.ApprovalGate())
	if policy, ok := driver.(posting.MakerCheckerPolicy); ok {
		postingEngine.SetMakerCheckerPolicy(policy)
	}
	subledgerService := subledgers.NewService(driver, postingEngine)
	return &AccountingEngine{
		MasterData: masterService,
		Posting:    postingEngine,
		Subledgers: subledgerService,
		Valuation:  valuation.NewEngine(driver),
		Reporting:  reporting.NewEngine(driver),
		Governance: governanceEngine,
		Dimensions: driver,
		Config:     config,
	}, nil
}

func (e *AccountingEngine) SaveCostCenter(ctx context.Context, center masterdata.CostCenter) error {
	return e.Dimensions.SaveCostCenter(ctx, center)
}

func (e *AccountingEngine) SaveAnalyticalDimension(ctx context.Context, value masterdata.AnalyticalDimensionValue) error {
	return e.Dimensions.SaveAnalyticalDimension(ctx, value)
}
