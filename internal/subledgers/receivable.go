package subledgers

import (
	"context"
	"strings"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ReceivableLedger struct {
	repo      ReceivableRepository
	companyID uuid.UUID
}

type ReceivableRepository interface {
	OpenLineRepository
	CompanyOpenLineRepository
	PostedInvoiceRepository
	WriteOffRepository
}

func NewReceivableLedger(repo ReceivableRepository) *ReceivableLedger {
	return &ReceivableLedger{repo: repo}
}

func NewReceivableLedgerForCompany(repo ReceivableRepository, companyID uuid.UUID) *ReceivableLedger {
	return &ReceivableLedger{repo: repo, companyID: companyID}
}

// Deprecated: use AddInvoiceContext.
func (l *ReceivableLedger) AddInvoice(partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceContext(context.Background(), partnerID, amount, documentDate, dueDate, reference)
}

func (l *ReceivableLedger) AddInvoiceContext(ctx context.Context, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.addInvoiceContext(ctx, uuid.Nil, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
	}
	return l.addInvoiceContext(ctx, l.companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceWithControlContext.
func (l *ReceivableLedger) AddInvoiceWithControl(partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceWithControlContext(context.Background(), partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *ReceivableLedger) AddInvoiceWithControlContext(ctx context.Context, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	if l.companyID == uuid.Nil {
		return l.addInvoiceContext(ctx, uuid.Nil, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
	}
	return l.AddInvoiceForCompanyWithControlContext(ctx, l.companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceForCompanyContext.
func (l *ReceivableLedger) AddInvoiceForCompany(companyID, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceForCompanyContext(context.Background(), companyID, partnerID, amount, documentDate, dueDate, reference)
}

func (l *ReceivableLedger) AddInvoiceForCompanyContext(ctx context.Context, companyID, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := l.validateCompany(companyID); err != nil {
		return OpenLine{}, err
	}
	return l.addInvoiceContext(ctx, companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceForCompanyWithControlContext.
func (l *ReceivableLedger) AddInvoiceForCompanyWithControl(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceForCompanyWithControlContext(context.Background(), companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *ReceivableLedger) AddInvoiceForCompanyWithControlContext(ctx context.Context, companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := l.validateCompany(companyID); err != nil {
		return OpenLine{}, err
	}
	if controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return l.addInvoiceContext(ctx, companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

// Deprecated: use AddInvoiceFromPostedEntryContext.
func (l *ReceivableLedger) AddInvoiceFromPostedEntry(partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceFromPostedEntryContext(context.Background(), partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *ReceivableLedger) AddInvoiceFromPostedEntryContext(ctx context.Context, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if l.companyID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return addPostedInvoice(ctx, l.repo, l.companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference, LineReceivable)
}

// Deprecated: use AddInvoiceFromPostedEntryForCompanyContext.
func (l *ReceivableLedger) AddInvoiceFromPostedEntryForCompany(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return l.AddInvoiceFromPostedEntryForCompanyContext(context.Background(), companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *ReceivableLedger) AddInvoiceFromPostedEntryForCompanyContext(ctx context.Context, companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := l.validateCompany(companyID); err != nil {
		return OpenLine{}, err
	}
	return addPostedInvoice(ctx, l.repo, companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference, LineReceivable)
}

func (l *ReceivableLedger) validateCompany(companyID uuid.UUID) error {
	if companyID == uuid.Nil {
		return ErrCompanyScopeRequired
	}
	if l.companyID != uuid.Nil && l.companyID != companyID {
		return ErrCompanyMismatch
	}
	return nil
}

// Deprecated: use PostInvoiceEntryContext.
func (l *ReceivableLedger) PostInvoiceEntry(
	engine *posting.Engine,
	entry *posting.JournalEntry,
	partnerID, controlAccountID uuid.UUID,
	amount financialtypes.Money,
	documentDate, dueDate time.Time,
	reference, posterID string,
) (OpenLine, error) {
	return l.PostInvoiceEntryContext(context.Background(), engine, entry, partnerID, controlAccountID, amount, documentDate, dueDate, reference, posterID)
}

func (l *ReceivableLedger) PostInvoiceEntryContext(
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
		engine, entry, l.companyID, partnerID, controlAccountID, LineReceivable,
		amount, documentDate, dueDate, reference, posterID,
	)
}

func (l *ReceivableLedger) addInvoiceContext(ctx context.Context, companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return OpenLine{}, err
	}
	if partnerID == uuid.Nil || amount.Currency() == "" || amount.IsNegative() {
		return OpenLine{}, ErrInvalidAmount
	}
	line := OpenLine{
		CompanyID:                companyID,
		ID:                       l.newLineID(),
		PartnerID:                &partnerID,
		ControlAccountID:         controlAccountID,
		JournalEntryID:           journalEntryID,
		Kind:                     LineReceivable,
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

func (l *ReceivableLedger) newLineID() uuid.UUID { return uuid.New() }

// Deprecated: use AgingContext.
func (l *ReceivableLedger) Aging(asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
	return l.AgingContext(context.Background(), asOf, partnerID)
}

func (l *ReceivableLedger) AgingContext(ctx context.Context, asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
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
	result := make(AgingSchedule)
	currency := lines[0].ResidualAmount.Currency()
	for _, bucket := range []AgingBucket{AgingCurrent, Aging1To30, Aging31To60, Aging61To90, AgingOver90} {
		zero, _ := financialtypes.Zero(currency)
		result[bucket] = zero
	}
	for _, line := range lines {
		if line.Kind != LineReceivable || line.ResidualAmount.Amount().IsZero() {
			continue
		}
		bucket := agingBucket(asOf, line.DueDate)
		total, err := result[bucket].Add(line.ResidualAmount)
		if err != nil {
			return nil, err
		}
		result[bucket] = total
	}
	return result, nil
}

func (l *ReceivableLedger) listOpenLines(ctx context.Context, partnerID string) ([]OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.repo.ListOpenLines(ctx, partnerID)
	}
	return l.repo.ListOpenLinesForCompanyContext(ctx, l.companyID, partnerID)
}

func agingBucket(asOf, dueDate time.Time) AgingBucket {
	if !dueDate.Before(asOf) || dueDate.Equal(asOf) {
		return AgingCurrent
	}
	days := int(asOf.Sub(dueDate) / (24 * time.Hour))
	if days <= 30 {
		return Aging1To30
	}
	if days <= 60 {
		return Aging31To60
	}
	if days <= 90 {
		return Aging61To90
	}
	return AgingOver90
}

func (l *ReceivableLedger) CalculateECL(asOf time.Time, partnerID *uuid.UUID, probabilityByBucket map[AgingBucket]decimal.Decimal) (financialtypes.Money, error) {
	schedule, err := l.Aging(asOf, partnerID)
	if err != nil || schedule == nil {
		return financialtypes.Money{}, err
	}
	currency := ""
	for _, amount := range schedule {
		currency = amount.Currency()
		break
	}
	total, _ := financialtypes.Zero(currency)
	for bucket, amount := range schedule {
		probability, ok := probabilityByBucket[bucket]
		if !ok {
			probability = decimal.Zero
		}
		expected, err := amount.Multiply(probability)
		if err != nil {
			return financialtypes.Money{}, err
		}
		total, err = total.Add(expected)
		if err != nil {
			return financialtypes.Money{}, err
		}
	}
	return total, nil
}

// Deprecated: use CloseOpenLineContext.
func (l *ReceivableLedger) CloseOpenLine(lineID uuid.UUID) error {
	return l.CloseOpenLineContext(context.Background(), lineID)
}

func (l *ReceivableLedger) CloseOpenLineContext(ctx context.Context, lineID uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := l.repo.GetOpenLine(ctx, l.companyID, lineID)
	if err != nil {
		return err
	}
	line.Closed = true
	return l.repo.SaveOpenLine(ctx, line)
}

type ReceivableMatrix map[AgingBucket]decimal.Decimal

func (m ReceivableMatrix) Validate() error {
	for bucket, probability := range m {
		if strings.TrimSpace(string(bucket)) == "" ||
			probability.IsNegative() || probability.GreaterThan(decimal.NewFromInt(1)) {
			return ErrInvalidAmount
		}
	}
	return nil
}
