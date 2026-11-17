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

var ErrCapabilityNotImplemented = types.ErrCapabilityNotImplemented

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
	ActionRead             Action = "ledger.read"
	ActionCreate           Action = "ledger.create"
	ActionValidate         Action = "ledger.validate"
	ActionPost             Action = "ledger.post"
	ActionReverse          Action = "ledger.reverse"
	ActionCreateAdjustment Action = "ledger.adjustment.create"
	ActionClose            Action = "period.close"
	ActionReopen           Action = "period.reopen"
)

// AuthorizationRequest is passed to an optional host-provided Authorizer.
type AuthorizationRequest struct {
	Principal Principal
	CompanyID types.CompanyID
	Action    Action
	// JournalID and EntryID are set when the action is scoped to one resource.
	JournalID *types.JournalID
	EntryID   *types.JournalEntryID
	// ApprovalReference lets the host verify the cited adjustment delegation.
	ApprovalReference string
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

type principalContextKey struct{}
type requestMetadataContextKey struct{}

// RequestMetadata carries transport-neutral tracing and source information.
type RequestMetadata struct {
	CorrelationID  string
	IdempotencyKey string
	SourceSystem   string
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func WithRequestMetadata(ctx context.Context, metadata RequestMetadata) context.Context {
	return context.WithValue(ctx, requestMetadataContextKey{}, metadata)
}

func RequestMetadataFromContext(ctx context.Context) (RequestMetadata, bool) {
	metadata, ok := ctx.Value(requestMetadataContextKey{}).(RequestMetadata)
	return metadata, ok
}

// CreateDraftCommand creates a draft entry after validating master data and
// double-entry invariants.
type CreateDraftCommand struct {
	Context
	CompanyID                   types.CompanyID
	JournalID                   types.JournalID
	FiscalPeriodID              types.FiscalPeriodID
	EffectiveDate               time.Time
	PostingDate                 time.Time
	Description                 string
	SourceModule                string
	ReferenceID                 string
	TransactionCurrency         string
	ExchangeRate                string
	Classification              ledger.EntryClassification
	TargetPeriodNumber          int
	AdjustingEntryType          ledger.AdjustingEntryType
	AdjustmentReason            string
	AdjustmentApprovalReference string
	Lines                       []ledger.EntryLine
}

// CreateAdjustingEntryCommand submits an adjusting entry together with the
// host-authorized adjustment delegation and its audit evidence.
type CreateAdjustingEntryCommand struct {
	Draft             CreateDraftCommand
	Type              ledger.AdjustingEntryType
	Reason            string
	ApprovalReference string
}

// AdjustingEntryService is an optional application capability for creating
// adjusting entries. The host Authorizer remains responsible for confirming
// that the principal may exercise the cited adjustment approval.
type AdjustingEntryService interface {
	CreateAdjustingEntry(context.Context, CreateAdjustingEntryCommand) (EntryResult, error)
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

// YearEndClosingPolicy selects the destination used to close nominal
// accounts. The income-summary policy creates a second transfer entry.
type YearEndClosingPolicy string

const (
	YearEndClosingDirectToRetainedEarnings YearEndClosingPolicy = "direct_to_retained_earnings"
	YearEndClosingThroughIncomeSummary     YearEndClosingPolicy = "income_summary"
)

// ExecuteYearEndClosingCommand requests an audited annual close. Opening
// balances are generated only when NextFiscalYearID is supplied.
type ExecuteYearEndClosingCommand struct {
	Context
	CompanyID                 types.CompanyID
	FiscalYearID              types.FiscalYearID
	NextFiscalYearID          types.FiscalYearID
	JournalID                 types.JournalID
	RetainedEarningsAccountID types.AccountID
	IncomeSummaryAccountID    types.AccountID
	Policy                    YearEndClosingPolicy
	Currency                  string
	Reason                    string
}

type YearEndClosingResult struct {
	ClosingEntryIDs []types.JournalEntryID
	OpeningEntryID  types.JournalEntryID
	NetIncome       string
}

type ReopenYearEndClosingCommand struct {
	Context
	CompanyID         types.CompanyID
	FiscalYearID      types.FiscalYearID
	NextFiscalYearID  types.FiscalYearID
	ApprovalReference string
	Reason            string
}

type ReopenYearEndClosingResult struct {
	ReversalEntryIDs []types.JournalEntryID
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

// YearEndClosingService is an optional application capability. It is
// separate from Service so hosts without a transactional close adapter do not
// accidentally advertise a non-atomic implementation.
type YearEndClosingService interface {
	ExecuteYearEndClosing(ctx context.Context, command ExecuteYearEndClosingCommand) (YearEndClosingResult, error)
	ReopenYearEndClosing(ctx context.Context, command ReopenYearEndClosingCommand) (ReopenYearEndClosingResult, error)
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
