package subledger

import (
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

type SubledgerType string

const (
	SubledgerReceivable SubledgerType = "AR"
	SubledgerPayable    SubledgerType = "AP"
	SubledgerFixedAsset SubledgerType = "FA"
	SubledgerTreasury   SubledgerType = "TR"
)

type Receivable struct {
	ID                        string
	CompanyID                 types.CompanyID
	PartnerID                 string
	ControlAccountID          types.AccountID
	JournalEntryID            types.JournalEntryID
	Amount                    types.Money
	RemainingAmount           types.Money
	FunctionalAmount          types.Money
	FunctionalRemainingAmount types.Money
	DocumentDate              time.Time
	DueDate                   time.Time
	Reference                 string
	Status                    string
	CreatedAt                 time.Time
}

type Payable struct {
	ID                        string
	CompanyID                 types.CompanyID
	PartnerID                 string
	ControlAccountID          types.AccountID
	JournalEntryID            types.JournalEntryID
	Amount                    types.Money
	RemainingAmount           types.Money
	FunctionalAmount          types.Money
	FunctionalRemainingAmount types.Money
	DocumentDate              time.Time
	DueDate                   time.Time
	Reference                 string
	Status                    string
	CreatedAt                 time.Time
}

type Payment struct {
	ID               string
	CompanyID        types.CompanyID
	PartnerID        string
	Kind             SubledgerType
	Amount           types.Money
	FunctionalAmount types.Money
	PaymentDate      time.Time
	Reference        string
	JournalEntryID   types.JournalEntryID
	AllocatedLines   []string
}

type AgingBucket struct {
	RangeLabel  string
	Currency    string
	TotalAmount decimal.Decimal
	Count       int
}

// InvoiceCommand describes the two sides of an AR/AP invoice accounting event.
// The control account is debited for receivables and credited for payables.
type InvoiceCommand struct {
	CompanyID        types.CompanyID
	PartnerID        string
	ControlAccountID types.AccountID
	CounterAccountID types.AccountID
	JournalID        types.JournalID
	Amount           types.Money
	FunctionalAmount types.Money
	DocumentDate     time.Time
	DueDate          time.Time
	Reference        string
	Description      string
}

type InvoiceResult struct {
	Receivable *Receivable
	Payable    *Payable
	EntryID    types.JournalEntryID
}

type PaymentAllocation struct {
	OpenItemID       string
	Amount           types.Money
	FunctionalAmount types.Money
}

type PaymentCommand struct {
	CompanyID        types.CompanyID
	Kind             SubledgerType
	CashAccountID    types.AccountID
	JournalID        types.JournalID
	Amount           types.Money
	FunctionalAmount types.Money
	PaymentDate      time.Time
	Reference        string
	Description      string
	Allocations      []PaymentAllocation
}

type PaymentResult struct {
	Payment Payment
	EntryID types.JournalEntryID
}

type WriteOffCommand struct {
	CompanyID           types.CompanyID
	Kind                SubledgerType
	OpenItemID          string
	AdjustmentAccountID types.AccountID
	JournalID           types.JournalID
	Amount              types.Money
	FunctionalAmount    types.Money
	Date                time.Time
	Reference           string
	Description         string
}

type ReconciliationEvidence struct {
	CompanyID        types.CompanyID
	Kind             SubledgerType
	ControlAccountID types.AccountID
	Currency         string
	AsOf             time.Time
	SubledgerBalance decimal.Decimal
	GLBalance        decimal.Decimal
	Difference       decimal.Decimal
	Reconciled       bool
	EvidenceAt       time.Time
}

type FixedAsset struct {
	ID                 string
	CompanyID          types.CompanyID
	AssetName          string
	Category           string
	AcquisitionDate    time.Time
	AcquisitionCost    types.Money
	UsefulLifeMonths   int
	SalvageValue       types.Money
	DepreciationMethod string
	AccumulatedDep     types.Money
	NetBookValue       types.Money
	Status             string
}

type DepreciationSchedule struct {
	ScheduleID         string
	AssetID            string
	PeriodID           string
	DepreciationAmount types.Money
	BookValueAfter     types.Money
	Posted             bool
}

type AssetDisposal struct {
	DisposalID   string
	AssetID      string
	DisposalDate time.Time
	Proceeds     types.Money
	GainOrLoss   types.Money
}

type BankAccount struct {
	AccountID     string
	CompanyID     types.CompanyID
	BankName      string
	AccountNumber string
	Currency      string
	GLAccountID   types.AccountID
}

type TreasuryTransaction struct {
	TransactionID   string
	CompanyID       types.CompanyID
	BankAccountID   string
	Amount          types.Money
	TransactionType string
	ValueDate       time.Time
	Reference       string
}

type ReconciliationReport struct {
	ReportID         string
	CompanyID        types.CompanyID
	BankAccountID    string
	StatementBalance decimal.Decimal
	BookBalance      decimal.Decimal
	UnmatchedItems   int
	Reconciled       bool
}
