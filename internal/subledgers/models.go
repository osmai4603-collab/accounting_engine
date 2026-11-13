package subledgers

import (
	"strings"
	"time"

	"github.com/google/uuid"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

type EconomicEvent struct {
	EventID          uuid.UUID
	CompanyID        uuid.UUID
	SourceSystem     string
	ExternalID       string
	IdempotencyKey   string
	EventType        string
	EventVersion     int
	PayloadJSON      []byte
	Status           EventStatus
	GeneratedEntryID *uuid.UUID
	ErrorMessage     string
	CreatedAt        time.Time
	ProcessedAt      *time.Time

	// Deprecated: use GeneratedEntryID. Kept for existing integrations.
	PayloadHash   string
	Date          time.Time
	MatchedAt     time.Time
	Amount        financialtypes.Money
	Values        map[string]financialtypes.Money
	Attributes    map[string]string
	PartnerID     *uuid.UUID
	Reference     string
	ResultEntryID uuid.UUID
}

type EventStatus string

const (
	EventReceived   EventStatus = "received"
	EventProcessed  EventStatus = "processed"
	EventRejected   EventStatus = "rejected"
	EventDeadLetter EventStatus = "dead_letter"
)

func (e EconomicEvent) Validate() error {
	if e.CompanyID == uuid.Nil || strings.TrimSpace(e.IdempotencyKey) == "" ||
		len(e.IdempotencyKey) > 255 || len(e.SourceSystem) > 255 || len(e.ExternalID) > 255 ||
		strings.TrimSpace(e.EventType) == "" || len(e.EventType) > 128 ||
		e.Amount.Currency() == "" || e.Amount.IsNegative() ||
		(e.PayloadHash != "" && len(e.PayloadHash) != 64) ||
		e.EventVersion < 0 ||
		(e.GeneratedEntryID != nil && *e.GeneratedEntryID == uuid.Nil) ||
		(e.Status != "" && e.Status != EventReceived && e.Status != EventProcessed &&
			e.Status != EventRejected && e.Status != EventDeadLetter) {
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
	CompanyID                   uuid.UUID
	ID                          uuid.UUID
	PartnerID                   *uuid.UUID
	ControlAccountID            uuid.UUID
	JournalEntryID              uuid.UUID
	Kind                        LineKind
	OriginalAmount              financialtypes.Money
	ResidualAmount              financialtypes.Money
	FunctionalOriginalAmount    financialtypes.Money
	FunctionalResidualAmount    financialtypes.Money
	PostingDate                 time.Time
	DueDate                     time.Time
	DocumentDate                time.Time
	Reference                   string
	MatchingNumber              string
	MatchIDs                    []uuid.UUID
	Matches                     []OpenLineMatch
	MatchHistory                []OpenLineMatch
	Closed                      bool
	WrittenOffAmount            financialtypes.Money
	PendingWriteOffID           uuid.UUID
	PendingWriteOffAmount       financialtypes.Money
	WriteOffAdjustmentAccountID uuid.UUID
}

type OpenLineMatch struct {
	MatchingNumber   string
	CounterpartID    uuid.UUID
	Amount           financialtypes.Money
	FunctionalAmount financialtypes.Money
	MatchedAt        time.Time
	ReversedAt       time.Time
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

type WriteOffParams struct {
	JournalID           uuid.UUID
	AdjustmentAccountID uuid.UUID
	Date                time.Time
	Description         string
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
	CompanyID      uuid.UUID
	ID             uuid.UUID
	AccountID      uuid.UUID
	JournalEntryID uuid.UUID
	Date           time.Time
	MatchedAt      time.Time
	Amount         financialtypes.Money
	Reference      string
	Status         BankItemStatus
}

type BankAccount struct {
	ID                uuid.UUID
	CompanyID         uuid.UUID
	BankName          string
	IBAN              string
	Currency          string
	GLAccountID       uuid.UUID
	ClearingAccountID uuid.UUID
}

func (a BankAccount) ValidateForCompany(companyID uuid.UUID) error {
	if a.ID == uuid.Nil || a.CompanyID == uuid.Nil ||
		(companyID != uuid.Nil && a.CompanyID != companyID) ||
		strings.TrimSpace(a.BankName) == "" || strings.TrimSpace(a.IBAN) == "" ||
		a.Currency == "" || a.GLAccountID == uuid.Nil || a.ClearingAccountID == uuid.Nil ||
		a.GLAccountID == a.ClearingAccountID {
		if companyID != uuid.Nil && a.CompanyID != uuid.Nil && a.CompanyID != companyID {
			return ErrCompanyMismatch
		}
		return ErrInvalidBankAccount
	}
	if _, ok := financialtypes.LookupCurrency(a.Currency); !ok {
		return ErrInvalidBankAccount
	}
	return nil
}

type BankTransferStatus string

const (
	BankTransferInTransit BankTransferStatus = "in_transit"
	BankTransferSettled   BankTransferStatus = "settled"
)

type InterbankTransfer struct {
	ID                   uuid.UUID
	CompanyID            uuid.UUID
	SourceBankAccountID  uuid.UUID
	DestinationAccountID uuid.UUID
	ClearingAccountID    uuid.UUID
	Amount               financialtypes.Money
	Reference            string
	Status               BankTransferStatus
	InitiatedEntryID     uuid.UUID
	SettlementEntryID    uuid.UUID
	InitiatedAt          time.Time
	SettledAt            *time.Time
}

type BankReconciliationMatch struct {
	StatementLineIDs []uuid.UUID
	JournalEntryIDs  []uuid.UUID
	LedgerItemIDs    []uuid.UUID
	BankFeesAmount   financialtypes.Money
	Difference       decimal.Decimal
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
	RemainingUsefulLifeMonths      int
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
	IsCWIP                         bool
	CWIPAccountID                  uuid.UUID
	PendingOperation               string
	PendingOperationEntryID        uuid.UUID
	AppliedEntryIDs                []uuid.UUID
	Components                     []AssetComponent
	RevaluationAdjustment          financialtypes.Money
	RevaluationSurplus             financialtypes.Money
	RevaluationSurplusAccountID    uuid.UUID
	RevaluationLossAccountID       uuid.UUID
	LastRevaluationDate            time.Time
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
