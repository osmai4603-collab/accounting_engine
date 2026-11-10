package memory

import (
	"sync"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/ifrs15"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	"accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Driver struct {
	mu               sync.RWMutex
	accounts         map[uuid.UUID]masterdata.Account
	versions         map[uuid.UUID][]masterdata.AccountVersion
	centers          map[uuid.UUID]masterdata.CostCenter
	dimensions       map[string]masterdata.AnalyticalDimensionValue
	companies        map[uuid.UUID]masterdata.Company
	years            map[uuid.UUID]masterdata.FiscalYear
	periods          map[uuid.UUID]masterdata.FiscalPeriod
	journalSeq       map[string]int64
	entries          map[uuid.UUID]posting.JournalEntry
	balances         map[accountBalanceKey]accountBalance
	adjustingPeriods map[companyPeriodKey]bool
	events           map[string]subledgers.EconomicEvent
	eventRules       map[string][]subledgers.EventAccountingRule
	lines            map[uuid.UUID]subledgers.OpenLine
	assets           map[uuid.UUID]subledgers.FixedAsset
	bank             map[string][]subledgers.BankItem
	contracts        map[uuid.UUID]map[uuid.UUID]ifrs15.Contract
	audit            []*governance.FinancialAuditLog
	rates            *valuation.RateRegistry
}

type accountBalanceKey struct {
	companyID uuid.UUID
	accountID uuid.UUID
	periodID  uuid.UUID
	currency  string
}

type companyPeriodKey struct {
	companyID uuid.UUID
	periodID  uuid.UUID
}

type accountBalance struct {
	beginningDebit  decimal.Decimal
	beginningCredit decimal.Decimal
	periodDebit     decimal.Decimal
	periodCredit    decimal.Decimal
}

var (
	_ subledgers.Repository               = (*Driver)(nil)
	_ masterdata.ContextStorageRepository = (*Driver)(nil)
)

func New() *Driver {
	return &Driver{
		accounts:         make(map[uuid.UUID]masterdata.Account),
		versions:         make(map[uuid.UUID][]masterdata.AccountVersion),
		centers:          make(map[uuid.UUID]masterdata.CostCenter),
		dimensions:       make(map[string]masterdata.AnalyticalDimensionValue),
		companies:        make(map[uuid.UUID]masterdata.Company),
		years:            make(map[uuid.UUID]masterdata.FiscalYear),
		periods:          make(map[uuid.UUID]masterdata.FiscalPeriod),
		journalSeq:       make(map[string]int64),
		entries:          make(map[uuid.UUID]posting.JournalEntry),
		balances:         make(map[accountBalanceKey]accountBalance),
		adjustingPeriods: make(map[companyPeriodKey]bool),
		events:           make(map[string]subledgers.EconomicEvent),
		eventRules:       make(map[string][]subledgers.EventAccountingRule),
		lines:            make(map[uuid.UUID]subledgers.OpenLine),
		assets:           make(map[uuid.UUID]subledgers.FixedAsset),
		bank:             make(map[string][]subledgers.BankItem),
		contracts:        make(map[uuid.UUID]map[uuid.UUID]ifrs15.Contract),
		rates:            valuation.NewRateRegistry(),
	}
}
