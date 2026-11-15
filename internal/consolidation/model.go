// Package consolidation builds a group trial balance and basic consolidated
// financial totals from typed entity trial balances and explicit policies.
// It implements only the consolidation adjustments represented by its inputs;
// it is not a general IFRS-compliance engine.
package consolidation

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrInvalidInput               = errors.New("invalid consolidation input")
	ErrMissingAccountMapping      = errors.New("missing consolidation account mapping")
	ErrMissingExchangeRate        = errors.New("missing consolidation exchange rate")
	ErrMissingConsolidationPolicy = errors.New("missing consolidation policy or control evidence")
	ErrUnbalancedTrialBalance     = errors.New("entity trial balance is not balanced")
	ErrUnbalancedJournal          = errors.New("consolidation journal entry is not balanced")
	ErrUnbalancedConsolidation    = errors.New("consolidated financial statements are not balanced")
	ErrInsufficientIntercompany   = errors.New("intercompany balance has an unexpected sign")
	ErrInsufficientInventory      = errors.New("unrealized profit exceeds inventory balance")
)

// AccountType identifies the statement classification needed by this engine.
type AccountType string

const (
	AccountAsset     AccountType = "asset"
	AccountLiability AccountType = "liability"
	AccountEquity    AccountType = "equity"
	AccountRevenue   AccountType = "revenue"
	AccountExpense   AccountType = "expense"
)

// RateClass selects the exchange rate convention used for a mapped account.
type RateClass string

const (
	RateClosing    RateClass = "closing"
	RateAverage    RateClass = "average"
	RateHistorical RateClass = "historical"
)

// ReportingPeriod is inclusive. Trial balances must use the same calendar dates.
type ReportingPeriod struct {
	StartDate time.Time
	EndDate   time.Time
}

type Entity struct {
	ID                 string
	FunctionalCurrency string
}

type GroupAccount struct {
	ID   string
	Name string
	Type AccountType
}

// AccountMapping connects one entity's source account to a group account.
// HistoricalDate is required for historical-rate mappings.
type AccountMapping struct {
	EntityID        string
	SourceAccountID string
	GroupAccountID  string
	RateClass       RateClass
	HistoricalDate  time.Time
}

// GoodwillMethod selects the IFRS 3 measurement basis for non-controlling
// interests. An empty value is treated as partial goodwill for compatibility.
type GoodwillMethod string

const (
	PartialGoodwill GoodwillMethod = "partial"
	FullGoodwill    GoodwillMethod = "full"
)

// Ownership describes one direct ownership link in the group's ownership
// chain. Acquisition values are supplied in group currency.
type Ownership struct {
	ParentEntityID                  string
	SubsidiaryEntityID              string
	Percentage                      decimal.Decimal
	ControlBasis                    string
	ControlEvidenceReference        string
	GoodwillMethod                  GoodwillMethod
	AcquisitionDate                 time.Time
	Consideration                   decimal.Decimal
	FairValueNetAssets              decimal.Decimal
	NCIAtFairValue                  decimal.Decimal
	InvestmentAccountID             string
	GoodwillAccountID               string
	NCIAccountID                    string
	ParentRetainedEarningsAccountID string
}

type IntercompanyBalanceRule struct {
	ID                  string
	ReceivableEntityID  string
	ReceivableAccountID string
	PayableEntityID     string
	PayableAccountID    string
}

type IntercompanyTradingRule struct {
	ID               string
	SellerEntityID   string
	SalesAccountID   string
	BuyerEntityID    string
	ExpenseAccountID string
}

type UnrealizedInventoryRule struct {
	ID                    string
	SellerEntityID        string
	InventoryEntityID     string
	InventoryAccountID    string
	ExpenseGroupAccountID string
	UnrealizedProfit      decimal.Decimal
}

type EliminationRules struct {
	IntercompanyBalances []IntercompanyBalanceRule
	IntercompanyTrading  []IntercompanyTradingRule
	UnrealizedInventory  []UnrealizedInventoryRule
}

// Policies names the approved policy set used for this consolidation. Use an
// explicit "none" policy when no intercompany activity or eliminations apply.
type Policies struct {
	AccountingPolicyID          string
	ControlPolicyID             string
	CurrencyTranslationPolicyID string
	EliminationPolicyID         string
	IntercompanyPolicyID        string
	EliminationScope            PolicyScope
	IntercompanyScope           PolicyScope
}

type PolicyScope string

const (
	PolicyScopeNone       PolicyScope = "none"
	PolicyScopeConfigured PolicyScope = "configured"
)

// Group is the complete, explicit policy and account map for one consolidation.
type Group struct {
	ID                           string
	ParentEntityID               string
	ReportingCurrency            string
	Period                       ReportingPeriod
	Entities                     []Entity
	Accounts                     []GroupAccount
	Mappings                     []AccountMapping
	Ownerships                   []Ownership
	Rules                        EliminationRules
	Policies                     Policies
	TranslationReserveAccountID  string
	BargainPurchaseGainAccountID string
}

// TrialBalance is a typed adapter shape for reporting.TrialBalance. Ending
// balances drive consolidation; period movements drive current-period NCI
// income attribution. Debit/credit balances must be nonnegative and cannot
// both be nonzero.
type TrialBalance struct {
	EntityID  string
	StartDate time.Time
	EndDate   time.Time
	Currency  string
	Lines     []TrialBalanceLine
}

type TrialBalanceLine struct {
	AccountID    string
	AccountCode  string
	PeriodDebit  decimal.Decimal
	PeriodCredit decimal.Decimal
	EndingDebit  decimal.Decimal
	EndingCredit decimal.Decimal
}

// RateRequest describes an explicit quote: Rate is group-currency units per
// one unit of FromCurrency.
type RateRequest struct {
	EntityID     string
	FromCurrency string
	ToCurrency   string
	Class        RateClass
	StartDate    time.Time
	EndDate      time.Time
	AsOfDate     time.Time
}

// RateProvider must return an explicit positive rate or an error. The engine
// never substitutes a rate for a missing quote.
type RateProvider interface {
	Rate(request RateRequest) (decimal.Decimal, error)
}

type JournalLine struct {
	AccountID string
	Debit     decimal.Decimal
	Credit    decimal.Decimal
}

type JournalEntry struct {
	ID     string
	Kind   string
	Memo   string
	Lines  []JournalLine
	Debit  decimal.Decimal
	Credit decimal.Decimal
}

// Amounts in AccountBalance use debit-positive, credit-negative convention.
type AccountBalance struct {
	AccountID string
	Name      string
	Type      AccountType
	Amount    decimal.Decimal
}

type TranslationAdjustment struct {
	EntityID  string
	AccountID string
	Amount    decimal.Decimal
}

type FinancialTotals struct {
	Assets            decimal.Decimal
	Liabilities       decimal.Decimal
	Equity            decimal.Decimal
	Revenue           decimal.Decimal
	Expenses          decimal.Decimal
	NetIncome         decimal.Decimal
	NCIIncome         decimal.Decimal
	ParentNetIncome   decimal.Decimal
	BalanceDifference decimal.Decimal
	Balanced          bool
}

type Result struct {
	GroupID                string
	Period                 ReportingPeriod
	Currency               string
	Accounts               []AccountBalance
	Entries                []JournalEntry
	TranslationAdjustments []TranslationAdjustment
	Totals                 FinancialTotals
}
