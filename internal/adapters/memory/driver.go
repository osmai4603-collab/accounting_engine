package memory

import (
	"sync"

	"github.com/osmai4603-collab/accounting_engine/internal/governance"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

type Driver struct {
	mu                         sync.RWMutex
	readOnly                   bool
	accounts                   map[uuid.UUID]masterdata.Account
	versions                   map[uuid.UUID][]masterdata.AccountVersion
	centers                    map[uuid.UUID]masterdata.CostCenter
	dimensions                 map[string]masterdata.AnalyticalDimensionValue
	companies                  map[uuid.UUID]masterdata.Company
	journals                   map[uuid.UUID]journalRecord
	years                      map[uuid.UUID]masterdata.FiscalYear
	periods                    map[uuid.UUID]masterdata.FiscalPeriod
	journalSeq                 map[string]int64
	entries                    map[uuid.UUID]posting.JournalEntry
	balances                   map[accountBalanceKey]accountBalance
	adjustingPeriods           map[companyPeriodKey]bool
	events                     map[string]subledgers.EconomicEvent
	eventRules                 map[string][]subledgers.EventAccountingRule
	lines                      map[uuid.UUID]subledgers.OpenLine
	assets                     map[uuid.UUID]subledgers.FixedAsset
	bank                       map[string][]subledgers.BankItem
	reconciliationReports      []subledgers.DiscrepancyReport
	reconciliationCertificates []subledgers.ReconciliationCertificate
	contracts                  map[uuid.UUID]map[uuid.UUID]ifrs15.Contract
	policies                   map[string][]policy.VersionedPolicy
	audit                      []*governance.FinancialAuditLog
	auditRecords               []storage.AuditRecord
	idempotency                map[memoryIdempotencyKey]storage.IdempotencyRecord
	rates                      *valuation.RateRegistry
}

type journalRecord struct {
	ID        uuid.UUID
	CompanyID uuid.UUID
	Code      string
	Name      string
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
		journals:         make(map[uuid.UUID]journalRecord),
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
		policies:         make(map[string][]policy.VersionedPolicy),
		idempotency:      make(map[memoryIdempotencyKey]storage.IdempotencyRecord),
		rates:            valuation.NewRateRegistry(),
	}
}
