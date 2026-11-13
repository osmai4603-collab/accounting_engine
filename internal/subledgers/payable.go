package subledgers

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

type PayableLedger struct {
	repo      PayableRepository
	companyID uuid.UUID
}

type PayableRepository interface {
	OpenLineRepository
	CompanyOpenLineRepository
	PostedInvoiceRepository
	WriteOffRepository
}

func NewPayableLedger(repo PayableRepository) *PayableLedger {
	return &PayableLedger{repo: repo}
}

func NewPayableLedgerForCompany(repo PayableRepository, companyID uuid.UUID) *PayableLedger {
	return &PayableLedger{repo: repo, companyID: companyID}
}

// Deprecated: use AddInvoiceContext.
func (l *PayableLedger) AddInvoice(partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceContext(context.Background(), partnerID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceContext(ctx context.Context, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.addInvoiceContext(ctx, uuid.Nil, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
	}
	return l.addInvoiceContext(ctx, l.companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceWithControlContext.
func (l *PayableLedger) AddInvoiceWithControl(partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceWithControlContext(context.Background(), partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceWithControlContext(ctx context.Context, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	if l.companyID == uuid.Nil {
		return l.addInvoiceContext(ctx, uuid.Nil, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
	}
	return l.AddInvoiceForCompanyWithControlContext(ctx, l.companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceForCompanyContext.
func (l *PayableLedger) AddInvoiceForCompany(companyID, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceForCompanyContext(context.Background(), companyID, partnerID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceForCompanyContext(ctx context.Context, companyID, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := l.validateCompany(companyID); err != nil {
		return OpenLine{}, err
	}
	return l.addInvoiceContext(ctx, companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceForCompanyWithControlContext.
func (l *PayableLedger) AddInvoiceForCompanyWithControl(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceForCompanyWithControlContext(context.Background(), companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceForCompanyWithControlContext(ctx context.Context, companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := l.validateCompany(companyID); err != nil {
		return OpenLine{}, err
	}
	if controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return l.addInvoiceContext(ctx, companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceFromPostedEntryContext.
func (l *PayableLedger) AddInvoiceFromPostedEntry(partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceFromPostedEntryContext(context.Background(), partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceFromPostedEntryContext(ctx context.Context, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if l.companyID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return addPostedInvoice(ctx, l.repo, l.companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference, LinePayable)
}

// Deprecated: use AddInvoiceFromPostedEntryForCompanyContext.
func (l *PayableLedger) AddInvoiceFromPostedEntryForCompany(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceFromPostedEntryForCompanyContext(context.Background(), companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceFromPostedEntryForCompanyContext(ctx context.Context, companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := l.validateCompany(companyID); err != nil {
		return OpenLine{}, err
	}
	return addPostedInvoice(ctx, l.repo, companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference, LinePayable)
}

func (l *PayableLedger) validateCompany(companyID uuid.UUID) error {
	if companyID == uuid.Nil {
		return ErrCompanyScopeRequired
	}
	if l.companyID != uuid.Nil && l.companyID != companyID {
		return ErrCompanyMismatch
	}
	return nil
}

// Deprecated: use PostInvoiceEntryContext.
func (l *PayableLedger) PostInvoiceEntry(
	engine *posting.Engine,
	entry *posting.JournalEntry,
	partnerID, controlAccountID uuid.UUID,
	amount financialtypes.Money,
	documentDate, dueDate time.Time,
	reference, posterID string,
) (OpenLine, error) {
	return l.PostInvoiceEntryContext(context.Background(), engine, entry, partnerID, controlAccountID, amount, documentDate, dueDate, reference, posterID)
}

func (l *PayableLedger) PostInvoiceEntryContext(
	ctx context.Context,
	engine *posting.Engine,
	entry *posting.JournalEntry,
	partnerID, controlAccountID uuid.UUID,
	amount financialtypes.Money,
	documentDate, dueDate time.Time,
	reference, posterID string,
) (OpenLine, error) {
	return postInvoiceEntryContext(
		ctx,
		engine, entry, l.companyID, partnerID, controlAccountID, LinePayable,
		amount, documentDate, dueDate, reference, posterID,
	)
}

func (l *PayableLedger) addInvoiceContext(ctx context.Context, companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return OpenLine{}, err
	}
	if partnerID == uuid.Nil || amount.Currency() == "" || amount.IsNegative() {
		return OpenLine{}, ErrInvalidAmount
	}
	line := OpenLine{
		CompanyID:                companyID,
		ID:                       uuid.New(),
		PartnerID:                &partnerID,
		ControlAccountID:         controlAccountID,
		JournalEntryID:           journalEntryID,
		Kind:                     LinePayable,
		OriginalAmount:           amount,
		ResidualAmount:           amount,
		FunctionalOriginalAmount: amount,
		FunctionalResidualAmount: amount,
		PostingDate:              documentDate,
		DocumentDate:             documentDate,
		DueDate:                  dueDate,
		Reference:                reference,
	}
	if err := l.repo.SaveOpenLine(ctx, line); err != nil {
		return OpenLine{}, err
	}
	return line, nil
}

// Deprecated: use AgingContext.
func (l *PayableLedger) Aging(asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
	return l.AgingContext(context.Background(), asOf, partnerID)
}

func (l *PayableLedger) AgingContext(ctx context.Context, asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
	partnerKey := ""
	if partnerID != nil {
		partnerKey = partnerID.String()
	}
	lines, err := l.listOpenLines(ctx, partnerKey)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, nil
	}
	currency := lines[0].ResidualAmount.Currency()
	result := make(AgingSchedule)
	for _, bucket := range []AgingBucket{AgingCurrent, Aging1To30, Aging31To60, Aging61To90, AgingOver90} {
		zero, err := financialtypes.Zero(currency)
		if err != nil {
			return nil, err
		}
		result[bucket] = zero
	}
	for _, line := range lines {
		if line.Kind != LinePayable || line.ResidualAmount.Amount().IsZero() {
			continue
		}
		bucket := agingBucket(asOf, line.DueDate)
		amount, err := result[bucket].Add(line.ResidualAmount)
		if err != nil {
			return nil, err
		}
		result[bucket] = amount
	}
	return result, nil
}

func (l *PayableLedger) listOpenLines(ctx context.Context, partnerID string) ([]OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.repo.ListOpenLines(ctx, partnerID)
	}
	return l.repo.ListOpenLinesForCompanyContext(ctx, l.companyID, partnerID)
}
