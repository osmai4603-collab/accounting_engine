package ledger

import (
	"time"

	"accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

type EntryStatus string

const (
	EntryDraft    EntryStatus = "draft"
	EntryPosted   EntryStatus = "posted"
	EntryReversed EntryStatus = "reversed"
)

type EntryClassification string

const (
	EntryClassRegular   EntryClassification = "regular"
	EntryClassAdjusting EntryClassification = "adjusting"
	EntryClassClosing   EntryClassification = "closing"
	EntryClassOpening   EntryClassification = "opening"
	EntryClassReversing EntryClassification = "reversing"
)

type EntryLine struct {
	ID                  types.AccountID
	AccountID           types.AccountID
	PartnerID           *types.CompanyID
	CostCenterID        *types.CompanyID
	ProjectID           *types.CompanyID
	SegmentID           *types.CompanyID
	BusinessUnitID      *types.CompanyID
	MatchingNumber      string
	ReconciledAmount    decimal.Decimal
	TransactionAmount   types.Money
	TransactionCurrency string
	ExchangeRate        decimal.Decimal
	FunctionalDebit     types.Money
	FunctionalCredit    types.Money
}

type JournalEntry struct {
	ID                  types.JournalEntryID
	CompanyID           types.CompanyID
	FiscalPeriodID      types.FiscalPeriodID
	EntryNumber         string
	Status              EntryStatus
	Classification      EntryClassification
	EffectiveDate       time.Time
	PostingDate         time.Time
	Description         string
	SourceModule        string
	ReferenceID         string
	TransactionCurrency string
	ExchangeRate        decimal.Decimal
	Lines               []EntryLine
	CreatedBy           string
	ApprovedBy          string
	ApprovalFingerprint string
	ReversalOfID        *types.JournalEntryID
}
