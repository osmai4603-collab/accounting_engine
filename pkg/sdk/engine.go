package sdk

import (
	"context"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

type Engine interface {
	Ledger() LedgerService
	Reporting() ReportingService
	Governance() GovernanceService
	Events() EventIngestionService
	Standards() StandardsService
	Close() error
}

type StandardsService interface {
	Registry() *standards.Registry
	Diagnostics() standards.EngineDiagnostics
	HasCapability(cap standards.Capability) bool
}

type LedgerService interface {
	PostEntry(ctx context.Context, entry ledger.JournalEntry) (ledger.JournalEntry, error)
	GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error)
	ReverseEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID, reason string) (ledger.JournalEntry, error)
}

type ReportingService interface {
	GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error)
	GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error)
	GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error)
	GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error)
}

type GovernanceService interface {
	RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error
	GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error)
}

type EventIngestionService interface {
	ProcessEvent(ctx context.Context, event events.AccountingEvent) (ledger.JournalEntry, error)
}
