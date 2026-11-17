package standards

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/shopspring/decimal"
)

var (
	ErrPostingServiceRequired = errors.New("standards: application posting service is required")
	ErrPrincipalRequired      = errors.New("standards: authorized principal is required in context")
	ErrUnauthorizedCompany    = errors.New("standards: principal is not authorized for this company")
	ErrStandardRefRequired    = errors.New("standards: standard reference is required")
	ErrEntityIDRequired       = errors.New("standards: entity reference is required")
	ErrIdempotencyKeyRequired = errors.New("standards: idempotency key is required")
	ErrInvalidCompanyID       = errors.New("standards: valid company ID is required")
	ErrInvalidJournalID       = errors.New("standards: valid journal ID is required")
	ErrInvalidEffectiveDate   = errors.New("standards: effective date is required")
	ErrLinesRequired          = errors.New("standards: at least two balanced lines are required")
	ErrUnbalancedLines        = errors.New("standards: entry lines must be balanced (debit equals credit)")
	ErrCurrencyMismatch       = errors.New("standards: entry line currencies do not match")
	ErrAccountNotFound        = errors.New("standards: account not found in chart of accounts")
)

// StandardPostingRequest carries all metadata, audit trail identifiers,
// and balanced entry lines required to post a standard-derived transaction.
type StandardPostingRequest struct {
	EntryID                     types.JournalEntryID       // Optional deterministic entry ID; derived if empty
	StandardRef                 string                     // e.g. "IFRS-15-REV-REC", "IFRS-9-ECL", "IFRS-16-LEASE", "IAS-12-TAX"
	EntityID                    string                     // Original entity ID (ContractID, LeaseID, InstrumentID, etc.)
	IdempotencyKey              string                     // Strict idempotency key
	CompanyID                   types.CompanyID            // Target company
	JournalID                   types.JournalID            // Destination journal
	FiscalPeriodID              types.FiscalPeriodID       // Optional fiscal period ID
	EffectiveDate               time.Time                  // Accounting effective date
	PostingDate                 time.Time                  // Posting date (defaults to EffectiveDate if zero)
	Description                 string                     // Human-readable description
	Lines                       []ledger.EntryLine         // Real balanced entry lines
	Classification              ledger.EntryClassification // Regular or Adjusting
	TargetPeriodNumber          int
	AdjustingEntryType          ledger.AdjustingEntryType
	AdjustmentReason            string
	AdjustmentApprovalReference string
}

// StandardPostingCapability is the public interface allowing any standard module
// to submit balanced journal entries through the official application layer.
type StandardPostingCapability interface {
	PostStandardEntry(ctx context.Context, req StandardPostingRequest) (publicapplication.EntryResult, error)
}

type defaultStandardPoster struct {
	app        publicapplication.Service
	masterData MasterDataReader
}

// NewStandardPostingCapability constructs a StandardPostingCapability from ModulePorts.
func NewStandardPostingCapability(ports ModulePorts) StandardPostingCapability {
	if ports == nil {
		return &defaultStandardPoster{}
	}
	return &defaultStandardPoster{
		app:        ports.Application(),
		masterData: ports.MasterData(),
	}
}

// NewStandardPoster constructs a StandardPostingCapability directly from application and master data ports.
func NewStandardPoster(app publicapplication.Service, masterData MasterDataReader) StandardPostingCapability {
	return &defaultStandardPoster{
		app:        app,
		masterData: masterData,
	}
}

func deriveSourceModule(standardRef string) string {
	ref := strings.ToLower(strings.TrimSpace(standardRef))
	parts := strings.Split(ref, "-")
	if len(parts) >= 2 {
		if parts[0] == "ifrs" || parts[0] == "ias" {
			return parts[0] + parts[1]
		}
	}
	return parts[0]
}

func (p *defaultStandardPoster) PostStandardEntry(
	ctx context.Context,
	req StandardPostingRequest,
) (publicapplication.EntryResult, error) {
	if p == nil || p.app == nil {
		return publicapplication.EntryResult{}, ErrPostingServiceRequired
	}
	if ctx == nil {
		return publicapplication.EntryResult{}, ErrPrincipalRequired
	}
	if err := ctx.Err(); err != nil {
		return publicapplication.EntryResult{}, err
	}

	standardRef := strings.TrimSpace(req.StandardRef)
	if standardRef == "" {
		return publicapplication.EntryResult{}, ErrStandardRefRequired
	}
	entityID := strings.TrimSpace(req.EntityID)
	if entityID == "" {
		return publicapplication.EntryResult{}, ErrEntityIDRequired
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return publicapplication.EntryResult{}, ErrIdempotencyKeyRequired
	}
	if req.CompanyID == (types.CompanyID{}) {
		return publicapplication.EntryResult{}, ErrInvalidCompanyID
	}
	if req.JournalID == (types.JournalID{}) {
		return publicapplication.EntryResult{}, ErrInvalidJournalID
	}
	if req.EffectiveDate.IsZero() {
		return publicapplication.EntryResult{}, ErrInvalidEffectiveDate
	}
	if len(req.Lines) < 2 {
		return publicapplication.EntryResult{}, ErrLinesRequired
	}

	// Double-entry balancing & currency consistency
	var currency string
	totalDebit := decimal.Zero
	totalCredit := decimal.Zero
	for i, line := range req.Lines {
		if line.AccountID == (types.AccountID{}) {
			return publicapplication.EntryResult{}, fmt.Errorf("%w: line %d missing account ID", ErrLinesRequired, i)
		}

		lineCurrency := line.TransactionCurrency
		if lineCurrency == "" {
			if line.TransactionAmount.Currency() != "" {
				lineCurrency = line.TransactionAmount.Currency()
			} else if line.FunctionalDebit.Currency() != "" {
				lineCurrency = line.FunctionalDebit.Currency()
			} else {
				lineCurrency = line.FunctionalCredit.Currency()
			}
		}
		if lineCurrency == "" {
			return publicapplication.EntryResult{}, ErrCurrencyMismatch
		}
		if currency == "" {
			currency = lineCurrency
		} else if !strings.EqualFold(currency, lineCurrency) {
			return publicapplication.EntryResult{}, ErrCurrencyMismatch
		}

		totalDebit = totalDebit.Add(line.FunctionalDebit.Amount())
		totalCredit = totalCredit.Add(line.FunctionalCredit.Amount())

		// Verify account existence in real Chart of Accounts if MasterData port is available
		if p.masterData != nil {
			if _, err := p.masterData.GetAccount(ctx, req.CompanyID, line.AccountID); err != nil {
				return publicapplication.EntryResult{}, fmt.Errorf("%w: account %s for company %s: %v",
					ErrAccountNotFound, line.AccountID, req.CompanyID, err)
			}
		}
	}

	if !totalDebit.Equal(totalCredit) || totalDebit.IsZero() {
		return publicapplication.EntryResult{}, ErrUnbalancedLines
	}

	// Context Principal validation
	principal, ok := publicapplication.PrincipalFromContext(ctx)
	if !ok || strings.TrimSpace(principal.ActorID) == "" {
		return publicapplication.EntryResult{}, ErrPrincipalRequired
	}
	companyAuthorized := false
	for _, cid := range principal.CompanyIDs {
		if cid == req.CompanyID {
			companyAuthorized = true
			break
		}
	}
	if !companyAuthorized {
		return publicapplication.EntryResult{}, ErrUnauthorizedCompany
	}

	// Prepare CreateDraftCommand
	metadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	commandContext := publicapplication.Context{
		Principal:      principal,
		CorrelationID:  metadata.CorrelationID,
		IdempotencyKey: idempotencyKey,
		SourceSystem:   strings.ToLower(standardRef),
	}

	classification := req.Classification
	if classification == "" {
		classification = ledger.EntryClassRegular
	}

	postingDate := req.PostingDate
	if postingDate.IsZero() {
		postingDate = req.EffectiveDate
	}

	desc := strings.TrimSpace(req.Description)
	if !strings.Contains(desc, standardRef) {
		if desc == "" {
			desc = fmt.Sprintf("[%s] %s", standardRef, entityID)
		} else {
			desc = fmt.Sprintf("[%s] %s", standardRef, desc)
		}
	}

	sourceModule := deriveSourceModule(standardRef)

	draftCmd := publicapplication.CreateDraftCommand{
		Context:                     commandContext,
		CompanyID:                   req.CompanyID,
		JournalID:                   req.JournalID,
		FiscalPeriodID:              req.FiscalPeriodID,
		EffectiveDate:               req.EffectiveDate,
		PostingDate:                 postingDate,
		Description:                 desc,
		SourceModule:                sourceModule,
		ReferenceID:                 entityID,
		TransactionCurrency:         currency,
		ExchangeRate:                "1",
		Classification:              classification,
		TargetPeriodNumber:          req.TargetPeriodNumber,
		AdjustingEntryType:          req.AdjustingEntryType,
		AdjustmentReason:            req.AdjustmentReason,
		AdjustmentApprovalReference: req.AdjustmentApprovalReference,
		Lines:                       req.Lines,
	}

	deterministicEntryID := req.EntryID
	if deterministicEntryID == (types.JournalEntryID{}) || uuid.UUID(deterministicEntryID) == uuid.Nil {
		entryUUID := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "%s:%s:%s", req.CompanyID, standardRef, idempotencyKey))
		deterministicEntryID = types.JournalEntryID(entryUUID)
	}

	// Check if this standard entry was already created/posted (idempotency check)
	existing, err := p.app.GetEntry(ctx, publicapplication.GetEntryQuery{
		Context:   commandContext,
		CompanyID: req.CompanyID,
		EntryID:   deterministicEntryID,
	})
	if err == nil && existing.ID == deterministicEntryID {
		if existing.Status == ledger.EntryPosted {
			return publicapplication.EntryResult{
				Entry:            existing,
				IdempotentReplay: true,
			}, nil
		}
		if existing.Status == ledger.EntryDraft {
			// Complete pending posting
			postCmd := publicapplication.PostEntryCommand{
				Context:   commandContext,
				CompanyID: req.CompanyID,
				EntryID:   deterministicEntryID,
			}
			return p.app.Post(ctx, postCmd)
		}
	}

	var created publicapplication.EntryResult
	if creator, ok := p.app.(interface {
		CreateDraftWithID(context.Context, publicapplication.CreateDraftCommand, types.JournalEntryID) (publicapplication.EntryResult, error)
	}); ok {
		created, err = creator.CreateDraftWithID(ctx, draftCmd, deterministicEntryID)
	} else {
		created, err = p.app.CreateDraft(ctx, draftCmd)
	}
	if err != nil {
		return publicapplication.EntryResult{}, fmt.Errorf("standard posting: create draft: %w", err)
	}

	// If draft was already posted via idempotent replay, return it directly
	if created.Entry.Status == ledger.EntryPosted {
		return created, nil
	}

	postCmd := publicapplication.PostEntryCommand{
		Context:   commandContext,
		CompanyID: req.CompanyID,
		EntryID:   created.Entry.ID,
	}
	posted, err := p.app.Post(ctx, postCmd)
	if err != nil {
		return publicapplication.EntryResult{}, fmt.Errorf("standard posting: post entry: %w", err)
	}

	return posted, nil
}
