package subledgers

import (
	"strings"
	"time"

	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type EconomicEvent struct {
	CompanyID      uuid.UUID
	IdempotencyKey string
	EventType      string
	Date           time.Time
	Amount         financialtypes.Money
	Values         map[string]financialtypes.Money
	Attributes     map[string]string
	PartnerID      *uuid.UUID
	Reference      string
	ResultEntryID  uuid.UUID
}

func (e EconomicEvent) Validate() error {
	if e.CompanyID == uuid.Nil || strings.TrimSpace(e.IdempotencyKey) == "" ||
		strings.TrimSpace(e.EventType) == "" ||
		e.Amount.Currency() == "" || e.Amount.IsNegative() {
		return ErrInvalidEvent
	}
	for key, amount := range e.Values {
		if strings.TrimSpace(key) == "" || amount.Currency() != e.Amount.Currency() || amount.IsNegative() {
			return ErrInvalidEvent
		}
	}
	return nil
}

type EventLineSide string

const (
	EventLineDebit  EventLineSide = "debit"
	EventLineCredit EventLineSide = "credit"
)

type EventLineTemplate struct {
	AccountLookupKey string
	AccountID        uuid.UUID
	Side             EventLineSide
	AmountFormula    string
	Description      string
}

type EventAccountingRule struct {
	ID         uuid.UUID
	CompanyID  uuid.UUID
	EventType  string
	RuleName   string
	JournalID  uuid.UUID
	Priority   int
	Conditions map[string]string
	Accounts   map[string]uuid.UUID
	Lines      []EventLineTemplate
}

func (r EventAccountingRule) Validate() error {
	if r.CompanyID == uuid.Nil || strings.TrimSpace(r.EventType) == "" ||
		strings.TrimSpace(r.RuleName) == "" || r.JournalID == uuid.Nil || len(r.Lines) < 2 {
		return ErrMappingNotFound
	}
	for _, line := range r.Lines {
		if (line.AccountID == uuid.Nil && strings.TrimSpace(line.AccountLookupKey) == "") ||
			(line.Side != EventLineDebit && line.Side != EventLineCredit) ||
			strings.TrimSpace(line.AmountFormula) == "" {
			return ErrMappingNotFound
		}
		if line.AccountID == uuid.Nil && r.Accounts[line.AccountLookupKey] == uuid.Nil {
			return ErrMappingNotFound
		}
	}
	return nil
}

type OpenLine struct {
	CompanyID        uuid.UUID
	ID               uuid.UUID
	PartnerID        *uuid.UUID
	ControlAccountID uuid.UUID
	JournalEntryID   uuid.UUID
	Kind             LineKind
	OriginalAmount   financialtypes.Money
	ResidualAmount   financialtypes.Money
	DueDate          time.Time
	DocumentDate     time.Time
	Reference        string
	MatchingNumber   string
	MatchIDs         []uuid.UUID
	Matches          []OpenLineMatch
	Closed           bool
}

type OpenLineMatch struct {
	MatchingNumber string
	CounterpartID  uuid.UUID
	Amount         financialtypes.Money
}

func linePartner(line OpenLine) string {
	if line.PartnerID == nil {
		return ""
	}
	return line.PartnerID.String()
}

type LineKind string

const (
	LineReceivable LineKind = "receivable"
	LinePayable    LineKind = "payable"
)

type FXMatchingParams struct {
	JournalID          uuid.UUID
	Date               time.Time
	OriginalRate       decimal.Decimal
	SettlementRate     decimal.Decimal
	FunctionalCurrency string
	ARAPAccountID      uuid.UUID
	FXGainAccountID    uuid.UUID
	FXLossAccountID    uuid.UUID
	Description        string
}

type BankFeeParams struct {
	JournalID     uuid.UUID
	Date          time.Time
	FeeAccountID  uuid.UUID
	BankAccountID uuid.UUID
	Description   string
}

type AgingBucket string

const (
	AgingCurrent AgingBucket = "current"
	Aging1To30   AgingBucket = "1_30"
	Aging31To60  AgingBucket = "31_60"
	Aging61To90  AgingBucket = "61_90"
	AgingOver90  AgingBucket = "over_90"
)

type AgingSchedule map[AgingBucket]financialtypes.Money

type BankItem struct {
	CompanyID uuid.UUID
	ID        uuid.UUID
	AccountID uuid.UUID
	Date      time.Time
	Amount    financialtypes.Money
	Reference string
	Status    BankItemStatus
}

type BankItemStatus string

const (
	BankItemPending    BankItemStatus = "pending"
	BankItemMatched    BankItemStatus = "matched"
	BankItemUnrecorded BankItemStatus = "unrecorded"
)

type DepreciationMethod string

const (
	StraightLine     DepreciationMethod = "straight_line"
	DecliningBalance DepreciationMethod = "declining_balance"
)

type FixedAsset struct {
	CompanyID                      uuid.UUID
	ID                             uuid.UUID
	HistoricalCost                 financialtypes.Money
	SalvageValue                   financialtypes.Money
	UsefulLifeMonths               int
	Method                         DepreciationMethod
	InServiceDate                  time.Time
	AccumulatedDepreciation        financialtypes.Money
	AccumulatedImpairment          financialtypes.Money
	CurrentBookValue               financialtypes.Money
	DecliningRate                  decimal.Decimal
	ExpenseAccountID               uuid.UUID
	AccumulatedAccountID           uuid.UUID
	AccumulatedImpairmentAccountID uuid.UUID
	AssetAccountID                 uuid.UUID
	LastDepreciationDate           time.Time
	ImpairmentAccountID            uuid.UUID
	DisposalGainAccountID          uuid.UUID
	DisposalLossAccountID          uuid.UUID
	DisposalCashAccountID          uuid.UUID
	DisposedAt                     *time.Time
	AppliedEntryIDs                []uuid.UUID
	Components                     []AssetComponent
	RevaluationAdjustment          financialtypes.Money
	RevaluationSurplus             financialtypes.Money
	RevaluationSurplusAccountID    uuid.UUID
	RevaluationLossAccountID       uuid.UUID
}

type AssetComponent struct {
	ID                      uuid.UUID
	Name                    string
	Cost                    financialtypes.Money
	SalvageValue            financialtypes.Money
	UsefulLifeMonths        int
	Method                  DepreciationMethod
	InServiceDate           time.Time
	DecliningRate           decimal.Decimal
	ExpenseAccountID        uuid.UUID
	AccumulatedAccountID    uuid.UUID
	AccumulatedDepreciation financialtypes.Money
}
