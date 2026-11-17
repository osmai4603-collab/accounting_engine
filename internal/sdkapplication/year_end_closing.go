package sdkapplication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

var (
	ErrYearEndClosingUnavailable  = errors.New("atomic year-end closing is unavailable")
	ErrYearEndCloseEntriesMissing = errors.New("posted year-end closing entries are missing or unlinked")
	ErrYearEndOpeningPeriodClosed = errors.New("the generated opening entry cannot be reversed because its period is not open")
	ErrYearEndCloseReference      = errors.New("year-end close linkage is invalid")
)

type publicYearEndClosingService struct {
	*publicService
	repository reporting.Repository
	runner     storage.UnitOfWorkRunner
}

func NewPublicServiceWithYearEndClosing(
	app *Application,
	authorizer publicapplication.Authorizer,
	repository reporting.Repository,
	runner storage.UnitOfWorkRunner,
) publicapplication.Service {
	if app == nil || repository == nil || runner == nil {
		return NewPublicService(app, authorizer)
	}
	return &publicYearEndClosingService{
		publicService: &publicService{app: app, authorizer: authorizer},
		repository:    repository,
		runner:        runner,
	}
}

var _ publicapplication.YearEndClosingService = (*publicYearEndClosingService)(nil)

func (s *publicYearEndClosingService) ExecuteYearEndClosing(
	ctx context.Context,
	command publicapplication.ExecuteYearEndClosingCommand,
) (publicapplication.YearEndClosingResult, error) {
	if err := validateYearEndCommand(command); err != nil {
		return publicapplication.YearEndClosingResult{}, err
	}
	if err := s.authorize(ctx, command.Context, command.CompanyID, publicapplication.ActionClose, nil, ""); err != nil {
		return publicapplication.YearEndClosingResult{}, err
	}
	actorID, err := parseYearEndActor(command.Context.Principal)
	if err != nil {
		return publicapplication.YearEndClosingResult{}, err
	}
	companyID := uuid.UUID(command.CompanyID)
	fiscalYearID := uuid.UUID(command.FiscalYearID)
	nextYearID := uuid.UUID(command.NextFiscalYearID)
	journalID := uuid.UUID(command.JournalID)
	retainedEarningsID := uuid.UUID(command.RetainedEarningsAccountID)
	incomeSummaryID := uuid.UUID(command.IncomeSummaryAccountID)
	policy := internalClosingPolicy(command.Policy)
	options := reporting.ClosingOptions{
		Policy: policy, IncomeSummaryAccountID: incomeSummaryID,
	}
	currency := strings.ToUpper(strings.TrimSpace(command.Currency))
	reason := strings.TrimSpace(command.Reason)
	baseCtx := storage.WithCompanyContext(ctx, command.CompanyID)
	baseCtx = storage.WithActorContext(baseCtx, command.Context.Principal.ActorID)

	var result publicapplication.YearEndClosingResult
	err = storage.ExecuteWithRetry(baseCtx, s.runner,
		storage.TxOptions{Isolation: storage.IsolationSerializable},
		storage.DefaultRetryPolicy(),
		func(txCtx context.Context, uow storage.UnitOfWork) error {
			result = publicapplication.YearEndClosingResult{}
			txCtx = storage.WithCompanyContext(txCtx, command.CompanyID)
			txCtx = storage.WithActorContext(txCtx, command.Context.Principal.ActorID)

			year, err := s.repository.GetFiscalYearForCompanyContext(txCtx, companyID, fiscalYearID)
			if err != nil {
				return err
			}
			if nextYearID != uuid.Nil {
				nextYear, err := s.repository.GetFiscalYearForCompanyContext(txCtx, companyID, nextYearID)
				if err != nil {
					return err
				}
				if !year.EndDate.Add(time.Nanosecond).Equal(nextYear.StartDate) ||
					nextYear.State != masterdata.FiscalYearOpen {
					return reporting.ErrInvalidOpeningYear
				}
			}

			engine := reporting.NewClosingEngine(s.repository)
			entries, checklist, err := engine.CloseFiscalYearWithChecklistPolicy(
				txCtx, year, journalID, retainedEarningsID, actorID, currency, reason, options,
			)
			if err != nil {
				return err
			}
			offsetAccountID := retainedEarningsID
			if policy == reporting.ClosingPolicyIncomeSummary {
				offsetAccountID = incomeSummaryID
			}
			netIncome := closingNetIncome(entries, offsetAccountID)
			reference := yearEndCloseReference(year.ID, nextYearID)
			postedClosingIDs := make([]types.JournalEntryID, 0, len(entries))
			for _, entry := range entries {
				entry.Reference = reference
				entry.SourceModule = "year-end-closing"
				publicEntry, err := fromInternalEntry(entry)
				if err != nil {
					return err
				}
				draft, err := s.app.CreateDraftEntry(txCtx, publicEntry, command.Context.Principal.ActorID)
				if err != nil {
					return fmt.Errorf("create year-end closing draft: %w", err)
				}
				posted, err := s.app.PostEntryByID(txCtx, command.CompanyID, draft.ID, command.Context.Principal.ActorID)
				if err != nil {
					return fmt.Errorf("post year-end closing entry: %w", err)
				}
				postedClosingIDs = append(postedClosingIDs, posted.ID)
			}

			var openingID types.JournalEntryID
			if nextYearID != uuid.Nil {
				nextYear, err := s.repository.GetFiscalYearForCompanyContext(txCtx, companyID, nextYearID)
				if err != nil {
					return err
				}
				opening, err := engine.GenerateYearEndOpeningMoveContext(txCtx, year, nextYear, journalID, currency)
				if err != nil {
					return fmt.Errorf("generate year-end opening entry: %w", err)
				}
				if opening.ID != uuid.Nil {
					opening.Reference = reference
					opening.SourceModule = "year-end-closing"
					publicEntry, err := fromInternalEntry(opening)
					if err != nil {
						return err
					}
					draft, err := s.app.CreateDraftEntry(txCtx, publicEntry, command.Context.Principal.ActorID)
					if err != nil {
						return fmt.Errorf("create year-end opening draft: %w", err)
					}
					posted, err := s.app.PostEntryByID(txCtx, command.CompanyID, draft.ID, command.Context.Principal.ActorID)
					if err != nil {
						return fmt.Errorf("post year-end opening entry: %w", err)
					}
					openingID = posted.ID
				}
			}

			if len(postedClosingIDs) > 0 {
				if err := engine.ConfirmFiscalYearCloseContext(txCtx, year, uuid.UUID(postedClosingIDs[0])); err != nil {
					return fmt.Errorf("finalize fiscal-year close: %w", err)
				}
			} else if err := engine.ConfirmFiscalYearCloseWithoutEntriesContext(
				txCtx, year, journalID, retainedEarningsID, currency, options,
			); err != nil {
				return err
			}

			result = publicapplication.YearEndClosingResult{
				ClosingEntryIDs: postedClosingIDs,
				OpeningEntryID:  openingID,
				NetIncome:       netIncome.String(),
			}
			payload, err := json.Marshal(struct {
				Policy           publicapplication.YearEndClosingPolicy `json:"policy"`
				NextFiscalYearID types.FiscalYearID                     `json:"next_fiscal_year_id,omitempty"`
				ClosingEntryIDs  []types.JournalEntryID                 `json:"closing_entry_ids"`
				OpeningEntryID   types.JournalEntryID                   `json:"opening_entry_id,omitempty"`
				NetIncome        string                                 `json:"net_income"`
				Checklist        string                                 `json:"checklist"`
			}{
				Policy: command.Policy, NextFiscalYearID: command.NextFiscalYearID,
				ClosingEntryIDs: postedClosingIDs, OpeningEntryID: openingID,
				NetIncome: netIncome.String(), Checklist: checklist.Summary(),
			})
			if err != nil {
				return err
			}
			return uow.Audit().AppendAudit(txCtx, storage.AuditRecord{
				CompanyID: command.CompanyID, EntityType: "fiscal_year", EntityID: fiscalYearID.String(),
				Action: "YEAR_END_CLOSING_EXECUTED", ActorID: command.Context.Principal.ActorID,
				Reason: reason, CorrelationID: command.Context.CorrelationID, Payload: payload,
				OccurredAt: time.Now().UTC(),
			})
		})
	if err != nil {
		return publicapplication.YearEndClosingResult{}, classifyUseCaseError(err)
	}
	return result, nil
}

func (s *publicYearEndClosingService) ReopenYearEndClosing(
	ctx context.Context,
	command publicapplication.ReopenYearEndClosingCommand,
) (publicapplication.ReopenYearEndClosingResult, error) {
	if err := validateReopenYearEndCommand(command); err != nil {
		return publicapplication.ReopenYearEndClosingResult{}, err
	}
	if err := s.authorize(
		ctx, command.Context, command.CompanyID, publicapplication.ActionReopen, nil,
		command.ApprovalReference,
	); err != nil {
		return publicapplication.ReopenYearEndClosingResult{}, err
	}
	actorID, err := parseYearEndActor(command.Context.Principal)
	if err != nil {
		return publicapplication.ReopenYearEndClosingResult{}, err
	}
	approvalID, err := uuid.Parse(strings.TrimSpace(command.ApprovalReference))
	if err != nil || approvalID == uuid.Nil {
		return publicapplication.ReopenYearEndClosingResult{}, publicapplication.NewError(
			publicapplication.ErrValidation, "a valid approval UUID is required to reopen the annual close", err,
		)
	}
	companyID := uuid.UUID(command.CompanyID)
	fiscalYearID := uuid.UUID(command.FiscalYearID)
	reason := strings.TrimSpace(command.Reason)
	baseCtx := storage.WithCompanyContext(ctx, command.CompanyID)
	baseCtx = storage.WithActorContext(baseCtx, command.Context.Principal.ActorID)

	var result publicapplication.ReopenYearEndClosingResult
	err = storage.ExecuteWithRetry(baseCtx, s.runner,
		storage.TxOptions{Isolation: storage.IsolationSerializable},
		storage.DefaultRetryPolicy(),
		func(txCtx context.Context, uow storage.UnitOfWork) error {
			result = publicapplication.ReopenYearEndClosingResult{}
			txCtx = storage.WithCompanyContext(txCtx, command.CompanyID)
			txCtx = storage.WithActorContext(txCtx, command.Context.Principal.ActorID)
			year, err := s.repository.GetFiscalYearForCompanyContext(txCtx, companyID, fiscalYearID)
			if err != nil {
				return err
			}
			if year.State == masterdata.FiscalYearOpen {
				return masterdata.ErrInvalidFiscalYearTransition
			}
			period13, ok := reporting.FindFiscalPeriod(year, 13)
			if !ok {
				return reporting.ErrClosingAdjustmentPeriodUnavailable
			}
			reversals, err := s.entriesToReverseForYearEnd(txCtx, year, command.NextFiscalYearID)
			if err != nil {
				return err
			}

			fiscalYearReopener, ok := s.repository.(reporting.FiscalYearReopenRepository)
			if !ok {
				return reporting.ErrFiscalYearReopenRepositoryRequired
			}
			if err := fiscalYearReopener.ReopenFiscalYearForCompanyContext(
				txCtx, companyID, fiscalYearID, actorID, reason,
			); err != nil {
				return err
			}
			periodReopener, ok := s.repository.(reporting.PeriodReopenWithApprovalRepository)
			if !ok {
				return reporting.ErrPeriodApprovalRepositoryRequired
			}
			if err := periodReopener.ReopenPeriodWithApprovalContext(
				txCtx, companyID, period13.ID, actorID, approvalID, reason,
			); err != nil {
				return err
			}
			if period14, exists := reporting.FindFiscalPeriod(year, 14); exists &&
				period14.State != masterdata.PeriodOpen {
				if err := periodReopener.ReopenPeriodWithApprovalContext(
					txCtx, companyID, period14.ID, actorID, approvalID, reason,
				); err != nil {
					return err
				}
			}

			reversalIDs := make([]types.JournalEntryID, 0, len(reversals))
			for _, original := range reversals {
				var reversed ledger.JournalEntry
				if original.Classification == posting.EntryClassClosing {
					reversed, _, err = s.app.ReverseYearEndCloseEntryByIDWithMetadata(
						txCtx, command.CompanyID, types.JournalEntryID(original.ID),
						command.Context.Principal.ActorID, reason, year.EndDate,
						"year-end-reopen:"+original.ID.String(),
						"YEAR-END REOPEN "+fiscalYearID.String(), "year-end-closing",
					)
				} else {
					reversed, _, err = s.app.ReverseEntryByIDWithMetadata(
						txCtx, command.CompanyID, types.JournalEntryID(original.ID),
						command.Context.Principal.ActorID, reason, original.EntryDate,
						"year-end-reopen:"+original.ID.String(),
						"YEAR-END REOPEN "+fiscalYearID.String(), "year-end-closing",
					)
				}
				if err != nil {
					return err
				}
				reversalIDs = append(reversalIDs, reversed.ID)
			}

			result = publicapplication.ReopenYearEndClosingResult{ReversalEntryIDs: reversalIDs}
			payload, err := json.Marshal(struct {
				ApprovalReference string                 `json:"approval_reference"`
				ReversalEntryIDs  []types.JournalEntryID `json:"reversal_entry_ids"`
			}{ApprovalReference: command.ApprovalReference, ReversalEntryIDs: reversalIDs})
			if err != nil {
				return err
			}
			return uow.Audit().AppendAudit(txCtx, storage.AuditRecord{
				CompanyID: command.CompanyID, EntityType: "fiscal_year", EntityID: fiscalYearID.String(),
				Action: "YEAR_END_CLOSING_REOPENED", ActorID: command.Context.Principal.ActorID,
				Reason: reason, CorrelationID: command.Context.CorrelationID, Payload: payload,
				OccurredAt: time.Now().UTC(),
			})
		})
	if err != nil {
		return publicapplication.ReopenYearEndClosingResult{}, classifyUseCaseError(err)
	}
	return result, nil
}

func (s *publicYearEndClosingService) entriesToReverseForYearEnd(
	ctx context.Context,
	year masterdata.FiscalYear,
	requestedNextYearID types.FiscalYearID,
) ([]posting.JournalEntry, error) {
	yearEndEntries, err := s.repository.ListEntriesForCompanyContext(
		ctx, year.CompanyID, year.StartDate, year.EndDate,
	)
	if err != nil {
		return nil, err
	}
	period13, _ := reporting.FindFiscalPeriod(year, 13)
	closeEntries := make([]posting.JournalEntry, 0, 2)
	var closeReference string
	for _, entry := range yearEndEntries {
		if entry.Status != posting.EntryPosted || entry.Classification != posting.EntryClassClosing ||
			entry.TargetPeriodNumber != 13 || entry.PeriodID != period13.ID {
			continue
		}
		switch entry.DocumentReference {
		case "YEAR-END CLOSING", "YEAR-END CLOSING - INCOME SUMMARY",
			"YEAR-END CLOSING - INCOME SUMMARY TRANSFER":
		default:
			return nil, ErrYearEndCloseEntriesMissing
		}
		if !validYearEndCloseReference(entry.Reference, year.ID) {
			return nil, ErrYearEndCloseReference
		}
		if closeReference != "" && entry.Reference != closeReference {
			return nil, ErrYearEndCloseReference
		}
		closeReference = entry.Reference
		closeEntries = append(closeEntries, entry)
	}
	if len(closeEntries) > 2 {
		return nil, ErrYearEndCloseEntriesMissing
	}

	if closeReference == "" {
		futureEntries, err := s.repository.ListEntriesForCompanyContext(
			ctx, year.CompanyID, year.EndDate.Add(time.Nanosecond), time.Time{},
		)
		if err != nil {
			return nil, err
		}
		for _, entry := range futureEntries {
			if entry.Status != posting.EntryPosted || entry.Classification != posting.EntryClassOpening ||
				entry.TargetPeriodNumber != 1 || entry.DocumentReference != "YEAR-OPENING BALANCES" ||
				!strings.HasPrefix(entry.Reference, "YEAR-END-CLOSE:"+year.ID.String()+":") {
				continue
			}
			if !validYearEndCloseReference(entry.Reference, year.ID) ||
				(closeReference != "" && entry.Reference != closeReference) {
				return nil, ErrYearEndCloseReference
			}
			closeReference = entry.Reference
		}
	}

	openingEntries := make([]posting.JournalEntry, 0, 1)
	if closeReference != "" {
		nextYearID, hasNextYear, err := nextYearFromCloseReference(closeReference)
		if err != nil {
			return nil, err
		}
		if requestedNextYearID != (types.FiscalYearID{}) &&
			(!hasNextYear || uuid.UUID(requestedNextYearID) != nextYearID) {
			return nil, ErrYearEndCloseReference
		}
		if hasNextYear {
			nextYear, err := s.repository.GetFiscalYearForCompanyContext(ctx, year.CompanyID, nextYearID)
			if err != nil {
				return nil, err
			}
			if !year.EndDate.Add(time.Nanosecond).Equal(nextYear.StartDate) {
				return nil, ErrYearEndCloseReference
			}
			nextYearEntries, err := s.repository.ListEntriesForCompanyContext(
				ctx, year.CompanyID, nextYear.StartDate, nextYear.EndDate,
			)
			if err != nil {
				return nil, err
			}
			firstPeriod, ok := reporting.FindFiscalPeriod(nextYear, 1)
			if !ok {
				return nil, reporting.ErrInvalidOpeningYear
			}
			for _, entry := range nextYearEntries {
				if entry.Status == posting.EntryPosted && entry.Classification == posting.EntryClassOpening &&
					entry.TargetPeriodNumber == 1 && entry.PeriodID == firstPeriod.ID &&
					entry.DocumentReference == "YEAR-OPENING BALANCES" && entry.Reference == closeReference {
					openingEntries = append(openingEntries, entry)
				}
			}
			if len(openingEntries) > 1 {
				return nil, ErrYearEndCloseReference
			}
			if len(openingEntries) == 1 &&
				(nextYear.State != masterdata.FiscalYearOpen || firstPeriod.State != masterdata.PeriodOpen) {
				return nil, ErrYearEndOpeningPeriodClosed
			}
		}
	}

	sort.Slice(closeEntries, func(i, j int) bool {
		return closeEntries[i].SequenceNumber > closeEntries[j].SequenceNumber
	})
	result := make([]posting.JournalEntry, 0, len(openingEntries)+len(closeEntries))
	result = append(result, openingEntries...)
	result = append(result, closeEntries...)
	return result, nil
}

func validateYearEndCommand(command publicapplication.ExecuteYearEndClosingCommand) error {
	if command.CompanyID == (types.CompanyID{}) || command.FiscalYearID == (types.FiscalYearID{}) ||
		command.JournalID == (types.JournalID{}) || command.RetainedEarningsAccountID == (types.AccountID{}) {
		return publicapplication.NewError(publicapplication.ErrValidation, "company, year, journal, and retained earnings account are required", nil)
	}
	if strings.TrimSpace(command.Currency) == "" {
		return publicapplication.NewError(publicapplication.ErrValidation, "functional currency is required", nil)
	}
	if err := validateYearEndReason(command.Reason); err != nil {
		return publicapplication.NewError(publicapplication.ErrValidation, "a valid year-end closing reason is required", err)
	}
	switch command.Policy {
	case "", publicapplication.YearEndClosingDirectToRetainedEarnings:
	case publicapplication.YearEndClosingThroughIncomeSummary:
		if command.IncomeSummaryAccountID == (types.AccountID{}) ||
			command.IncomeSummaryAccountID == command.RetainedEarningsAccountID {
			return publicapplication.NewError(publicapplication.ErrValidation, "income-summary account is required and must differ from retained earnings", nil)
		}
	default:
		return publicapplication.NewError(publicapplication.ErrValidation, "unknown year-end closing policy", nil)
	}
	return nil
}

func validateReopenYearEndCommand(command publicapplication.ReopenYearEndClosingCommand) error {
	if command.CompanyID == (types.CompanyID{}) || command.FiscalYearID == (types.FiscalYearID{}) {
		return publicapplication.NewError(publicapplication.ErrValidation, "company and fiscal year are required", nil)
	}
	if strings.TrimSpace(command.ApprovalReference) == "" {
		return publicapplication.NewError(publicapplication.ErrValidation, "reopening the annual close requires an approval reference", nil)
	}
	if err := validateYearEndReason(command.Reason); err != nil {
		return publicapplication.NewError(publicapplication.ErrValidation, "a valid year-end reopening reason is required", err)
	}
	return nil
}

func validateYearEndReason(reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 500 {
		return ErrReasonInvalid
	}
	for _, character := range reason {
		if unicode.IsControl(character) {
			return ErrReasonInvalid
		}
	}
	return nil
}

func parseYearEndActor(principal publicapplication.Principal) (uuid.UUID, error) {
	actorID, err := uuid.Parse(strings.TrimSpace(principal.ActorID))
	if err != nil || actorID == uuid.Nil {
		return uuid.Nil, publicapplication.NewError(
			publicapplication.ErrValidation, "year-end closing requires an actor UUID", err,
		)
	}
	return actorID, nil
}

func internalClosingPolicy(policy publicapplication.YearEndClosingPolicy) reporting.ClosingPolicy {
	switch policy {
	case publicapplication.YearEndClosingThroughIncomeSummary:
		return reporting.ClosingPolicyIncomeSummary
	default:
		return reporting.ClosingPolicyDirectToRetainedEarnings
	}
}

func closingNetIncome(entries []posting.JournalEntry, offsetAccountID uuid.UUID) decimal.Decimal {
	netIncome := decimal.Zero
	if len(entries) == 0 {
		return netIncome
	}
	for _, line := range entries[0].Lines {
		if line.AccountID != offsetAccountID {
			continue
		}
		debit, credit, _ := posting.LineFunctionalAmounts(line)
		netIncome = netIncome.Add(credit.Sub(debit))
	}
	return netIncome
}

func yearEndCloseReference(yearID, nextYearID uuid.UUID) string {
	nextReference := "none"
	if nextYearID != uuid.Nil {
		nextReference = nextYearID.String()
	}
	return fmt.Sprintf("YEAR-END-CLOSE:%s:%s", yearID, nextReference)
}

func validYearEndCloseReference(reference string, yearID uuid.UUID) bool {
	nextYearID, ok, err := nextYearFromCloseReference(reference)
	return err == nil && strings.HasPrefix(reference, "YEAR-END-CLOSE:"+yearID.String()+":") &&
		(ok && nextYearID != uuid.Nil || !ok)
}

func nextYearFromCloseReference(reference string) (uuid.UUID, bool, error) {
	parts := strings.Split(reference, ":")
	if len(parts) != 3 || parts[0] != "YEAR-END-CLOSE" {
		return uuid.Nil, false, ErrYearEndCloseReference
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return uuid.Nil, false, ErrYearEndCloseReference
	}
	if parts[2] == "none" {
		return uuid.Nil, false, nil
	}
	nextYearID, err := uuid.Parse(parts[2])
	if err != nil || nextYearID == uuid.Nil {
		return uuid.Nil, false, ErrYearEndCloseReference
	}
	return nextYearID, true, nil
}
