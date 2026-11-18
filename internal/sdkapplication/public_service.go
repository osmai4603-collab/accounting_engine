package sdkapplication

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/osmai4603-collab/accounting_engine/internal/apperrors"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/shopspring/decimal"
)

type publicService struct {
	app        *Application
	authorizer publicapplication.Authorizer
}

func validAdjustingEntryRequest(command publicapplication.CreateAdjustingEntryCommand) bool {
	if strings.TrimSpace(command.Reason) == "" || strings.TrimSpace(command.ApprovalReference) == "" {
		return false
	}
	switch command.Type {
	case ledger.AdjustingEntryAccrual, ledger.AdjustingEntryDeferral,
		ledger.AdjustingEntryProvisionEstimate, ledger.AdjustingEntryReclassification:
		return true
	default:
		return false
	}
}

func NewPublicService(app *Application, authorizer publicapplication.Authorizer) publicapplication.Service {
	if app == nil {
		return nil
	}
	return &publicService{app: app, authorizer: authorizer}
}

var _ publicapplication.Service = (*publicService)(nil)
var _ publicapplication.AdjustingEntryService = (*publicService)(nil)
var _ publicapplication.TransactionalService = (*publicService)(nil)
var _ publicapplication.IdempotentPostingLookup = (*publicService)(nil)
var _ publicapplication.DeterministicDraftCreator = (*publicService)(nil)

func principalActorID(principal publicapplication.Principal) string {
	return strings.TrimSpace(principal.ActorID)
}

func classifyUseCaseError(err error) error {
	if err == nil {
		return nil
	}
	var publicError interface{ PublicMessage() string }
	if errors.As(err, &publicError) {
		return err
	}
	if errors.Is(err, posting.ErrEntryNotFound) {
		return fmt.Errorf("%w: %w", publicapplication.NewError(publicapplication.ErrNotFound, "journal entry not found", nil), err)
	}
	_, category := posting.ClassifyError(err)
	switch category {
	case apperrors.Validation:
		return fmt.Errorf("%w: %w", publicapplication.NewError(publicapplication.ErrValidation, "accounting validation failed", nil), err)
	case apperrors.Conflict:
		return fmt.Errorf("%w: %w", publicapplication.NewError(publicapplication.ErrConflict, "accounting state conflict", nil), err)
	case apperrors.Authorization:
		return fmt.Errorf("%w: %w", publicapplication.NewError(publicapplication.ErrForbidden, "posting authorization required", nil), err)
	case apperrors.Infrastructure:
		return fmt.Errorf("%w: %w", publicapplication.NewError(publicapplication.ErrDependencyUnavailable, "accounting dependency unavailable", nil), err)
	default:
		return fmt.Errorf("%w: %w", publicapplication.NewError(publicapplication.ErrInternal, "accounting operation failed", nil), err)
	}
}

func (s *publicService) authorize(
	ctx context.Context,
	commandContext publicapplication.Context,
	companyID types.CompanyID,
	action publicapplication.Action,
	entryID *types.JournalEntryID,
	approvalReference string,
) error {
	principal := commandContext.Principal
	if principalActorID(principal) == "" {
		return publicapplication.NewError(publicapplication.ErrUnauthorized, "verified actor identity is required", nil)
	}
	if companyID == (types.CompanyID{}) {
		return publicapplication.NewError(publicapplication.ErrValidation, "company id is required", nil)
	}
	member := false
	for _, allowedCompanyID := range principal.CompanyIDs {
		if allowedCompanyID == companyID {
			member = true
			break
		}
	}
	if !member {
		return publicapplication.NewError(publicapplication.ErrForbidden, "principal is not authorized for this company", nil)
	}
	if s.authorizer != nil {
		request := publicapplication.AuthorizationRequest{
			Principal:         principal,
			CompanyID:         companyID,
			Action:            action,
			EntryID:           entryID,
			ApprovalReference: approvalReference,
		}
		if err := s.authorizer.Authorize(ctx, request); err != nil {
			return publicapplication.NewError(publicapplication.ErrForbidden, "authorization denied", err)
		}
	}
	return nil
}

func toEntryFromCreateCommand(command publicapplication.CreateDraftCommand) (ledger.JournalEntry, error) {
	now := time.Now().UTC()
	entry := ledger.JournalEntry{
		CompanyID:                   command.CompanyID,
		JournalID:                   command.JournalID,
		FiscalPeriodID:              command.FiscalPeriodID,
		EffectiveDate:               command.EffectiveDate,
		PostingDate:                 command.PostingDate,
		Description:                 command.Description,
		SourceModule:                command.SourceModule,
		ReferenceID:                 command.ReferenceID,
		TransactionCurrency:         command.TransactionCurrency,
		Classification:              command.Classification,
		TargetPeriodNumber:          command.TargetPeriodNumber,
		AdjustingEntryType:          ledger.AdjustingEntryType(command.AdjustingEntryType),
		AdjustmentReason:            command.AdjustmentReason,
		AdjustmentApprovalReference: command.AdjustmentApprovalReference,
		Lines:                       command.Lines,
		CreatedBy:                   principalActorID(command.Context.Principal),
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	if command.EffectiveDate.IsZero() {
		entry.EffectiveDate = now
	}
	if command.PostingDate.IsZero() {
		entry.PostingDate = entry.EffectiveDate
	}
	if command.ExchangeRate != "" {
		rate, err := decimal.NewFromString(command.ExchangeRate)
		if err != nil {
			return ledger.JournalEntry{}, publicapplication.NewError(publicapplication.ErrValidation, "invalid exchange rate", err)
		}
		entry.ExchangeRate = rate
	}
	return entry, nil
}

func (s *publicService) CreateDraft(ctx context.Context, command publicapplication.CreateDraftCommand) (publicapplication.EntryResult, error) {
	return s.createDraft(ctx, command, types.JournalEntryID{}, publicapplication.ActionCreate, false, "")
}

func (s *publicService) CreateDraftWithID(
	ctx context.Context,
	command publicapplication.CreateDraftCommand,
	entryID types.JournalEntryID,
) (publicapplication.EntryResult, error) {
	return s.createDraft(ctx, command, entryID, publicapplication.ActionCreate, false, "")
}

func (s *publicService) CreateAdjustingEntry(
	ctx context.Context,
	command publicapplication.CreateAdjustingEntryCommand,
) (publicapplication.EntryResult, error) {
	draft := command.Draft
	draft.Classification = ledger.EntryClassAdjusting
	draft.AdjustingEntryType = command.Type
	draft.AdjustmentReason = strings.TrimSpace(command.Reason)
	draft.AdjustmentApprovalReference = strings.TrimSpace(command.ApprovalReference)
	if !validAdjustingEntryRequest(command) {
		return publicapplication.EntryResult{}, classifyUseCaseError(posting.ErrAdjustingEntryMetadataRequired)
	}
	if s.authorizer == nil {
		return publicapplication.EntryResult{}, publicapplication.NewError(
			publicapplication.ErrForbidden,
			"host authorization is required for adjusting entries",
			nil,
		)
	}
	return s.createDraft(ctx, draft, types.JournalEntryID{}, publicapplication.ActionCreateAdjustment, true,
		strings.TrimSpace(command.ApprovalReference))
}

func (s *publicService) createDraft(
	ctx context.Context,
	command publicapplication.CreateDraftCommand,
	entryID types.JournalEntryID,
	action publicapplication.Action,
	authorizedAdjustment bool,
	approvalReference string,
) (publicapplication.EntryResult, error) {
	if command.Classification == ledger.EntryClassAdjusting && !authorizedAdjustment {
		return publicapplication.EntryResult{}, classifyUseCaseError(posting.ErrAdjustmentDelegationRequired)
	}
	if err := s.authorize(ctx, command.Context, command.CompanyID, action, nil, approvalReference); err != nil {
		return publicapplication.EntryResult{}, err
	}
	entry, err := toEntryFromCreateCommand(command)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}
	entry.ID = entryID
	if authorizedAdjustment {
		now := time.Now().UTC()
		entry.AdjustmentAuthorized = true
		entry.ApprovedBy = principalActorID(command.Context.Principal)
		entry.ApprovedAt = &now
	}
	created, err := s.app.CreateDraftEntry(ctx, entry, principalActorID(command.Context.Principal))
	if err != nil {
		return publicapplication.EntryResult{}, classifyUseCaseError(err)
	}
	return publicapplication.EntryResult{Entry: created}, nil
}

func (s *publicService) Validate(ctx context.Context, command publicapplication.ValidateEntryCommand) error {
	entryID := command.EntryID
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionValidate, &entryID, ""); err != nil {
		return err
	}
	return classifyUseCaseError(s.app.ValidateEntryByID(ctx, command.CompanyID, command.EntryID))
}

func (s *publicService) Post(ctx context.Context, command publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
	entryID := command.EntryID
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionPost, &entryID, ""); err != nil {
		return publicapplication.EntryResult{}, err
	}
	posted, replay, err := s.app.PostEntryByIDWithIdempotencyResult(
		ctx, command.CompanyID, command.EntryID,
		principalActorID(command.Context.Principal), command.Context.IdempotencyKey,
	)
	if err != nil {
		return publicapplication.EntryResult{}, classifyUseCaseError(err)
	}
	return publicapplication.EntryResult{Entry: posted, IdempotentReplay: replay}, nil
}

// WithinTransaction lets hosts run a whole operation — posting, subledger
// updates, audit — inside the storage adapter's single unit of work. It is a
// capability: hosts must check for it rather than assume it.
func (s *publicService) WithinTransaction(
	ctx context.Context,
	companyID types.CompanyID,
	fn func(ctx context.Context) error,
) error {
	if s.app == nil {
		return types.ErrCapabilityNotImplemented
	}
	return s.app.WithinTransaction(ctx, companyID, fn)
}

// FindPostedEntryByIdempotencyKey exposes replay detection so callers can
// avoid re-applying non-posting side effects (for example subledger balance
// updates) when a retry of an idempotent operation arrives.
func (s *publicService) FindPostedEntryByIdempotencyKey(
	ctx context.Context,
	companyID types.CompanyID,
	key string,
) (ledger.JournalEntry, bool, error) {
	if s.app == nil {
		return ledger.JournalEntry{}, false, types.ErrCapabilityNotImplemented
	}
	return s.app.FindPostedEntryByIdempotencyKey(ctx, companyID, key)
}

func (s *publicService) Reverse(ctx context.Context, command publicapplication.ReverseEntryCommand) (publicapplication.EntryResult, error) {
	entryID := command.EntryID
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionReverse, &entryID, ""); err != nil {
		return publicapplication.EntryResult{}, err
	}
	idempotencyKey := strings.TrimSpace(command.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(command.Context.IdempotencyKey)
	}
	reversed, replay, err := s.app.ReverseEntryByIDWithMetadata(
		ctx, command.CompanyID, command.EntryID,
		principalActorID(command.Context.Principal), command.Reason, command.EffectiveDate,
		idempotencyKey, command.Context.CorrelationID, command.Context.SourceSystem,
	)
	if err != nil {
		return publicapplication.EntryResult{}, classifyUseCaseError(err)
	}
	return publicapplication.EntryResult{Entry: reversed, IdempotentReplay: replay}, nil
}

func (s *publicService) GetEntry(ctx context.Context, query publicapplication.GetEntryQuery) (ledger.JournalEntry, error) {
	entryID := query.EntryID
	if err := s.authorize(ctx, query.Context, query.CompanyID, publicapplication.ActionRead, &entryID, ""); err != nil {
		return ledger.JournalEntry{}, err
	}
	entry, err := s.app.GetEntry(ctx, query.CompanyID, query.EntryID)
	if err != nil {
		return ledger.JournalEntry{}, classifyUseCaseError(err)
	}
	return entry, nil
}
