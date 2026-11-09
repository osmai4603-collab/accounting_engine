package accounting_engine

import (
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
	masterdata.Repository
	posting.Repository
	posting.BulkRepository
	subledgers.Repository
	valuation.RateProviderSPI
	reporting.Repository
	governance.Repository
	reporting.AccountRepository
	reporting.LedgerRepository
	reporting.EntryDateRepository
	reporting.FiscalYearRepository
}

type AccountingEngine struct {
	MasterData *masterdata.Service
	Posting    *posting.Engine
	Subledgers *subledgers.Service
	Valuation  *valuation.Engine
	Reporting  *reporting.Engine
	Governance *governance.Engine
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

func NewEngine(driver StorageDriver, config Config) (*AccountingEngine, error) {
	if driver == nil {
		return nil, ErrUnsupportedStorageDriver
	}
	if config.BaseCurrency == "" {
		config.BaseCurrency = DefaultConfig().BaseCurrency
	}

	masterService := masterdata.NewService(driver)
	postingEngine := posting.NewEngine(driver, masterService)
	subledgerService := subledgers.NewService(driver, postingEngine)
	return &AccountingEngine{
		MasterData: masterService,
		Posting:    postingEngine,
		Subledgers: subledgerService,
		Valuation:  valuation.NewEngine(driver),
		Reporting:  reporting.NewEngine(driver),
		Governance: governance.NewEngine(driver),
		Config:     config,
	}, nil
}
