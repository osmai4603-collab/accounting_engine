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

func NewPublicService(app *Application, authorizer publicapplication.Authorizer) publicapplication.Service {
	if app == nil {
		return nil
	}
	return &publicService{app: app, authorizer: authorizer}
}

var _ publicapplication.Service = (*publicService)(nil)

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
			Principal: principal,
			CompanyID: companyID,
			Action:    action,
			EntryID:   entryID,
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
		CompanyID:           command.CompanyID,
		JournalID:           command.JournalID,
		FiscalPeriodID:      command.FiscalPeriodID,
		EffectiveDate:       command.EffectiveDate,
		PostingDate:         command.PostingDate,
		Description:         command.Description,
		SourceModule:        command.SourceModule,
		ReferenceID:         command.ReferenceID,
		TransactionCurrency: command.TransactionCurrency,
		Classification:      command.Classification,
		Lines:               command.Lines,
		CreatedBy:           principalActorID(command.Context.Principal),
		CreatedAt:           now,
		UpdatedAt:           now,
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
	return s.createDraft(ctx, command, types.JournalEntryID{})
}

func (s *publicService) CreateDraftWithID(
	ctx context.Context,
	command publicapplication.CreateDraftCommand,
	entryID types.JournalEntryID,
) (publicapplication.EntryResult, error) {
	return s.createDraft(ctx, command, entryID)
}

func (s *publicService) createDraft(
	ctx context.Context,
	command publicapplication.CreateDraftCommand,
	entryID types.JournalEntryID,
) (publicapplication.EntryResult, error) {
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionCreate, nil); err != nil {
		return publicapplication.EntryResult{}, err
	}
	entry, err := toEntryFromCreateCommand(command)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}
	entry.ID = entryID
	created, err := s.app.CreateDraftEntry(ctx, entry, principalActorID(command.Context.Principal))
	if err != nil {
		return publicapplication.EntryResult{}, classifyUseCaseError(err)
	}
	return publicapplication.EntryResult{Entry: created}, nil
}

func (s *publicService) Validate(ctx context.Context, command publicapplication.ValidateEntryCommand) error {
	entryID := command.EntryID
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionValidate, &entryID); err != nil {
		return err
	}
	return classifyUseCaseError(s.app.ValidateEntryByID(ctx, command.CompanyID, command.EntryID))
}

func (s *publicService) Post(ctx context.Context, command publicapplication.PostEntryCommand) (publicapplication.EntryResult, error) {
	entryID := command.EntryID
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionPost, &entryID); err != nil {
		return publicapplication.EntryResult{}, err
	}
	posted, err := s.app.PostEntryByID(ctx, command.CompanyID, command.EntryID, principalActorID(command.Context.Principal))
	if err != nil {
		return publicapplication.EntryResult{}, classifyUseCaseError(err)
	}
	return publicapplication.EntryResult{Entry: posted}, nil
}

func (s *publicService) Reverse(ctx context.Context, command publicapplication.ReverseEntryCommand) (publicapplication.EntryResult, error) {
	entryID := command.EntryID
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionReverse, &entryID); err != nil {
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
	if err := s.authorize(ctx, query.Context, query.CompanyID, publicapplication.ActionRead, &entryID); err != nil {
		return ledger.JournalEntry{}, err
	}
	entry, err := s.app.GetEntry(ctx, query.CompanyID, query.EntryID)
	if err != nil {
		return ledger.JournalEntry{}, classifyUseCaseError(err)
	}
	return entry, nil
}
