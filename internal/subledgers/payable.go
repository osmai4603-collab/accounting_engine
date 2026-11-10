package subledgers

import (
	"context"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
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

func (l *PayableLedger) AddInvoice(partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.addInvoice(uuid.Nil, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
	}
	return l.addInvoice(l.companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceWithControl(partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	if l.companyID == uuid.Nil {
		return l.addInvoice(uuid.Nil, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
	}
	return l.AddInvoiceForCompanyWithControl(l.companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceForCompany(companyID, partnerID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if companyID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return l.addInvoice(companyID, partnerID, uuid.Nil, uuid.Nil, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceForCompanyWithControl(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if companyID == uuid.Nil || controlAccountID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return l.addInvoice(companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference)
}

func (l *PayableLedger) AddInvoiceFromPostedEntry(partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	if l.companyID == uuid.Nil {
		return OpenLine{}, ErrInvalidOpenLine
	}
	return addPostedInvoice(l.repo, l.companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference, LinePayable)
}

func (l *PayableLedger) AddInvoiceFromPostedEntryForCompany(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
	return addPostedInvoice(l.repo, companyID, partnerID, controlAccountID, journalEntryID, amount, documentDate, dueDate, reference, LinePayable)
}

func (l *PayableLedger) PostInvoiceEntry(
	engine *posting.Engine,
	entry *posting.JournalEntry,
	partnerID, controlAccountID uuid.UUID,
	amount financialtypes.Money,
	documentDate, dueDate time.Time,
	reference, posterID string,
) (OpenLine, error) {
	return postInvoiceEntry(
		engine, entry, l.companyID, partnerID, controlAccountID, LinePayable,
		amount, documentDate, dueDate, reference, posterID,
	)
}

func (l *PayableLedger) addInvoice(companyID, partnerID, controlAccountID, journalEntryID uuid.UUID, amount financialtypes.Money, documentDate, dueDate time.Time, reference string) (OpenLine, error) {
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
	if err := l.repo.SaveOpenLine(context.Background(), line); err != nil {
		return OpenLine{}, err
	}
	return line, nil
}

func (l *PayableLedger) Aging(asOf time.Time, partnerID *uuid.UUID) (AgingSchedule, error) {
	partnerKey := ""
	if partnerID != nil {
		partnerKey = partnerID.String()
	}
	lines, err := l.listOpenLines(partnerKey)
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

func (l *PayableLedger) listOpenLines(partnerID string) ([]OpenLine, error) {
	if l.companyID == uuid.Nil {
		return l.repo.ListOpenLines(context.Background(), partnerID)
	}
	return l.repo.ListOpenLinesForCompanyContext(context.Background(), l.companyID, partnerID)
}
