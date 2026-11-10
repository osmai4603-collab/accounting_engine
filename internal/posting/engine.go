package posting

import (
	"context"
	"errors"
	"sync"
	"time"

	"accounting_engine/internal/masterdata"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrEntryNotFound                         = errors.New("journal entry not found")
	ErrInvalidEntryState                     = errors.New("invalid journal entry state")
	ErrApprovalRequired                      = errors.New("journal entry requires completed approvals")
	ErrAtomicPostingOpenLineRequired         = errors.New("atomic journal posting with an open line is required")
	ErrAdjustmentPeriodAuthorizationRequired = errors.New("adjustment period requires explicit authorization")
	ErrFunctionalCurrencyMismatch            = errors.New("journal entry functional currency does not match company")
)

var (
	ErrMoveAlreadyPosted      = financialtypes.ErrMoveAlreadyPosted
	ErrCannotModifyPostedMove = financialtypes.ErrCannotModifyPostedMove
	ErrMakerCheckerViolation  = financialtypes.ErrMakerCheckerViolation
)

type Engine struct {
	repo       Repository
	masterdata *masterdata.Service
	mu         sync.Mutex

	MakerCheckerThreshold decimal.Decimal
	dimensions            *FinancialDimensionsEngine
	approvalGate          ApprovalGate
	makerCheckerPolicy    MakerCheckerPolicy
}

// MakerCheckerPolicy exposes a company-level maker-checker threshold. The
// boolean reports whether the company has configured a policy; an unconfigured
// policy leaves the engine's explicit MakerCheckerThreshold in effect.
type MakerCheckerPolicy interface {
	MakerCheckerThreshold(ctx context.Context, companyID uuid.UUID) (decimal.Decimal, bool, error)
}

func NewEngine(repo Repository, masterData *masterdata.Service) *Engine {
	return &Engine{repo: repo, masterdata: masterData}
}

func (e *Engine) SetFinancialDimensionsEngine(engine *FinancialDimensionsEngine) {
	e.dimensions = engine
}

func (e *Engine) SetApprovalGate(gate ApprovalGate) {
	e.approvalGate = gate
}

func (e *Engine) SetMakerCheckerPolicy(policy MakerCheckerPolicy) {
	e.makerCheckerPolicy = policy
}

func (e *Engine) CreateDraft(entry JournalEntry) error {
	return e.CreateDraftContext(context.Background(), entry)
}

func (e *Engine) CreateDraftContext(ctx context.Context, entry JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	entry.Status = EntryDraft
	if entry.CompanyID != uuid.Nil {
		for i := range entry.Lines {
			if entry.Lines[i].CompanyID == uuid.Nil {
				entry.Lines[i].CompanyID = entry.CompanyID
			}
		}
	}
	if entry.EntryDate.IsZero() {
		entry.EntryDate = entry.Date
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.UpdatedAt = entry.CreatedAt
	if err := entry.Validate(); err != nil {
		return err
	}
	return e.repo.SaveDraftContext(ctx, entry)
}

func (e *Engine) SaveDraft(entry JournalEntry) error {
	return e.CreateDraft(entry)
}

func (e *Engine) Post(companyID, entryID uuid.UUID, posterID string) error {
	return e.PostContext(context.Background(), companyID, entryID, posterID)
}

func (e *Engine) PostContext(ctx context.Context, companyID, entryID uuid.UUID, posterID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, err := e.repo.GetEntryByIDForCompany(ctx, companyID, entryID)
	if err != nil {
		return err
	}
	return e.PostEntryContext(ctx, &entry, posterID)
}

func (e *Engine) PostForCompany(ctx context.Context, companyID, entryID uuid.UUID, posterID string) error {
	return e.PostContext(ctx, companyID, entryID, posterID)
}

func (e *Engine) PostEntry(entry *JournalEntry, posterID string) error {
	return e.PostEntryContext(context.Background(), entry, posterID)
}

func (e *Engine) PostEntryContext(ctx context.Context, entry *JournalEntry, posterID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.Status != EntryDraft {
		if entry.Status == EntryPosted {
			return financialtypes.ErrMoveAlreadyPosted
		}
		return ErrEntryNotDraft
	}
	if err := e.prepareForPosting(ctx, entry, posterID); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now().UTC()
	entry.PostedBy = posterID
	entry.PostedAt = &now
	entry.Status = EntryPosted
	entry.UpdatedAt = now
	if repository, ok := e.repo.(ContextAtomicPostingRepository); ok {
		posted, err := repository.PostAndSequenceContext(ctx, *entry)
		if err != nil {
			entry.Status = EntryDraft
			entry.PostedBy = ""
			entry.PostedAt = nil
			return err
		}
		*entry = posted
		return nil
	}
	sequence, err := e.repo.GetNextSequenceForCompanyContext(ctx, entry.CompanyID, entry.JournalID)
	if err != nil {
		entry.Status = EntryDraft
		entry.PostedBy = ""
		entry.PostedAt = nil
		return err
	}
	entry.SequenceNumber = sequence
	if err := e.repo.PostAtomicContext(ctx, *entry); err != nil {
		entry.Status = EntryDraft
		entry.PostedBy = ""
		entry.PostedAt = nil
		entry.SequenceNumber = 0
		return err
	}
	return nil
}

func (e *Engine) PostEntryWithAttachment(entry *JournalEntry, posterID string, attachments ...LedgerAttachment) error {
	return e.PostEntryWithAttachmentContext(context.Background(), entry, posterID, attachments...)
}

func (e *Engine) PostEntryWithAttachmentContext(ctx context.Context, entry *JournalEntry, posterID string, attachments ...LedgerAttachment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.Status != EntryDraft {
		if entry.Status == EntryPosted {
			return financialtypes.ErrMoveAlreadyPosted
		}
		return ErrEntryNotDraft
	}
	for _, att := range attachments {
		if att != nil {
			if err := att.ValidateAttachment(ctx, *entry); err != nil {
				return err
			}
		}
	}
	if err := e.prepareForPosting(ctx, entry, posterID); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	original := *entry
	now := time.Now().UTC()
	entry.PostedBy = posterID
	entry.PostedAt = &now
	entry.Status = EntryPosted
	entry.UpdatedAt = now

	if repo, ok := e.repo.(ContextAtomicAttachmentRepository); ok {
		if err := repo.PostWithAttachmentsContext(ctx, entry, attachments); err != nil {
			*entry = original
			return err
		}
		return nil
	}
	if repo, ok := e.repo.(AtomicAttachmentRepository); ok {
		if err := repo.PostWithAttachments(ctx, entry, attachments); err != nil {
			*entry = original
			return err
		}
		return nil
	}

	*entry = original
	return ErrAtomicPostingOpenLineRequired
}

func (e *Engine) PostEntryWithOpenLine(entry *JournalEntry, line OpenLineAttachment, posterID string) error {
	return e.PostEntryWithOpenLineContext(context.Background(), entry, line, posterID)
}

func (e *Engine) PostEntryWithOpenLineContext(ctx context.Context, entry *JournalEntry, line OpenLineAttachment, posterID string) error {
	attachment := &OpenLineLedgerAttachment{Attachment: line}
	err := e.PostEntryWithAttachmentContext(ctx, entry, posterID, attachment)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrAtomicPostingOpenLineRequired) {
		return err
	}

	// Fallback to legacy ContextAtomicPostingWithOpenLineRepository if repo does not implement AtomicAttachmentRepository
	if entry.Status != EntryDraft {
		if entry.Status == EntryPosted {
			return financialtypes.ErrMoveAlreadyPosted
		}
		return ErrEntryNotDraft
	}
	if entry.CompanyID == uuid.Nil || line.ID == uuid.Nil || line.CompanyID != entry.CompanyID ||
		line.JournalEntryID != entry.ID ||
		line.PartnerID == uuid.Nil || line.ControlAccountID == uuid.Nil ||
		(line.Kind != "receivable" && line.Kind != "payable") ||
		line.Amount.Currency() == "" || line.Amount.IsNegative() || line.Amount.Amount().IsZero() {
		return ErrInvalidEntry
	}
	repository, ok := e.repo.(ContextAtomicPostingWithOpenLineRepository)
	if !ok {
		return ErrAtomicPostingOpenLineRequired
	}
	if err := e.prepareForPosting(ctx, entry, posterID); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	original := *entry
	now := time.Now().UTC()
	entry.PostedBy = posterID
	entry.PostedAt = &now
	entry.Status = EntryPosted
	entry.UpdatedAt = now
	posted, err := repository.PostAndSequenceWithOpenLineContext(ctx, *entry, line)
	if err != nil {
		*entry = original
		return err
	}
	*entry = posted
	return nil
}

func (e *Engine) prepareForPosting(ctx context.Context, entry *JournalEntry, posterID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	if e.dimensions != nil {
		if err := e.dimensions.ValidateEntry(ctx, *entry); err != nil {
			return err
		}
	}
	if e.approvalGate != nil {
		approved, err := e.approvalGate.IsApproved(ctx, entry.CompanyID, entry.ID)
		if err != nil {
			return err
		}
		if !approved {
			return ErrApprovalRequired
		}
	}
	if e.masterdata != nil {
		if entry.CompanyID != uuid.Nil {
			company, err := e.masterdata.GetCompanyByIDContext(ctx, entry.CompanyID)
			if err != nil {
				return err
			}
			if !company.Active {
				return masterdata.ErrCompanyInactive
			}
			if err := validateFunctionalCurrency(*entry, company.FunctionalCurrency); err != nil {
				return err
			}
		}
		if entry.TargetPeriodNumber == 13 || entry.TargetPeriodNumber == 14 {
			if !entry.AdjustmentAuthorized {
				return ErrAdjustmentPeriodAuthorizationRequired
			}
			var period masterdata.FiscalPeriod
			var err error
			if entry.CompanyID != uuid.Nil {
				period, err = e.masterdata.GetOpenAdjustmentPeriodForCompanyDateContext(ctx, entry.CompanyID, effectiveDate(entry), entry.TargetPeriodNumber)
			} else {
				period, err = e.masterdata.GetOpenAdjustmentPeriodForDate(effectiveDate(entry), entry.TargetPeriodNumber)
			}
			if err != nil {
				return err
			}
			entry.PeriodID = period.ID
		} else if entry.TargetPeriodNumber != 0 {
			return masterdata.ErrFiscalPeriodNotFound
		} else {
			var period masterdata.FiscalPeriod
			var err error
			if entry.CompanyID != uuid.Nil {
				period, err = e.masterdata.GetOpenPeriodForCompanyDateContext(ctx, entry.CompanyID, effectiveDate(entry))
			} else {
				period, err = e.masterdata.GetOpenPeriodForDate(effectiveDate(entry))
			}
			if err != nil {
				return err
			}
			entry.PeriodID = period.ID
		}
		for _, line := range entry.Lines {
			var err error
			if entry.CompanyID != uuid.Nil {
				err = e.masterdata.ValidateCanPostToAccountForCompanyContext(ctx, entry.CompanyID, line.AccountID)
			} else {
				err = e.masterdata.ValidateCanPostToAccount(line.AccountID)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := e.checkMakerChecker(ctx, entry, posterID); err != nil {
		return err
	}
	return nil
}

func validateFunctionalCurrency(entry JournalEntry, expectedCurrency string) error {
	for _, line := range entry.Lines {
		resolved, err := normalizeLineCurrencies(line)
		if err != nil {
			return err
		}
		if resolved.FunctionalDebit.Currency() != expectedCurrency {
			return ErrFunctionalCurrencyMismatch
		}
	}
	return nil
}

func effectiveDate(entry *JournalEntry) time.Time {
	if !entry.EntryDate.IsZero() {
		return entry.EntryDate
	}
	return entry.Date
}

func (e *Engine) checkMakerChecker(ctx context.Context, entry *JournalEntry, posterID string) error {
	threshold := e.MakerCheckerThreshold
	configured := !threshold.IsZero()
	if e.makerCheckerPolicy != nil {
		policyThreshold, policyConfigured, err := e.makerCheckerPolicy.MakerCheckerThreshold(ctx, entry.CompanyID)
		if err != nil {
			return err
		}
		if policyConfigured {
			threshold, configured = policyThreshold, true
		}
	}
	if !configured {
		return nil
	}
	total, err := EntryFunctionalTotal(*entry)
	if err != nil {
		return err
	}
	if !total.Amount().GreaterThan(threshold) {
		return nil
	}
	if posterID == "" || entry.CreatedBy == posterID {
		return financialtypes.ErrMakerCheckerViolation
	}
	return nil
}

func (e *Engine) SetMakerCheckerThreshold(threshold decimal.Decimal) {
	e.MakerCheckerThreshold = threshold
}

func (e *Engine) GetEntry(ctx context.Context, companyID, entryID uuid.UUID) (JournalEntry, error) {
	return e.repo.GetEntryByIDForCompany(ctx, companyID, entryID)
}
