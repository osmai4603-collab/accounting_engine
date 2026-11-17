package ledger

import (
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
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

type AdjustingEntryType string

const (
	AdjustingEntryAccrual           AdjustingEntryType = "accrual"
	AdjustingEntryDeferral          AdjustingEntryType = "deferral"
	AdjustingEntryProvisionEstimate AdjustingEntryType = "provision_estimate"
	AdjustingEntryReclassification  AdjustingEntryType = "reclassification"
)

type EntryLine struct {
	ID                  types.JournalEntryLineID
	AccountID           types.AccountID
	PartnerID           *types.PartnerID
	CostCenterID        *types.CostCenterID
	ProjectID           *types.ProjectID
	SegmentID           *types.SegmentID
	BusinessUnitID      *types.BusinessUnitID
	MatchingNumber      string
	ReconciledAmount    decimal.Decimal
	TransactionAmount   types.Money
	TransactionCurrency string
	ExchangeRate        decimal.Decimal
	FunctionalDebit     types.Money
	FunctionalCredit    types.Money
}

type JournalEntry struct {
	ID                          types.JournalEntryID
	CompanyID                   types.CompanyID
	JournalID                   types.JournalID
	FiscalPeriodID              types.FiscalPeriodID
	EntryNumber                 string
	SequenceNumber              int64
	LegalNumber                 string
	Status                      EntryStatus
	Classification              EntryClassification
	AdjustingEntryType          AdjustingEntryType
	AdjustmentReason            string
	AdjustmentApprovalReference string
	AdjustmentAuthorized        bool
	TargetPeriodNumber          int
	EffectiveDate               time.Time
	PostingDate                 time.Time
	Description                 string
	SourceModule                string
	ReferenceID                 string
	TransactionCurrency         string
	ExchangeRate                decimal.Decimal
	Lines                       []EntryLine
	CreatedBy                   string
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
	PostedBy                    string
	PostedAt                    *time.Time
	ApprovedBy                  string
	ApprovedAt                  *time.Time
	ApprovalFingerprint         string
	ReversalOfID                *types.JournalEntryID
	ReversedByID                *types.JournalEntryID
	ReversalReason              string
}
