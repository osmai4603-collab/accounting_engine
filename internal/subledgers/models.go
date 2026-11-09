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
	return nil
}

type OpenLine struct {
	CompanyID      uuid.UUID
	ID             uuid.UUID
	PartnerID      *uuid.UUID
	Kind           LineKind
	OriginalAmount financialtypes.Money
	ResidualAmount financialtypes.Money
	DueDate        time.Time
	DocumentDate   time.Time
	Reference      string
	MatchingNumber string
	MatchIDs       []uuid.UUID
	Matches        []OpenLineMatch
	Closed         bool
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
}
