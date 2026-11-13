package posting

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/observability"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
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
	policyUnit            *policy.Unit
	logger                *slog.Logger
	metrics               *observability.Metrics
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

func (e *Engine) SetPolicyUnit(unit *policy.Unit) {
	e.policyUnit = unit
}

// SetObservability attaches the structured logger and metrics registry used to
// instrument posting latency and outcome.
func (e *Engine) SetObservability(logger *slog.Logger, metrics *observability.Metrics) {
	e.logger = logger
	e.metrics = metrics
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
	entry.Lines = append([]JournalEntryLine(nil), entry.Lines...)
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
	if e.masterdata != nil && entry.CompanyID != uuid.Nil && entry.PeriodID == uuid.Nil {
		var period masterdata.FiscalPeriod
		var err error
		switch entry.TargetPeriodNumber {
		case 13, 14:
			period, err = e.masterdata.GetAdjustmentPeriodForCompanyDateContext(
				ctx, entry.CompanyID, effectiveDate(&entry), entry.TargetPeriodNumber,
			)
		case 0:
			period, err = e.masterdata.GetPeriodForCompanyDateContext(ctx, entry.CompanyID, effectiveDate(&entry))
		default:
			return masterdata.ErrFiscalPeriodNotFound
		}
		if err != nil {
			return err
		}
		entry.PeriodID = period.ID
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.UpdatedAt = entry.CreatedAt
	if e.policyUnit != nil && entry.CompanyID != uuid.Nil {
		currencyPolicy, err := e.policyUnit.GetCurrencyPolicyAsOf(ctx, entry.CompanyID, effectiveDate(&entry))
		if err != nil {
			return err
		}
		if err := currencyPolicy.Validate(); err != nil {
			return err
		}
		if currencyPolicy.AutoSettleRoundingDifferences && hasSubMinorFunctionalAmounts(entry) {
			if err := RoundEntryWithRoundingPolicy(&entry, currencyPolicy.RoundingDifferenceAccountID, currencyPolicy.Round); err != nil {
				return err
			}
		} else if err := entry.ValidateWithRounding(currencyPolicy.Round); err != nil {
			return err
		}
		if err := validateCurrencyPolicyAmounts(entry, currencyPolicy); err != nil {
			return err
		}
	} else if err := entry.Validate(); err != nil {
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
	return e.postEntryContext(ctx, entry, posterID, true)
}

func (e *Engine) postEntryContext(ctx context.Context, entry *JournalEntry, posterID string, observe bool) (err error) {
	if entry != nil {
		ctx = observability.WithCompanyID(ctx, entry.CompanyID)
		ctx = observability.WithEntryID(ctx, entry.ID)
		ctx = observability.WithPeriodID(ctx, entry.PeriodID)
		if ensured, _, ensureErr := observability.EnsureTraceparent(ctx); ensureErr == nil {
			ctx = ensured
		}
	}
	start := time.Now()
	if observe {
		defer func() { e.recordPosting(ctx, entry, start, err) }()
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if entry.Status != EntryDraft {
		if entry.Status == EntryPosted {
			return financialtypes.ErrMoveAlreadyPosted
		}
		return ErrEntryNotDraft
	}
	if err = e.prepareForPosting(ctx, entry, posterID); err != nil {
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
		posted, postErr := repository.PostAndSequenceContext(ctx, *entry)
		if postErr != nil {
			entry.Status = EntryDraft
			entry.PostedBy = ""
			entry.PostedAt = nil
			return postErr
		}
		*entry = posted
		return nil
	}
	sequence, sequenceErr := e.repo.GetNextSequenceForCompanyContext(ctx, entry.CompanyID, entry.JournalID)
	if sequenceErr != nil {
		entry.Status = EntryDraft
		entry.PostedBy = ""
		entry.PostedAt = nil
		return sequenceErr
	}
	entry.SequenceNumber = sequence
	if postErr := e.repo.PostAtomicContext(ctx, *entry); postErr != nil {
		entry.Status = EntryDraft
		entry.PostedBy = ""
		entry.PostedAt = nil
		entry.SequenceNumber = 0
		return postErr
	}
	return nil
}

// recordPosting records the latency and outcome of one posting attempt and
// emits a structured, context-enriched log record. Failures are additionally
// counted under the engine-wide error taxonomy so operators can alert on
// authorization/conflict spikes separately from infrastructure faults.
func (e *Engine) recordPosting(ctx context.Context, entry *JournalEntry, start time.Time, err error) {
	duration := time.Since(start)
	if entry != nil {
		ctx = observability.WithPeriodID(ctx, entry.PeriodID)
	}
	if e.metrics != nil {
		e.metrics.IncPostingAttempts()
		e.metrics.ObservePostingDuration(duration.Seconds())
		e.metrics.ObserveTransactionDuration(duration.Seconds())
		if err == nil {
			e.metrics.IncPostedEntries()
		} else {
			_, category := ClassifyError(err)
			e.metrics.IncPostingFailure(string(category))
		}
	}
	if e.logger == nil {
		return
	}
	attrs := []any{"duration_seconds", duration.Seconds()}
	if entry != nil {
		attrs = append(attrs, "journal_id", entry.JournalID.String(), "status", string(entry.Status))
	}
	if err != nil {
		code, category := ClassifyError(err)
		attrs = append(attrs, "error", err.Error(), "error_code", string(code),
			"error_category", string(category))
		if entry != nil {
			attrs = append(attrs, "period_id", entry.PeriodID.String())
		}
		e.logger.WarnContext(ctx, "journal entry posting failed", attrs...)
		return
	}
	e.logger.InfoContext(ctx, "journal entry posted", attrs...)
}

func (e *Engine) PostEntryWithAttachment(entry *JournalEntry, posterID string, attachments ...LedgerAttachment) error {
	return e.PostEntryWithAttachmentContext(context.Background(), entry, posterID, attachments...)
}

func (e *Engine) PostEntryWithAttachmentContext(ctx context.Context, entry *JournalEntry, posterID string, attachments ...LedgerAttachment) error {
	return e.postEntryWithAttachmentContext(ctx, entry, posterID, attachments, true)
}

func (e *Engine) postEntryWithAttachmentContext(
	ctx context.Context,
	entry *JournalEntry,
	posterID string,
	attachments []LedgerAttachment,
	observe bool,
) (err error) {
	if entry != nil {
		ctx = observability.WithCompanyID(ctx, entry.CompanyID)
		ctx = observability.WithEntryID(ctx, entry.ID)
		ctx = observability.WithPeriodID(ctx, entry.PeriodID)
		if ensured, _, ensureErr := observability.EnsureTraceparent(ctx); ensureErr == nil {
			ctx = ensured
		}
	}
	start := time.Now()
	if observe {
		defer func() { e.recordPosting(ctx, entry, start, err) }()
	}
	if err = ctx.Err(); err != nil {
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
			if err = att.ValidateAttachment(ctx, *entry); err != nil {
				return err
			}
		}
	}
	if err = e.prepareForPosting(ctx, entry, posterID); err != nil {
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
		if postErr := repo.PostWithAttachmentsContext(ctx, entry, attachments); postErr != nil {
			*entry = original
			return postErr
		}
		return nil
	}
	if repo, ok := e.repo.(AtomicAttachmentRepository); ok {
		if postErr := repo.PostWithAttachments(ctx, entry, attachments); postErr != nil {
			*entry = original
			return postErr
		}
		return nil
	}

	*entry = original
	return ErrAtomicPostingOpenLineRequired
}

func (e *Engine) PostEntryWithOpenLine(entry *JournalEntry, line OpenLineAttachment, posterID string) error {
	return e.PostEntryWithOpenLineContext(context.Background(), entry, line, posterID)
}

func (e *Engine) PostEntryWithOpenLineContext(ctx context.Context, entry *JournalEntry, line OpenLineAttachment, posterID string) (err error) {
	if entry != nil {
		ctx = observability.WithCompanyID(ctx, entry.CompanyID)
		ctx = observability.WithEntryID(ctx, entry.ID)
		ctx = observability.WithPeriodID(ctx, entry.PeriodID)
		if ensured, _, ensureErr := observability.EnsureTraceparent(ctx); ensureErr == nil {
			ctx = ensured
		}
	}
	start := time.Now()
	defer func() { e.recordPosting(ctx, entry, start, err) }()

	attachment := &OpenLineLedgerAttachment{Attachment: line}
	err = e.postEntryWithAttachmentContext(ctx, entry, posterID, []LedgerAttachment{attachment}, false)
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
	if err = e.prepareForPosting(ctx, entry, posterID); err != nil {
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
	posted, postErr := repository.PostAndSequenceWithOpenLineContext(ctx, *entry, line)
	if postErr != nil {
		*entry = original
		return postErr
	}
	*entry = posted
	return nil
}

func (e *Engine) prepareForPosting(ctx context.Context, entry *JournalEntry, posterID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var currencyPolicy *policy.CurrencyPolicy
	if e.policyUnit != nil && entry.CompanyID != uuid.Nil {
		value, err := e.policyUnit.GetCurrencyPolicyAsOf(ctx, entry.CompanyID, effectiveDate(entry))
		if err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return err
		}
		if value.AutoSettleRoundingDifferences && hasSubMinorFunctionalAmounts(*entry) {
			if err := RoundEntryWithRoundingPolicy(entry, value.RoundingDifferenceAccountID, value.Round); err != nil {
				return err
			}
		}
		currencyPolicy = &value
	}
	if currencyPolicy != nil {
		if err := entry.ValidateWithRounding(currencyPolicy.Round); err != nil {
			return err
		}
	} else if err := entry.Validate(); err != nil {
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
	var postingPolicy *policy.PostingPolicy
	if e.policyUnit != nil && entry.CompanyID != uuid.Nil {
		date := effectiveDate(entry)
		postingCurrency, err := e.policyUnit.GetPostingPolicyAsOf(ctx, entry.CompanyID, date)
		if err != nil {
			return err
		}
		postingPolicy = &postingCurrency
		if err := validateCurrencyPolicyAmounts(*entry, *currencyPolicy); err != nil {
			return err
		}
		if backdatingDays := policy.BackdatingDays(date, time.Now().UTC()); backdatingDays > postingCurrency.MaxBackdatingDays {
			approved := false
			if postingCurrency.AllowBackdatingException {
				approved, err = e.policyUnit.AuthorizeBackdatingException(ctx, entry.CompanyID, entry.ID, posterID)
				if err != nil {
					return err
				}
			}
			if !approved {
				return policy.ErrBackdatingLimit
			}
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
			if currencyPolicy != nil {
				if !strings.EqualFold(company.FunctionalCurrency, currencyPolicy.FunctionalCurrency) {
					return ErrFunctionalCurrencyMismatch
				}
			}
		}
		if entry.TargetPeriodNumber == 13 || entry.TargetPeriodNumber == 14 {
			if !entry.AdjustmentAuthorized {
				return ErrAdjustmentPeriodAuthorizationRequired
			}
			var period masterdata.FiscalPeriod
			var err error
			if entry.CompanyID != uuid.Nil {
				if postingPolicy != nil {
					period, err = e.masterdata.GetAdjustmentPeriodForCompanyDateContext(ctx, entry.CompanyID, effectiveDate(entry), entry.TargetPeriodNumber)
					if err == nil {
						err = e.validatePolicyPeriod(ctx, entry.CompanyID, period, *postingPolicy, posterID)
					}
				} else {
					period, err = e.masterdata.GetOpenAdjustmentPeriodForCompanyDateContext(ctx, entry.CompanyID, effectiveDate(entry), entry.TargetPeriodNumber)
				}
			} else {
				period, err = e.masterdata.GetOpenAdjustmentPeriodForDateContext(ctx, effectiveDate(entry), entry.TargetPeriodNumber)
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
			if entry.CompanyID != uuid.Nil && postingPolicy != nil {
				period, err = e.masterdata.GetPeriodForCompanyDateContext(ctx, entry.CompanyID, effectiveDate(entry))
				if err == nil {
					err = e.validatePolicyPeriod(ctx, entry.CompanyID, period, *postingPolicy, posterID)
				}
			} else if entry.CompanyID != uuid.Nil {
				period, err = e.masterdata.GetOpenPeriodForCompanyDateContext(ctx, entry.CompanyID, effectiveDate(entry))
			} else {
				period, err = e.masterdata.GetOpenPeriodForDateContext(ctx, effectiveDate(entry))
			}
			if err != nil {
				return err
			}
			entry.PeriodID = period.ID
		}
		for _, line := range entry.Lines {
			if postingPolicy != nil && (postingPolicy.RequireCostCenterForIncomeExpense ||
				postingPolicy.RequireAnalyticalDimensionForIncomeExpense) {
				account, err := e.masterdata.GetAccountByIDForCompanyContext(ctx, entry.CompanyID, line.AccountID)
				if err != nil {
					return err
				}
				if account.AccountType == masterdata.AccountTypeIncome || account.AccountType == masterdata.AccountTypeExpense {
					if postingPolicy.RequireCostCenterForIncomeExpense && line.CostCenterID == nil {
						return ErrCostCenterRequired
					}
					if postingPolicy.RequireAnalyticalDimensionForIncomeExpense &&
						line.ProjectID == nil && line.SegmentID == nil && line.BusinessUnitID == nil {
						return ErrDimensionPolicyUnavailable
					}
				}
			}
			var err error
			if entry.CompanyID != uuid.Nil {
				err = e.masterdata.ValidateCanPostToAccountForCompanyContext(ctx, entry.CompanyID, line.AccountID)
			} else {
				err = e.masterdata.ValidateCanPostToAccountContext(ctx, line.AccountID)
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

func (e *Engine) validatePolicyPeriod(
	ctx context.Context,
	companyID uuid.UUID,
	period masterdata.FiscalPeriod,
	postingPolicy policy.PostingPolicy,
	posterID string,
) error {
	switch period.State {
	case masterdata.PeriodHardClosed:
		return policy.ErrHardClosedPeriod
	case masterdata.PeriodSoftClosed:
		if !postingPolicy.AllowSoftClosedPosting {
			return policy.ErrSoftClosedPeriod
		}
		if postingPolicy.RequireManagerForSoftClosed {
			return e.policyUnit.AuthorizeSoftClosedPosting(ctx, companyID, posterID)
		}
		return nil
	case masterdata.PeriodOpen:
		return nil
	default:
		return masterdata.ErrFiscalPeriodClosed
	}
}

func validateCurrencyPolicyAmounts(entry JournalEntry, currencyPolicy policy.CurrencyPolicy) error {
	for _, line := range entry.Lines {
		resolved, err := normalizeLineCurrencies(line)
		if err != nil {
			return err
		}
		if resolved.FunctionalDebit.Currency() != currencyPolicy.FunctionalCurrency {
			return ErrFunctionalCurrencyMismatch
		}
		for _, amount := range []financialtypes.Money{
			resolved.TransactionAmount, resolved.FunctionalDebit, resolved.FunctionalCredit,
		} {
			if err := currencyPolicy.ValidateMoney(amount); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasSubMinorFunctionalAmounts(entry JournalEntry) bool {
	for _, line := range entry.Lines {
		resolved, err := normalizeLineCurrencies(line)
		if err != nil {
			continue
		}
		if resolved.FunctionalDebit.HasSubMinorUnitFraction() || resolved.FunctionalCredit.HasSubMinorUnitFraction() {
			return true
		}
	}
	return false
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
