// Package application defines the stable use-case contracts of the accounting
// engine: commands, queries, principal/authorization types, and the error
// taxonomy returned by every transport.
package application

import (
	"context"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

// Principal is a verified caller identity supplied by the host application or
// transport. The engine never trusts a bare CompanyID from request payloads.
type Principal struct {
	ActorID     string
	TenantID    string
	CompanyIDs  []types.CompanyID
	Permissions []string
}

// Action is an authorization action checked before the application layer runs.
type Action string

const (
	ActionRead     Action = "ledger.read"
	ActionCreate   Action = "ledger.create"
	ActionValidate Action = "ledger.validate"
	ActionPost     Action = "ledger.post"
	ActionReverse  Action = "ledger.reverse"
	ActionClose    Action = "period.close"
	ActionReopen   Action = "period.reopen"
)

// AuthorizationRequest is passed to an optional host-provided Authorizer.
type AuthorizationRequest struct {
	Principal Principal
	CompanyID types.CompanyID
	Action    Action
	// JournalID and EntryID are set when the action is scoped to one resource.
	JournalID *types.JournalID
	EntryID   *types.JournalEntryID
}

// Authorizer is an optional host capability. When absent, the engine still
// enforces principal presence and company membership.
type Authorizer interface {
	Authorize(ctx context.Context, request AuthorizationRequest) error
}

// Context carries the caller identity, correlation and idempotency data that
// apply to a whole use case.
type Context struct {
	Principal      Principal
	CorrelationID  string
	IdempotencyKey string
	SourceSystem   string
}

// CreateDraftCommand creates a draft entry after validating master data and
// double-entry invariants.
type CreateDraftCommand struct {
	Context
	CompanyID           types.CompanyID
	JournalID           types.JournalID
	FiscalPeriodID      types.FiscalPeriodID
	EffectiveDate       time.Time
	PostingDate         time.Time
	Description         string
	SourceModule        string
	ReferenceID         string
	TransactionCurrency string
	ExchangeRate        string
	Classification      ledger.EntryClassification
	Lines               []ledger.EntryLine
}

// ValidateEntryCommand re-runs all validation rules for an existing draft.
type ValidateEntryCommand struct {
	Context
	CompanyID types.CompanyID
	EntryID   types.JournalEntryID
}

// PostEntryCommand posts an existing, validated draft.
type PostEntryCommand struct {
	Context
	CompanyID types.CompanyID
	EntryID   types.JournalEntryID
}

// ReverseEntryCommand creates and posts a full reversal. Reason and ActorID
// are intentionally separate fields.
type ReverseEntryCommand struct {
	Context
	CompanyID      types.CompanyID
	EntryID        types.JournalEntryID
	Reason         string
	EffectiveDate  time.Time
	PostingDate    time.Time
	IdempotencyKey string
}

// GetEntryQuery reads one company-scoped entry.
type GetEntryQuery struct {
	Context
	CompanyID types.CompanyID
	EntryID   types.JournalEntryID
}

// EntryResult is returned by mutating use cases. IdempotentReplay reports that
// an earlier identical request produced this result.
type EntryResult struct {
	Entry            ledger.JournalEntry
	IdempotentReplay bool
}

// Service is the single accounting use-case surface. Every transport and
// integration adapter must call it; none may post directly.
type Service interface {
	CreateDraft(ctx context.Context, command CreateDraftCommand) (EntryResult, error)
	Validate(ctx context.Context, command ValidateEntryCommand) error
	Post(ctx context.Context, command PostEntryCommand) (EntryResult, error)
	Reverse(ctx context.Context, command ReverseEntryCommand) (EntryResult, error)
	GetEntry(ctx context.Context, query GetEntryQuery) (ledger.JournalEntry, error)
}

// Capabilities describes which optional capabilities a built engine exposes.
type Capabilities struct {
	Reporting      bool     `json:"reporting"`
	Governance     bool     `json:"governance"`
	Standards      []string `json:"standards,omitempty"`
	Idempotency    bool     `json:"idempotency"`
	Authorization  bool     `json:"authorization"`
	Transactions   bool     `json:"transactions"`
	DisablingNotes []string `json:"disabling_notes,omitempty"`
}
